package assurance

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	maxAnalysisWorkers = 4
	minParallelFiles   = 32
)

type analysisCounters struct {
	bodyReads                     atomic.Int64
	lineBuilds                    atomic.Int64
	jsonParses                    atomic.Int64
	goParses                      atomic.Int64
	goFormats                     atomic.Int64
	pathMatches                   atomic.Int64
	pathResolutions               atomic.Int64
	packageManagerDirectoryProbes atomic.Int64
	packageManagerLockProbes      atomic.Int64
}

type analysisStats struct {
	BodyReads                     int64
	LineBuilds                    int64
	JSONParses                    int64
	GoParses                      int64
	GoFormats                     int64
	PathMatches                   int64
	PathResolutions               int64
	PackageManagerDirectoryProbes int64
	PackageManagerLockProbes      int64
	Files                         int64
	Bytes                         int64
}

type fileFacts struct {
	path       string
	bodyLoaded bool
	bodyBytes  []byte
	bodyErr    error

	linesLoaded bool
	lines       []string

	jsonObjects [2]jsonObjectFacts

	packageLoaded bool
	packageDoc    packageScriptDocument
	packageErr    error

	goSyntaxLoaded bool
	goFileSet      *token.FileSet
	goTree         *ast.File
	goSyntaxErr    error

	goFormatLoaded bool
	goFormatted    bool
	goFormatErr    error
}

type jsonObjectFacts struct {
	loaded   bool
	document map[string]json.RawMessage
	err      error
}

func (state *evaluationState) fact(path string) *fileFacts {
	fact := state.facts[path]
	if fact == nil {
		fact = &fileFacts{path: path}
		state.facts[path] = fact
	}
	return fact
}

func (fact *fileFacts) body(state *evaluationState) ([]byte, error) {
	if fact.bodyLoaded {
		return fact.bodyBytes, fact.bodyErr
	}
	fact.bodyLoaded = true
	fact.bodyBytes, fact.bodyErr = readBounded(fact.path, state.budget)
	if fact.bodyErr == nil {
		state.stats.bodyReads.Add(1)
	}
	return fact.bodyBytes, fact.bodyErr
}

func (state *evaluationState) lines(path string) ([]string, error) {
	fact := state.fact(path)
	if fact.linesLoaded {
		return fact.lines, fact.bodyErr
	}
	body, err := fact.body(state)
	if err != nil {
		return nil, err
	}
	fact.linesLoaded = true
	state.stats.lineBuilds.Add(1)
	fact.lines = strings.Split(string(body), "\n")
	return fact.lines, nil
}

func (state *evaluationState) jsonObject(path string, trimBOM bool) (map[string]json.RawMessage, error) {
	fact := state.fact(path)
	body, err := fact.body(state)
	if err != nil {
		return nil, err
	}
	variant := 0
	if trimBOM && bytes.HasPrefix(body, []byte{0xef, 0xbb, 0xbf}) {
		variant = 1
		body = body[3:]
	}
	cached := &fact.jsonObjects[variant]
	if !cached.loaded {
		cached.loaded = true
		state.stats.jsonParses.Add(1)
		cached.err = json.Unmarshal(body, &cached.document)
	}
	return cached.document, cached.err
}

func (state *evaluationState) packageDocument(path string) (packageScriptDocument, error) {
	fact := state.fact(path)
	if fact.packageLoaded {
		return fact.packageDoc, fact.packageErr
	}
	fact.packageLoaded = true
	document, err := state.jsonObject(path, true)
	if err != nil {
		fact.packageErr = err
		return fact.packageDoc, fact.packageErr
	}
	if raw, present := document["packageManager"]; present {
		if err := json.Unmarshal(raw, &fact.packageDoc.PackageManager); err != nil {
			fact.packageErr = err
			return fact.packageDoc, fact.packageErr
		}
	}
	if raw, present := document["scripts"]; present {
		if err := json.Unmarshal(raw, &fact.packageDoc.Scripts); err != nil {
			fact.packageErr = err
			return fact.packageDoc, fact.packageErr
		}
	}
	return fact.packageDoc, nil
}

func (state *evaluationState) goSyntax(file changedFile) (*token.FileSet, *ast.File, error) {
	fact := state.fact(file.full)
	fact.loadGoSyntax(state, file.relative)
	return fact.goFileSet, fact.goTree, fact.goSyntaxErr
}

func (fact *fileFacts) loadGoSyntax(state *evaluationState, relative string) {
	if fact.goSyntaxLoaded {
		return
	}
	fact.goSyntaxLoaded = true
	body, err := fact.body(state)
	if err != nil {
		fact.goSyntaxErr = err
		return
	}
	state.stats.goParses.Add(1)
	fact.goFileSet = token.NewFileSet()
	fact.goTree, fact.goSyntaxErr = parser.ParseFile(fact.goFileSet, relative, body, parser.SkipObjectResolution|parser.ParseComments)
}

func (state *evaluationState) goFormatMatches(file changedFile) (bool, error) {
	fact := state.fact(file.full)
	fact.loadGoFormat(state, file.relative)
	return fact.goFormatted, fact.goFormatErr
}

func (fact *fileFacts) loadGoFormat(state *evaluationState, relative string) {
	if fact.goFormatLoaded {
		return
	}
	fact.goFormatLoaded = true
	fact.loadGoSyntax(state, relative)
	if fact.goSyntaxErr != nil {
		fact.goFormatErr = fact.goSyntaxErr
		return
	}
	state.stats.goFormats.Add(1)
	var formatted bytes.Buffer
	if err := format.Node(&formatted, fact.goFileSet, fact.goTree); err != nil {
		fact.goFormatErr = err
		return
	}
	fact.goFormatted = bytes.Equal(fact.bodyBytes, formatted.Bytes())
}

func (state *evaluationState) prepareGoFacts(files []changedFile, syntax, formatting bool) {
	if len(files) < minParallelFiles {
		return
	}
	type job struct {
		fact     *fileFacts
		relative string
	}
	jobsToRun := make([]job, 0, len(files))
	seen := make(map[*fileFacts]bool, len(files))
	for _, file := range files {
		fact := state.fact(file.full)
		if seen[fact] {
			continue
		}
		seen[fact] = true
		jobsToRun = append(jobsToRun, job{fact: fact, relative: file.relative})
		if _, err := fact.body(state); err != nil {
			break
		}
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > state.analysisWorkers {
		workers = state.analysisWorkers
	}
	if workers > len(jobsToRun) {
		workers = len(jobsToRun)
	}
	jobs := make(chan job)
	var group sync.WaitGroup
	group.Add(workers)
	for range workers {
		go func() {
			defer group.Done()
			for job := range jobs {
				if syntax {
					job.fact.loadGoSyntax(state, job.relative)
				}
				if formatting {
					job.fact.loadGoFormat(state, job.relative)
				}
			}
		}()
	}
	for _, job := range jobsToRun {
		jobs <- job
	}
	close(jobs)
	group.Wait()
}

func (state *evaluationState) analysisStats() analysisStats {
	return analysisStats{
		BodyReads:                     state.stats.bodyReads.Load(),
		LineBuilds:                    state.stats.lineBuilds.Load(),
		JSONParses:                    state.stats.jsonParses.Load(),
		GoParses:                      state.stats.goParses.Load(),
		GoFormats:                     state.stats.goFormats.Load(),
		PathMatches:                   state.stats.pathMatches.Load(),
		PathResolutions:               state.stats.pathResolutions.Load(),
		PackageManagerDirectoryProbes: state.stats.packageManagerDirectoryProbes.Load(),
		PackageManagerLockProbes:      state.stats.packageManagerLockProbes.Load(),
		Files:                         int64(len(state.budget.files)),
		Bytes:                         state.budget.bytes,
	}
}
