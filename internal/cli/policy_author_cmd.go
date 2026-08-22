package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/term"

	"reconc.dev/reconc/internal/adopt"
	"reconc.dev/reconc/internal/impactlab"
	"reconc.dev/reconc/internal/ingest"
	"reconc.dev/reconc/internal/policyauthor"
	"reconc.dev/reconc/internal/runtime"
)

type policyAuthorReport struct {
	FormatVersion string                `json:"format_version"`
	Preview       policyauthor.Preview  `json:"preview"`
	Detection     *adopt.Report         `json:"detection,omitempty"`
	Impact        *impactlab.Report     `json:"impact,omitempty"`
	Adoption      policyauthor.Adoption `json:"adoption"`
}

func runPolicy(args []string, version string, input io.Reader, terminal bool, stdout io.Writer) error {
	if len(args) == 0 {
		return policyAuthorError("expected subcommand author")
	}
	if isHelpFlag(args[0]) {
		fmt.Fprintln(stdout, "Usage: reconc policy author [repo] (--candidate FILE | --detected) [authoring flags]")
		fmt.Fprintln(stdout, "Validate, explain, and explicitly adopt a repository policy fragment.")
		fmt.Fprintln(stdout, "\nSubcommands:\n  author           validate, explain, and explicitly adopt a policy")
		return nil
	}
	if args[0] != "author" {
		return policyAuthorError(fmt.Sprintf("unknown subcommand %q", args[0]))
	}
	return runPolicyAuthor(args[1:], version, input, terminal, stdout)
}

func runPolicyAuthor(args []string, version string, input io.Reader, terminal bool, stdout io.Writer) error {
	options, err := parsePolicyAuthorOptions(args)
	if err != nil {
		return err
	}
	request, detection, err := preparePolicyAuthorRequest(options, version)
	if err != nil {
		return policyAuthorError(err.Error())
	}
	preview, err := policyauthor.Prepare(request)
	if err != nil {
		return policyAuthorError(err.Error())
	}
	report := policyAuthorReport{
		FormatVersion: policyauthor.ReportFormatVersion, Preview: preview, Detection: detection,
		Adoption: policyauthor.Adoption{Target: preview.Target},
	}
	if len(options.corpusPaths) > 0 || options.hasEvidence {
		impact, impactErr := policyAuthorImpact(options, preview)
		if impactErr != nil {
			return policyAuthorError("impact: " + impactErr.Error())
		}
		report.Impact = &impact
	}

	apply := options.apply
	if !options.jsonOutput && !apply && terminal {
		report.Adoption = policyauthor.Adoption{Requested: true, Target: preview.Target}
		if err := renderPolicyAuthorText(stdout, report); err != nil {
			return policyAuthorError(err.Error())
		}
		confirmed, promptErr := confirmPolicyAuthor(input, stdout, preview.Target)
		if promptErr != nil {
			return policyAuthorError("confirmation: " + promptErr.Error())
		}
		if !confirmed {
			report.Adoption = policyauthor.Adoption{Requested: true, Declined: true, Target: preview.Target}
			fmt.Fprintln(stdout, "Policy adoption declined; repository unchanged.")
			return nil
		}
		apply = true
	}
	if apply {
		adoption, applyErr := policyauthor.Apply(request, preview)
		report.Adoption = adoption
		if applyErr != nil {
			return policyAuthorError("apply: " + applyErr.Error())
		}
		if terminal && !options.apply && !options.jsonOutput {
			fmt.Fprintf(stdout, "Adopted atomically; verified lock %s.\n", adoption.LockSHA256)
		}
	}
	if options.jsonOutput {
		return renderPolicyAuthorJSON(stdout, report)
	}
	if !terminal || options.apply {
		if err := renderPolicyAuthorText(stdout, report); err != nil {
			return policyAuthorError(err.Error())
		}
	}
	return nil
}

func preparePolicyAuthorRequest(options policyAuthorOptions, version string) (policyauthor.Request, *adopt.Report, error) {
	discovery, err := ingest.DiscoverPolicyRepo(options.repo)
	if err != nil {
		return policyauthor.Request{}, nil, err
	}
	if !discovery.Discovered {
		return policyauthor.Request{}, nil, fmt.Errorf("no policy repository discovered")
	}
	request := policyauthor.Request{
		Repo: discovery.RepoRoot, Version: version, Target: options.target,
		CandidateKind: "file", CandidateName: filepath.Base(options.candidateFile),
	}
	if options.candidateFile != "" {
		body, err := readImpactCandidate(options.candidateFile)
		if err != nil {
			return policyauthor.Request{}, nil, fmt.Errorf("read candidate: %w", err)
		}
		request.Body = body
		return request, nil, nil
	}
	detection, err := adopt.Scan(discovery.RepoRoot)
	if err != nil {
		return policyauthor.Request{}, nil, fmt.Errorf("detect recommendations: %w", err)
	}
	body, err := detectedPolicyYAML(detection)
	if err != nil {
		return policyauthor.Request{}, nil, err
	}
	detection.RepoRoot = "."
	request.CandidateKind = "detected"
	request.CandidateName = "repository-recommendations"
	request.Body = body
	return request, &detection, nil
}

func detectedPolicyYAML(report adopt.Report) ([]byte, error) {
	if len(report.Suggestions) == 0 {
		return nil, fmt.Errorf("detector produced no policy-file rule recommendations; pack suggestions remain review-only")
	}
	return []byte("rules:\n" + adopt.RenderRulesYAML(report.Suggestions)), nil
}

func policyAuthorImpact(options policyAuthorOptions, preview policyauthor.Preview) (impactlab.Report, error) {
	candidateEvaluator, err := runtime.NewCompiledPolicyEvaluator(preview.LockfileBytes())
	if err != nil {
		return impactlab.Report{}, fmt.Errorf("prepare candidate evaluator: %w", err)
	}
	actionRuntime, err := candidateEvaluator.ActionRuntime()
	if err != nil {
		return impactlab.Report{}, fmt.Errorf("prepare candidate actions: %w", err)
	}
	repoRoot := preview.RepositoryRoot()
	corpus, err := loadImpactCorpora(impactCompareOptions{
		repo: repoRoot, corpusPaths: options.corpusPaths, caseID: options.caseID,
		inputs: options.inputs, hasEvidence: options.hasEvidence,
	})
	if err != nil {
		return impactlab.Report{}, err
	}
	current, sourceDigest, err := runtime.NewEvaluator().CurrentCompiledPolicyEvaluator(repoRoot)
	if err != nil {
		return impactlab.Report{}, fmt.Errorf("prepare current policy: %w", err)
	}
	if sourceDigest != preview.BaseSourceDigest {
		return impactlab.Report{}, fmt.Errorf("policy sources changed during comparison; retry")
	}
	return impactlab.Compare(repoRoot, corpus, impactlab.Candidate{
		Kind: "policy_file", Name: preview.CandidateName,
		SourceDigest: preview.CompiledSourceDigest, LockDigest: actionRuntime.LockDigest,
		ActionPlanIdentity: actionRuntime.Evaluator.PlanIdentity(),
		RuleCount:          len(preview.Explanation.Rules), ActionToolCount: actionRuntime.ToolCount,
		ActionRuleCount: actionRuntime.ActionRuleCount,
	}, current, candidateEvaluator)
}

func renderPolicyAuthorJSON(output io.Writer, report policyAuthorReport) error {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return policyAuthorError("render JSON: " + err.Error())
	}
	body = append(body, '\n')
	_, err = output.Write(body)
	return err
}

func renderPolicyAuthorText(output io.Writer, report policyAuthorReport) error {
	preview := report.Preview
	fmt.Fprintf(output, "Policy candidate %s: schema=valid compile=valid ready=%t\n", preview.CandidateName, preview.Validation.Ready)
	fmt.Fprintf(output, "Target: %s; rules=%d; sources=%d; conflicts=%d\n",
		preview.Target, len(preview.Explanation.Rules), len(preview.Explanation.Sources), len(preview.Explanation.Conflicts))
	if len(preview.Explanation.EffectivePacks) > 0 {
		fmt.Fprintf(output, "Effective packs: %s\n", strings.Join(preview.Explanation.EffectivePacks, ", "))
	}
	for _, count := range preview.Explanation.RuleKinds {
		fmt.Fprintf(output, "  %s: %d\n", count.Kind, count.Count)
	}
	for _, conflict := range preview.Explanation.Conflicts {
		fmt.Fprintf(output, "Conflict: %s\n", conflict.Description)
	}
	if report.Impact != nil {
		if _, err := output.Write(impactlab.RenderText(*report.Impact)); err != nil {
			return err
		}
	}
	switch {
	case report.Adoption.Applied:
		fmt.Fprintf(output, "Adopted atomically; verified lock %s.\n", report.Adoption.LockSHA256)
	case !report.Adoption.Requested:
		fmt.Fprintln(output, "Preview only; repository unchanged.")
	}
	return nil
}

func confirmPolicyAuthor(input io.Reader, output io.Writer, target string) (bool, error) {
	fmt.Fprintf(output, "Adopt this policy at %s? [y/N] ", target)
	line, err := bufio.NewReader(io.LimitReader(input, 33)).ReadString('\n')
	if err != nil && err != io.EOF {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes", nil
}

func inputIsTerminal(input *os.File) bool {
	if input == nil {
		return false
	}
	return term.IsTerminal(int(input.Fd()))
}
