package cli

import (
	"fmt"
	"io"
	"os"
	"time"
	"unicode/utf8"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/cireport"
	"reconc.dev/reconc/internal/compiler"
	"reconc.dev/reconc/internal/impactlab"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/presets"
	"reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

const maxImpactCandidateBytes int64 = 8 << 20

func runImpact(args []string, version string, stdout io.Writer) error {
	if len(args) > 0 && args[0] == "export" {
		return runImpactExport(args[1:], stdout)
	}
	options, help, err := parseImpactCompareOptions(args, stdout)
	if err != nil || help {
		return err
	}
	candidate, body, err := compileImpactCandidate(options, version)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc impact: " + err.Error()}
	}
	evaluator, err := runtime.NewCompiledPolicyEvaluator(body)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc impact: prepare candidate: " + err.Error()}
	}
	actionRuntime, err := evaluator.ActionRuntime()
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc impact: prepare candidate actions: " + err.Error()}
	}
	candidate.metadata.LockDigest = actionRuntime.LockDigest
	candidate.metadata.ActionPlanIdentity = actionRuntime.Evaluator.PlanIdentity()
	candidate.metadata.ActionToolCount = actionRuntime.ToolCount
	candidate.metadata.ActionRuleCount = actionRuntime.ActionRuleCount
	corpus, err := loadImpactCorpora(options)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc impact: " + err.Error()}
	}
	current, currentSourceDigest, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(candidate.repoRoot)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc impact: prepare current policy: " + err.Error()}
	}
	if currentSourceDigest != candidate.baseSourceDigest {
		return &CLIError{ExitCode: 1, Message: "reconc impact: policy sources changed during comparison; retry with a stable repository"}
	}
	report, err := impactlab.Compare(candidate.repoRoot, corpus, candidate.metadata, current, evaluator)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc impact: compare: " + err.Error()}
	}
	if options.deltaManifest != "" {
		manifest, manifestErr := impactlab.DecodeDeltaManifestFile(options.deltaManifest)
		if manifestErr != nil {
			return &CLIError{ExitCode: 1, Message: "reconc impact: delta manifest: " + manifestErr.Error()}
		}
		report, manifestErr = impactlab.ApplyDeltaManifest(report, manifest, time.Now().UTC())
		if manifestErr != nil {
			return &CLIError{ExitCode: 1, Message: "reconc impact: delta manifest: " + manifestErr.Error()}
		}
	}
	format, err := resolveImpactReportFormat(options.format, options.jsonOutput)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc impact: " + err.Error()}
	}
	var output []byte
	switch format {
	case impactText:
		output = impactlab.RenderText(report)
	case impactJSON:
		output, err = impactlab.MarshalReport(report)
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc impact: render JSON: " + err.Error()}
		}
	case impactSARIF, impactJUnit, impactGitHub:
		ciFormat := cireport.FormatSARIF
		if format == impactJUnit {
			ciFormat = cireport.FormatJUnit
		} else if format == impactGitHub {
			ciFormat = cireport.FormatGitHub
		}
		output, err = cireport.Render(ciFormat, cireport.FromImpact(version, report))
		if err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc impact: render " + string(format) + ": " + err.Error()}
		}
	}
	if err := writeImpactBytes("impact", options.outputPath, stdout, output); err != nil {
		return err
	}
	if !report.DeltaGate.Passed {
		return &CLIError{ExitCode: 2, Message: ""}
	}
	return nil
}

type compiledImpactCandidate struct {
	repoRoot         string
	baseSourceDigest string
	metadata         impactlab.Candidate
}

func compileImpactCandidate(options impactCompareOptions, version string) (compiledImpactCandidate, []byte, error) {
	source := compiler.CandidateSource{Kind: policy.SourcePolicyFile, Name: "candidate-file"}
	kind, name := "policy_file", "candidate-file"
	if options.candidateFile != "" {
		content, err := readImpactCandidate(options.candidateFile)
		if err != nil {
			return compiledImpactCandidate{}, nil, err
		}
		source.Content = string(content)
	} else {
		content, err := presets.Load(options.pack)
		if err != nil {
			return compiledImpactCandidate{}, nil, err
		}
		source = compiler.CandidateSource{Kind: policy.SourcePreset, Name: options.pack, Content: content}
		kind, name = "preset", options.pack
	}
	compiled, body, baseSourceDigest, err := compiler.RenderRepoPolicyWithCandidate(options.repo, version, source)
	if err != nil {
		return compiledImpactCandidate{}, nil, err
	}
	return compiledImpactCandidate{
		repoRoot: compiled.RepoRoot, baseSourceDigest: baseSourceDigest,
		metadata: impactlab.Candidate{
			Kind: kind, Name: name, SourceDigest: compiled.SourceDigest, RuleCount: compiled.RuleCount,
		},
	}, body, nil
}

func readImpactCandidate(filePath string) ([]byte, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("candidate must be a regular file and not a symlink")
	}
	body, err := boundedio.ReadRegularFile(filePath, maxImpactCandidateBytes)
	if err != nil {
		return nil, err
	}
	if len(body) == 0 || !utf8.Valid(body) {
		return nil, fmt.Errorf("candidate must contain valid UTF-8 policy YAML")
	}
	return body, nil
}

func loadImpactCorpora(options impactCompareOptions) (impactlab.Corpus, error) {
	corpora := make([]impactlab.Corpus, 0, len(options.corpusPaths)+1)
	for _, filePath := range options.corpusPaths {
		corpus, err := impactlab.DecodeCorpusFile(filePath)
		if err != nil {
			return impactlab.Corpus{}, fmt.Errorf("load corpus %s: %w", filePath, err)
		}
		corpora = append(corpora, corpus)
	}
	if options.hasEvidence {
		corpus, err := impactlab.NewCorpus(options.repo, []impactlab.Case{
			impactlab.NewRepositoryCase(options.caseID, options.inputs),
		}, impactlab.AllEventClasses())
		if err != nil {
			return impactlab.Corpus{}, err
		}
		corpora = append(corpora, corpus)
	}
	return impactlab.MergeCorpora(corpora)
}

func runImpactExport(args []string, stdout io.Writer) error {
	options, err := parseImpactExportOptions(args)
	if err != nil {
		return err
	}
	if options.session {
		sessionInputs, sessionErr := activeImpactInputs(options.repo)
		if sessionErr != nil {
			return &CLIError{ExitCode: 1, Message: "reconc impact export: active session: " + sessionErr.Error()}
		}
		options.inputs = options.inputs.MergedWith(sessionInputs)
	}
	corpus, err := impactlab.NewCorpus(options.repo, []impactlab.Case{
		impactlab.NewRepositoryCase(options.caseID, options.inputs),
	}, options.complete)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc impact export: " + err.Error()}
	}
	body, err := impactlab.MarshalCorpus(corpus)
	if err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc impact export: " + err.Error()}
	}
	return writeImpactBytes("impact export", options.outputPath, stdout, body)
}

func activeImpactInputs(repo string) (runtime.ExecutionInputs, error) {
	active, err := agentsession.ActiveEvidence(repo)
	if err != nil {
		return runtime.Empty(), err
	}
	inputs := runtime.ExecutionInputs{
		ReadPaths: append([]string{}, active.ReadPaths...), WritePaths: append([]string{}, active.WritePaths...),
		WriteEpochs: make(map[string]uint64, len(active.WriteEpochs)),
		Commands:    append([]string{}, active.Commands...), Claims: append([]string{}, active.Claims...),
		CommandResults: make([]runtime.CommandResult, 0, len(active.CommandResults)),
	}
	for path, epoch := range active.WriteEpochs {
		inputs.WriteEpochs[path] = epoch
	}
	for _, result := range active.CommandResults {
		inputs.CommandResults = append(inputs.CommandResults, runtime.CommandResult{
			Command: result.Command, Outcome: result.Outcome, EvidenceEpoch: result.EvidenceEpoch,
		})
	}
	return inputs, nil
}

func writeImpactBytes(command, outputPath string, stdout io.Writer, body []byte) error {
	if outputPath != "" {
		if _, err := atomicfile.WriteIfChanged(outputPath, body, 0o644); err != nil {
			return &CLIError{ExitCode: 1, Message: "reconc " + command + ": write output: " + err.Error()}
		}
	}
	if _, err := stdout.Write(body); err != nil {
		return &CLIError{ExitCode: 1, Message: "reconc " + command + ": write stdout: " + err.Error()}
	}
	return nil
}
