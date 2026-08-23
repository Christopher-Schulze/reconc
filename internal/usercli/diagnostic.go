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
	paths, err := resolveReceiptPaths()
	if err != nil {
		return nil, err
	}
	var report *GlobalDiagnostic
	err = withReceiptReadLock(paths, func() error {
		var diagnoseErr error
		report, diagnoseErr = diagnoseGlobalUnlocked(version, paths)
		return diagnoseErr
	})
	return report, err
}

func diagnoseGlobalUnlocked(version string, paths receiptPaths) (*GlobalDiagnostic, error) {
	status, err := InspectCurrent("")
	if err != nil {
		return nil, err
	}
	report := &GlobalDiagnostic{
		Schema: schema.Resolve(schema.GlobalDiagnostic), FormatVersion: GlobalDiagnosticFormatVersion,
		Operation: "doctor.global", Status: DiagnosticHealthy, CurrentVersion: strings.TrimSpace(version),
		RunningPath: status.SourcePath, InstallTargetPath: status.TargetPath,
		PathShadows: []string{}, Checks: []DiagnosticCheck{}, Actions: []DiagnosticAction{},
	}
	report.ReceiptPath = stringPointer(paths.receipt)
	report.Checks = append(report.Checks, DiagnosticCheck{
		Name: "running-binary", Status: "pass",
		Detail: fmt.Sprintf("%s (%s)", status.SourcePath, status.ExpectedSHA256),
	})
	report.Checks = append(report.Checks, status.Diagnostics...)
	for _, diagnostic := range status.Diagnostics {
		if diagnostic.Status == "fail" {
			report.promoteStatus(DiagnosticInvalid)
		}
	}

	candidates, _, err := pathCandidatesDetailed()
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
			report.promoteStatus(DiagnosticInvalid)
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
		report.promoteStatus(DiagnosticUnowned)
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "installation-receipt", Status: "warn", Detail: "no installation receipt",
		})
		classifyLegacyOwnership(report, status, provenanceErr)
	default:
		report.promoteStatus(DiagnosticInvalid)
		report.Checks = append(report.Checks, DiagnosticCheck{
			Name: "installation-receipt", Status: "fail", Detail: loadErr.Error(),
		})
	}

	if report.ResolvedPath == nil {
		report.promoteStatus(DiagnosticShadowed)
	} else if report.ReceiptValid && report.Owner != nil &&
		!samePath(*report.ResolvedPath, *report.BinaryPath) {
		report.promoteStatus(DiagnosticShadowed)
	}
	if len(candidates) > 1 && !report.ReceiptValid {
		report.promoteStatus(DiagnosticAmbiguous)
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
		report.promoteStatus(DiagnosticStale)
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
		report.promoteStatus(DiagnosticStale)
		check.Status = "fail"
		check.Detail += "; receipt checksum differs"
	}
	if receipt.Version != report.CurrentVersion {
		report.promoteStatus(DiagnosticStale)
		check.Status = "fail"
		check.Detail += fmt.Sprintf("; receipt version %s differs from running %s", receipt.Version, report.CurrentVersion)
	}
	if report.ResolvedPath == nil || !samePath(receipt.BinaryPath, status.ResolvedPath) {
		report.promoteStatus(DiagnosticShadowed)
		check.Status = "fail"
		check.Detail += "; PATH resolves a different binary"
	}
	report.Checks = append(report.Checks, check)
}

func (report *GlobalDiagnostic) promoteStatus(candidate DiagnosticStatus) {
	if diagnosticSeverity(candidate) > diagnosticSeverity(report.Status) {
		report.Status = candidate
	}
}

func diagnosticSeverity(status DiagnosticStatus) int {
	switch status {
	case DiagnosticHealthy:
		return 0
	case DiagnosticUnowned:
		return 1
	case DiagnosticAmbiguous:
		return 2
	case DiagnosticShadowed:
		return 3
	case DiagnosticStale:
		return 4
	case DiagnosticInvalid:
		return 5
	default:
		return 6
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
	candidates, _, err := pathCandidatesDetailed()
	return candidates, err
}

func pathCandidatesDetailed() ([]string, []DiagnosticCheck, error) {
	entries := filepath.SplitList(os.Getenv("PATH"))
	if len(entries) > maxPATHEntries {
		return nil, nil, fmt.Errorf("PATH contains more than %d entries", maxPATHEntries)
	}
	names := executableCandidateNames()
	seen := map[string]bool{}
	candidates := []string{}
	diagnostics := []DiagnosticCheck{}
	for _, directory := range entries {
		if strings.TrimSpace(directory) == "" {
			continue
		}
		// Directory-major, name-minor: that is the order a shell resolves a
		// bare command, so the first candidate is the one that would run.
		for _, name := range names {
			candidate := filepath.Join(directory, name)
			info, err := os.Lstat(candidate)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				diagnostics = append(diagnostics, pathCandidateDiagnostic(candidate, "inspect", err))
				continue
			}
			if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				continue
			}
			resolved, err := pathidentity.ResolveExisting(candidate)
			if err != nil {
				diagnostics = append(diagnostics, pathCandidateDiagnostic(candidate, "resolve", err))
				continue
			}
			info, err = os.Stat(resolved)
			if err != nil {
				diagnostics = append(diagnostics, pathCandidateDiagnostic(candidate, "inspect resolved", err))
				continue
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
	}
	return candidates, diagnostics, nil
}

func pathCandidateDiagnostic(path, operation string, err error) DiagnosticCheck {
	return DiagnosticCheck{
		Name: "path-candidate", Status: "warn",
		Detail: fmt.Sprintf("%s PATH candidate %s: %v", operation, path, err),
	}
}

// defaultWindowsPATHEXT is the extension set cmd.exe assumes when PATHEXT is
// unset. Keeping the executable ones is enough: a bare `reconc` never resolves
// to a script type the shell will not run directly.
const defaultWindowsPATHEXT = ".COM;.EXE;.BAT;.CMD"

// executableCandidateNames returns every filename a shell would try for a bare
// `reconc`, in resolution order.
//
// On Windows that is not one name. cmd.exe and PowerShell walk PATHEXT, so a
// `reconc.bat` placed earlier in PATH shadows the installed `reconc.exe` and
// runs instead of it. Scanning only the .exe reported such an installation as
// unshadowed, which is the one answer `reconc doctor --global` must never give
// wrongly.
func executableCandidateNames() []string {
	if runtime.GOOS != "windows" {
		return []string{"reconc"}
	}
	raw := strings.TrimSpace(os.Getenv("PATHEXT"))
	if raw == "" {
		raw = defaultWindowsPATHEXT
	}
	names := make([]string, 0, 8)
	seen := map[string]bool{}
	for _, extension := range strings.Split(raw, ";") {
		extension = strings.TrimSpace(extension)
		if extension == "" || !strings.HasPrefix(extension, ".") {
			continue
		}
		name := strings.ToLower("reconc" + extension)
		if seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return []string{executableName()}
	}
	return names
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
