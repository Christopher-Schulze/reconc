package usercli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"reconc.dev/reconc/buildprovenance"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/schema"
)

const (
	GlobalDiagnosticFormatVersion = "reconc.global-diagnostic/v1"
	maxPATHEntries                = 256
)

type DiagnosticStatus string

const (
	DiagnosticHealthy   DiagnosticStatus = "healthy"
	DiagnosticUnowned   DiagnosticStatus = "unowned"
	DiagnosticStale     DiagnosticStatus = "stale"
	DiagnosticShadowed  DiagnosticStatus = "shadowed"
	DiagnosticAmbiguous DiagnosticStatus = "ambiguous"
	DiagnosticInvalid   DiagnosticStatus = "invalid"
)

type DiagnosticCheck struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type DiagnosticAction struct {
	Kind    string `json:"kind"`
	Command string `json:"command"`
	Detail  string `json:"detail"`
}

type GlobalDiagnostic struct {
	Schema            string             `json:"$schema"`
	FormatVersion     string             `json:"format_version"`
	Operation         string             `json:"operation"`
	Status            DiagnosticStatus   `json:"status"`
	Changed           bool               `json:"changed"`
	Owner             *Manager           `json:"owner"`
	CurrentVersion    string             `json:"current_version"`
	TargetVersion     *string            `json:"target_version"`
	Channel           *Channel           `json:"channel"`
	BinaryPath        *string            `json:"binary_path"`
	ReceiptPath       *string            `json:"receipt_path"`
	PlanDigest        *string            `json:"plan_digest"`
	RunningPath       string             `json:"running_path"`
	ResolvedPath      *string            `json:"resolved_path"`
	InstallTargetPath string             `json:"install_target_path"`
	ReceiptValid      bool               `json:"receipt_valid"`
	ChecksumIdentity  bool               `json:"checksum_identity"`
	PathShadows       []string           `json:"path_shadows"`
	ProvenanceState   *ProvenanceState   `json:"provenance_state"`
	OwnershipEvidence string             `json:"ownership_evidence"`
	Checks            []DiagnosticCheck  `json:"checks"`
	Actions           []DiagnosticAction `json:"actions"`
	NextAction        string             `json:"next_action"`
}

func (report *GlobalDiagnostic) Blocking() bool {
	if report == nil {
		return true
	}
	switch report.Status {
	case DiagnosticHealthy, DiagnosticUnowned:
		return false
	default:
		return true
	}
}

func DiagnoseGlobal(version string) (*GlobalDiagnostic, error) {
	status, err := InspectCurrent("")
	if err != nil {
		return nil, err
	}
	paths, err := resolveReceiptPaths()
	if err != nil {
		return nil, err
	}
	report := &GlobalDiagnostic{
		Schema: schema.GlobalDiagnosticURL, FormatVersion: GlobalDiagnosticFormatVersion,
		Operation: "doctor.global", Status: DiagnosticUnowned, CurrentVersion: strings.TrimSpace(version),
		RunningPath: status.SourcePath, InstallTargetPath: status.TargetPath,
		PathShadows: []string{}, Checks: []DiagnosticCheck{}, Actions: []DiagnosticAction{},
	}
	report.ReceiptPath = stringPointer(paths.receipt)
	report.Checks = append(report.Checks, DiagnosticCheck{
		Name: "running-binary", Status: "pass",
		Detail: fmt.Sprintf("%s (%s)", status.SourcePath, status.ExpectedSHA256),
	})

	candidates, err := pathCandidates()
	if err != nil {
		return nil, err
	}
	if status.ResolvedPath != "" {
		report.ResolvedPath = stringPointer(status.ResolvedPath)
		report.BinaryPath = stringPointer(status.ResolvedPath)
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "path-resolution", Status: "pass", Detail: status.ResolvedPath,
		})
	} else {
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "path-resolution", Status: "fail", Detail: "bare `reconc` is not resolvable on PATH",
		})
	}
	for _, candidate := range candidates {
		if report.ResolvedPath == nil || !samePath(candidate, *report.ResolvedPath) {
			report.PathShadows = append(report.PathShadows, candidate)
		}
	}
	if len(report.PathShadows) == 0 {
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "path-shadows", Status: "pass", Detail: "no additional Reconc executable is visible on PATH",
		})
	} else {
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "path-shadows", Status: "warn",
			Detail: fmt.Sprintf("%d additional executable(s): %s", len(report.PathShadows), strings.Join(report.PathShadows, ", ")),
		})
	}

	provenance, provenanceErr := buildprovenance.InspectBinary(status.SourcePath)
	if provenanceErr == nil {
		state := ProvenanceEmbeddedVerified
		report.ProvenanceState = &state
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "build-provenance", Status: "pass",
			Detail: fmt.Sprintf("%s %s/%s source=%s", provenance.Version, provenance.GOOS, provenance.GOARCH, provenance.SourceDigest),
		})
		if provenance.Version != report.CurrentVersion {
			report.Status = DiagnosticInvalid
			report.Checks[len(report.Checks)-1].Status = "fail"
			report.Checks[len(report.Checks)-1].Detail += "; running version output disagrees"
		}
	} else {
		state := ProvenanceSourceLocal
		report.ProvenanceState = &state
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "build-provenance", Status: "warn", Detail: provenanceErr.Error(),
		})
	}

	receipt, loadErr := loadReceiptFile(paths.receipt)
	switch {
	case loadErr == nil:
		report.ReceiptValid = true
		report.Owner = managerPointer(receipt.Manager)
		report.Channel = channelPointer(receipt.Channel)
		report.BinaryPath = stringPointer(receipt.BinaryPath)
		report.ProvenanceState = provenanceStatePointer(receipt.ProvenanceState)
		report.OwnershipEvidence = "validated installation receipt " + paths.receipt
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "installation-receipt", Status: "pass",
			Detail: fmt.Sprintf("%s owner=%s digest=%s", paths.receipt, receipt.Manager, receipt.ReceiptDigest),
		})
		evaluateReceiptIdentity(report, receipt, status)
	case errors.Is(loadErr, os.ErrNotExist):
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "installation-receipt", Status: "warn", Detail: "no installation receipt",
		})
		classifyLegacyOwnership(report, status, provenanceErr)
	default:
		report.Status = DiagnosticInvalid
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "installation-receipt", Status: "fail", Detail: loadErr.Error(),
		})
	}

	if report.ResolvedPath == nil {
		report.Status = DiagnosticShadowed
	} else if report.ReceiptValid && report.Owner != nil &&
		!samePath(*report.ResolvedPath, *report.BinaryPath) {
		report.Status = DiagnosticShadowed
	}
	if len(candidates) > 1 && !report.ReceiptValid {
		report.Status = DiagnosticAmbiguous
	}
	report.NextAction, report.Actions = diagnosticRemediation(report)
	sort.SliceStable(report.Checks, func(left, right int) bool {
		return report.Checks[left].Name < report.Checks[right].Name
	})
	return report, nil
}

func evaluateReceiptIdentity(report *GlobalDiagnostic, receipt *Receipt, status *Status) {
	digest, err := fileSHA256(receipt.BinaryPath)
	if err != nil {
		report.Status = DiagnosticStale
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "receipt-binary", Status: "fail", Detail: err.Error(),
		})
		return
	}
	report.ChecksumIdentity = digest == receipt.ArtifactSHA256
	check := DiagnosticCheck{
		Name: "receipt-binary", Status: "pass",
		Detail: fmt.Sprintf("%s checksum=%s", receipt.BinaryPath, digest),
	}
	if !report.ChecksumIdentity {
		report.Status = DiagnosticStale
		check.Status = "fail"
		check.Detail += "; receipt checksum differs"
	}
	if receipt.Version != report.CurrentVersion {
		report.Status = DiagnosticStale
		check.Status = "fail"
		check.Detail += fmt.Sprintf("; receipt version %s differs from running %s", receipt.Version, report.CurrentVersion)
	}
	if report.ResolvedPath == nil || !samePath(receipt.BinaryPath, status.ResolvedPath) {
		report.Status = DiagnosticShadowed
		check.Status = "fail"
		check.Detail += "; PATH resolves a different binary"
	}
	report.Checks = append(report.Checks, check)
	if check.Status == "pass" && report.Status != DiagnosticInvalid {
		report.Status = DiagnosticHealthy
	}
}

func classifyLegacyOwnership(report *GlobalDiagnostic, status *Status, provenanceErr error) {
	if status.ResolvedPath == "" {
		return
	}
	if samePath(status.ResolvedPath, status.TargetPath) && provenanceErr == nil {
		report.Owner = managerPointer(ManagerDirect)
		exact := ChannelExact
		report.Channel = &exact
		report.BinaryPath = stringPointer(status.ResolvedPath)
		report.OwnershipEvidence = "legacy release binary at the canonical direct-install target"
		return
	}
	if samePath(status.ResolvedPath, status.SourcePath) {
		report.Owner = managerPointer(ManagerSource)
		source := ChannelSource
		report.Channel = &source
		report.BinaryPath = stringPointer(status.ResolvedPath)
		report.OwnershipEvidence = "unreceipted running source binary"
	}
}

func diagnosticRemediation(report *GlobalDiagnostic) (string, []DiagnosticAction) {
	switch report.Status {
	case DiagnosticHealthy:
		return "Global Reconc installation is healthy.", []DiagnosticAction{}
	case DiagnosticUnowned:
		command := quote(report.RunningPath) + " install-cli"
		return "Run `" + command + "` to publish verified source ownership.", []DiagnosticAction{{
			Kind: "record-ownership", Command: command, Detail: "No valid installation receipt exists.",
		}}
	case DiagnosticShadowed:
		if report.BinaryPath != nil {
			directory := filepath.Dir(*report.BinaryPath)
			return pathRemediation(directory), []DiagnosticAction{{
				Kind: "repair-path", Command: "", Detail: pathRemediation(directory),
			}}
		}
		return "Install Reconc on PATH, then rerun `reconc doctor --global`.", []DiagnosticAction{{
			Kind: "repair-path", Command: "", Detail: "Bare `reconc` is unavailable.",
		}}
	case DiagnosticStale, DiagnosticInvalid:
		command := quote(report.RunningPath) + " install-cli"
		return "Run `" + command + "` from the intended binary to repair owned state.", []DiagnosticAction{{
			Kind: "repair-installation", Command: command, Detail: "Receipt or binary identity is invalid.",
		}}
	default:
		return "Remove PATH ambiguity, then rerun `reconc doctor --global`.", []DiagnosticAction{{
			Kind: "resolve-ambiguity", Command: "", Detail: "Multiple ownership signals disagree.",
		}}
	}
}

func pathCandidates() ([]string, error) {
	entries := filepath.SplitList(os.Getenv("PATH"))
	if len(entries) > maxPATHEntries {
		return nil, fmt.Errorf("PATH contains more than %d entries", maxPATHEntries)
	}
	name := executableName()
	seen := map[string]bool{}
	candidates := []string{}
	for _, directory := range entries {
		if strings.TrimSpace(directory) == "" {
			continue
		}
		candidate := filepath.Join(directory, name)
		info, err := os.Lstat(candidate)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect PATH candidate %s: %w", candidate, err)
		}
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			continue
		}
		resolved, err := pathidentity.ResolveExisting(candidate)
		if err != nil {
			return nil, fmt.Errorf("resolve PATH candidate %s: %w", candidate, err)
		}
		info, err = os.Stat(resolved)
		if err != nil {
			return nil, fmt.Errorf("inspect resolved PATH candidate %s: %w", candidate, err)
		}
		if !info.Mode().IsRegular() ||
			(runtime.GOOS != "windows" && info.Mode()&0o111 == 0) {
			continue
		}
		key := pathKey(resolved)
		if seen[key] {
			continue
		}
		seen[key] = true
		candidates = append(candidates, resolved)
	}
	return candidates, nil
}

func samePath(left string, right string) bool {
	return canonicalPathKey(left) == canonicalPathKey(right)
}

func canonicalPathKey(path string) string {
	resolved, err := pathidentity.ResolveExisting(path)
	if err == nil {
		return pathKey(resolved)
	}
	return pathKey(filepath.Clean(path))
}

func pathKey(path string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}

func managerPointer(value Manager) *Manager {
	return &value
}

func channelPointer(value Channel) *Channel {
	return &value
}

func stringPointer(value string) *string {
	return &value
}

func provenanceStatePointer(value ProvenanceState) *ProvenanceState {
	return &value
}
