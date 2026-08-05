package assurance

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestFactGraphSharesCompatibleAnalysis(t *testing.T) {
	root := t.TempDir()
	writeAssuranceFile(t, root, "src/main.go", "package main\n\nfunc run() {\n\tGuardedClient()\n\thttp.Get(\"https://example.test\")\n\tApplyHardening()\n\texec.Command(\"tool\")\n}\n")
	writeAssuranceFile(t, root, "package.json", `{"packageManager":"npm@11.4.2","scripts":{"test":"node --test"},"dependencies":{"react":"19.1.0"}}`)
	gates := []policy.AssuranceGate{
		{ID: "pins", Type: policy.AssuranceDependencyPins, ManifestPaths: []string{"package.json"}, DependencySections: []string{"dependencies"}},
		{ID: "scripts", Type: policy.AssurancePackageScripts, ManifestPaths: []string{"package.json"}, PackageManager: "npm", Commands: []string{"npm run test"}},
		{ID: "network-a", Type: policy.AssuranceNetworkBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"http.Get("}, GuardMarkers: []string{"GuardedClient"}, MarkerWindowLines: 2},
		{ID: "network-b", Type: policy.AssuranceNetworkBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"http.Get("}, GuardMarkers: []string{"GuardedClient"}, MarkerWindowLines: 2},
		{ID: "process", Type: policy.AssuranceProcessBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"exec.Command("}, GuardMarkers: []string{"ApplyHardening"}, MarkerWindowLines: 2},
		{ID: "concurrency-a", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"src/**"}},
		{ID: "concurrency-b", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"src/**"}},
		{ID: "format-a", Type: policy.AssuranceGoFormat, ScanPaths: []string{"src/**"}},
		{ID: "format-b", Type: policy.AssuranceGoFormat, ScanPaths: []string{"src/**"}},
		{ID: "hygiene-a", Type: policy.AssuranceSourceHygiene, ScanPaths: []string{"src/**"}},
		{ID: "hygiene-b", Type: policy.AssuranceSourceHygiene, ScanPaths: []string{"src/**"}},
	}
	findings, stats, err := evaluateWithStats(root, gates, Inputs{
		ChangedPaths:       []string{"src/main.go", "package.json"},
		SuccessfulCommands: []string{"npm run test"},
	})
	if err != nil || len(findings) != 0 {
		t.Fatalf("fact-graph evaluation = findings=%+v err=%v", findings, err)
	}
	if stats.BodyReads != 2 || stats.LineBuilds != 1 || stats.JSONParses != 1 || stats.GoParses != 1 || stats.GoFormats != 1 {
		t.Fatalf("compatible analysis was repeated: %+v", stats)
	}
}

func TestFactGraphWorkerLimitsAreDifferentiallyStable(t *testing.T) {
	root := t.TempDir()
	changed := make([]string, 0, 64)
	for index := 0; index < 64; index++ {
		relative := filepath.ToSlash(filepath.Join("src", "file-"+strconv.Itoa(index)+".go"))
		content := "package p\n\nfunc run() {}\n"
		if index == 5 {
			content = "package p\n\nfunc run() {\n\tgo func() {}()\n}\n"
		}
		if index == 9 {
			content = "package p\nfunc unformatted( ){}\n"
		}
		writeAssuranceFile(t, root, relative, content)
		changed = append(changed, relative)
	}
	gates := []policy.AssuranceGate{
		{ID: "concurrency", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"src/**"}},
		{ID: "format", Type: policy.AssuranceGoFormat, ScanPaths: []string{"src/**"}},
		{ID: "hygiene", Type: policy.AssuranceSourceHygiene, ScanPaths: []string{"src/**"}},
	}
	serial, serialStats, serialErr := evaluateWithWorkerLimit(root, gates, Inputs{ChangedPaths: changed}, 1)
	parallel, parallelStats, parallelErr := evaluateWithWorkerLimit(root, gates, Inputs{ChangedPaths: changed}, maxAnalysisWorkers)
	if fmt.Sprint(serialErr) != fmt.Sprint(parallelErr) || !reflect.DeepEqual(serial, parallel) {
		t.Fatalf("worker-limit drift:\nserial=%+v err=%v\nparallel=%+v err=%v", serial, serialErr, parallel, parallelErr)
	}
	if serialStats != parallelStats {
		t.Fatalf("worker-limit analysis counts drifted: serial=%+v parallel=%+v", serialStats, parallelStats)
	}
}

func TestFactGraphParallelMalformedSourceOwnsFirstSortedError(t *testing.T) {
	root := t.TempDir()
	changed := make([]string, 0, 64)
	for index := 0; index < 64; index++ {
		relative := filepath.ToSlash(filepath.Join("src", fmt.Sprintf("%03d.go", index)))
		content := "package p\n\nfunc run() {}\n"
		if index < 2 {
			content = "package p\nfunc broken( {\n"
		}
		writeAssuranceFile(t, root, relative, content)
		changed = append(changed, relative)
	}
	gate := policy.AssuranceGate{ID: "concurrency", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"src/**"}}
	_, _, serialErr := evaluateWithWorkerLimit(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: changed}, 1)
	_, _, parallelErr := evaluateWithWorkerLimit(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: changed}, maxAnalysisWorkers)
	if serialErr == nil || parallelErr == nil || serialErr.Error() != parallelErr.Error() || !strings.Contains(serialErr.Error(), "src/000.go") {
		t.Fatalf("first malformed-source error drifted: serial=%v parallel=%v", serialErr, parallelErr)
	}
}

func TestGoGatesRejectNonGoClassesBeforeFilesystemAnalysis(t *testing.T) {
	root := t.TempDir()
	changed := make([]string, 0, 1_000)
	for index := 0; index < 1_000; index++ {
		changed = append(changed, "src/file-"+strconv.Itoa(index)+".ts")
	}
	gates := []policy.AssuranceGate{
		{ID: "concurrency", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"src/**"}},
		{ID: "format", Type: policy.AssuranceGoFormat, ScanPaths: []string{"src/**"}},
	}
	findings, stats, err := evaluateWithStats(root, gates, Inputs{ChangedPaths: changed})
	if err != nil || len(findings) != 0 {
		t.Fatalf("non-Go class rejection = findings=%+v err=%v", findings, err)
	}
	if stats.BodyReads != 0 || stats.GoParses != 0 || stats.GoFormats != 0 || stats.PathResolutions != 0 {
		t.Fatalf("non-Go paths reached expensive analysis: %+v", stats)
	}
}

func TestGoFormatFactMatchesStandardSourceFormatter(t *testing.T) {
	cases := []string{
		"package p\n\nfunc run() {}\n",
		"//go:build linux\n\npackage p\n\n// Run documents run.\nfunc Run( ){}\n",
		"package p\n\nimport (\n\t\"strings\"\n\t\"fmt\"\n)\n\nvar _, _ = fmt.Println, strings.TrimSpace\n",
		"package p\r\n\r\nfunc run( ) {}\r\n",
	}
	for index, content := range cases {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			root := t.TempDir()
			relative := "source.go"
			writeAssuranceFile(t, root, relative, content)
			body, err := os.ReadFile(filepath.Join(root, relative))
			if err != nil {
				t.Fatal(err)
			}
			expected, expectedErr := format.Source(body)
			state := newEvaluationState(nil, 1)
			matched, actualErr := state.goFormatMatches(changedFile{relative: relative, full: filepath.Join(root, relative)})
			if fmt.Sprint(expectedErr) != fmt.Sprint(actualErr) || matched != bytes.Equal(body, expected) {
				t.Fatalf("formatter parity = matched=%t err=%v; standard matched=%t err=%v", matched, actualErr, bytes.Equal(body, expected), expectedErr)
			}
		})
	}
}

func BenchmarkEvaluateAssuranceFactGraph(b *testing.B) {
	for _, fileCount := range []int{10, 1_000, maxScannedFiles} {
		b.Run(strconv.Itoa(fileCount)+"-files", func(b *testing.B) {
			root := b.TempDir()
			changed := make([]string, 0, fileCount)
			content := []byte("package pkg\n\nfunc run() {\n\tGuardedClient()\n\thttp.Get(\"https://example.test\")\n\tApplyHardening()\n\texec.Command(\"tool\")\n}\n")
			for index := 0; index < fileCount; index++ {
				relative := filepath.ToSlash(filepath.Join("src", "pkg", "file-"+strconv.Itoa(index)+".go"))
				full := filepath.Join(root, filepath.FromSlash(relative))
				if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
					b.Fatal(err)
				}
				if err := os.WriteFile(full, content, 0o644); err != nil {
					b.Fatal(err)
				}
				changed = append(changed, relative)
			}
			gates := mixedSourceBenchmarkGates()
			var totals analysisStats
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				findings, stats, err := evaluateWithStats(root, gates, Inputs{ChangedPaths: changed})
				if err != nil || len(findings) != 0 {
					b.Fatalf("evaluation = findings=%+v err=%v", findings, err)
				}
				totals.BodyReads += stats.BodyReads
				totals.LineBuilds += stats.LineBuilds
				totals.JSONParses += stats.JSONParses
				totals.GoParses += stats.GoParses
				totals.GoFormats += stats.GoFormats
				totals.PathMatches += stats.PathMatches
				totals.PathResolutions += stats.PathResolutions
				totals.Files += stats.Files
				totals.Bytes += stats.Bytes
			}
			operations := float64(b.N)
			b.ReportMetric(float64(totals.Bytes)/operations, "read-bytes/op")
			b.ReportMetric(float64(totals.BodyReads)/operations, "file-reads/op")
			b.ReportMetric(float64(totals.LineBuilds)/operations, "line-builds/op")
			b.ReportMetric(float64(totals.JSONParses)/operations, "json-parses/op")
			b.ReportMetric(float64(totals.GoParses)/operations, "go-parses/op")
			b.ReportMetric(float64(totals.GoFormats)/operations, "go-formats/op")
			b.ReportMetric(float64(totals.PathMatches)/operations, "path-matches/op")
			b.ReportMetric(float64(totals.PathResolutions)/operations, "path-resolutions/op")
			b.ReportMetric(float64(totals.Files)/operations, "files/op")
		})
	}
}

func mixedSourceBenchmarkGates() []policy.AssuranceGate {
	return []policy.AssuranceGate{
		{ID: "language", Type: policy.AssuranceLanguageBoundary, ScanPaths: []string{"src/**"}, AllowedExtensions: []string{".go"}},
		{ID: "network", Type: policy.AssuranceNetworkBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"http.Get("}, GuardMarkers: []string{"GuardedClient"}, MarkerWindowLines: 2},
		{ID: "process", Type: policy.AssuranceProcessBoundary, ScanPaths: []string{"src/**"}, SitePatterns: []string{"exec.Command("}, GuardMarkers: []string{"ApplyHardening"}, MarkerWindowLines: 2},
		{ID: "concurrency", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"src/**"}},
		{ID: "format", Type: policy.AssuranceGoFormat, ScanPaths: []string{"src/**"}},
		{ID: "hygiene", Type: policy.AssuranceSourceHygiene, ScanPaths: []string{"src/**"}},
	}
}
