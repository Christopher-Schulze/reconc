package usercli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"reconc.dev/reconc/buildprovenance"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/filelock"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/privatefs"
	"reconc.dev/reconc/internal/schema"
)

const (
	ReceiptFormatVersion     = "1"
	maxInstallationReceipt   = 64 << 10
	installationStateDirName = "install"
	receiptFileName          = "receipt.json"
	receiptLockFileName      = "receipt.lock"
	unavailableSourceDigest  = "unavailable"
)

type Manager string

const (
	ManagerDirect Manager = "direct"
	ManagerSource Manager = "source"
)

type Channel string

const (
	ChannelStable  Channel = "stable"
	ChannelPreview Channel = "preview"
	ChannelExact   Channel = "exact"
	ChannelSource  Channel = "source"
)

type ProvenanceState string

const (
	ProvenanceGitHubVerified   ProvenanceState = "github-verified"
	ProvenanceEmbeddedVerified ProvenanceState = "embedded-verified"
	ProvenanceSourceLocal      ProvenanceState = "source-local"
)

var (
	buildVersionPattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]*$`)
	targetNamePattern   = regexp.MustCompile(`^[a-z0-9]+$`)
	sha256Pattern       = regexp.MustCompile(`^[0-9a-f]{64}$`)
	artifactNamePattern = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]*$`)
)

type Receipt struct {
	Schema           string          `json:"$schema"`
	FormatVersion    string          `json:"format_version"`
	Manager          Manager         `json:"manager"`
	Channel          Channel         `json:"channel"`
	Version          string          `json:"version"`
	SourceRepository string          `json:"source_repository"`
	ReleaseTag       *string         `json:"release_tag"`
	ArtifactName     string          `json:"artifact_name"`
	ArtifactSHA256   string          `json:"artifact_sha256"`
	BinaryPath       string          `json:"binary_path"`
	GOOS             string          `json:"goos"`
	GOARCH           string          `json:"goarch"`
	SourceDigest     string          `json:"source_digest"`
	ProvenanceState  ProvenanceState `json:"provenance_state"`
	InstalledAt      string          `json:"installed_at"`
	ReceiptDigest    string          `json:"receipt_digest"`
}

type ReceiptInput struct {
	Manager          Manager
	Channel          Channel
	Version          string
	SourceRepository string
	ReleaseTag       *string
	ArtifactName     string
	ArtifactSHA256   string
	BinaryPath       string
	GOOS             string
	GOARCH           string
	SourceDigest     string
	ProvenanceState  ProvenanceState
	InstalledAt      time.Time
}

type receiptPaths struct {
	directory string
	receipt   string
	lock      string
}

func NewReceipt(input ReceiptInput) (*Receipt, error) {
	installedAt := input.InstalledAt.UTC()
	if installedAt.IsZero() {
		installedAt = time.Now().UTC()
	}
	receipt := &Receipt{
		Schema: schema.Resolve(schema.InstallationReceipt), FormatVersion: ReceiptFormatVersion,
		Manager: input.Manager, Channel: input.Channel, Version: strings.TrimSpace(input.Version),
		SourceRepository: strings.TrimSpace(input.SourceRepository), ReleaseTag: input.ReleaseTag,
		ArtifactName: strings.TrimSpace(input.ArtifactName), ArtifactSHA256: strings.TrimSpace(input.ArtifactSHA256),
		BinaryPath: filepath.Clean(input.BinaryPath), GOOS: strings.TrimSpace(input.GOOS),
		GOARCH: strings.TrimSpace(input.GOARCH), SourceDigest: strings.TrimSpace(input.SourceDigest),
		ProvenanceState: input.ProvenanceState, InstalledAt: installedAt.Format(time.RFC3339),
	}
	digest, err := computeReceiptDigest(receipt)
	if err != nil {
		return nil, err
	}
	receipt.ReceiptDigest = digest
	if err := validateReceipt(receipt); err != nil {
		return nil, err
	}
	return receipt, nil
}

func LoadReceipt() (*Receipt, string, error) {
	paths, err := resolveReceiptPaths()
	if err != nil {
		return nil, "", err
	}
	receipt, err := loadReceiptFile(paths.receipt)
	return receipt, paths.receipt, err
}

func WriteReceipt(receipt *Receipt) (bool, string, error) {
	paths, err := resolveReceiptPaths()
	if err != nil {
		return false, "", err
	}
	var changed bool
	err = withReceiptLock(paths, func() error {
		var writeErr error
		changed, writeErr = writeReceiptUnlocked(paths.receipt, receipt)
		return writeErr
	})
	return changed, paths.receipt, err
}

func RemoveReceiptIfOwned(receipt *Receipt) error {
	if receipt == nil {
		return errors.New("installation receipt is required")
	}
	paths, err := resolveReceiptPaths()
	if err != nil {
		return err
	}
	return withReceiptLock(paths, func() error {
		current, loadErr := loadReceiptFile(paths.receipt)
		if loadErr != nil {
			return loadErr
		}
		if current.ReceiptDigest != receipt.ReceiptDigest {
			return errors.New("installation receipt changed before removal")
		}
		if err := os.Remove(paths.receipt); err != nil {
			return fmt.Errorf("remove installation receipt: %w", err)
		}
		return nil
	})
}

func sourceReceiptInput(status *Status, version string, installedAt time.Time) (ReceiptInput, error) {
	if status == nil || !status.Ready {
		return ReceiptInput{}, errors.New("ready user CLI status is required for receipt publication")
	}
	binaryPath, err := pathidentity.ResolveExisting(status.TargetPath)
	if err != nil {
		return ReceiptInput{}, fmt.Errorf("resolve installed source binary identity: %w", err)
	}
	provenance, provenanceErr := buildprovenance.InspectBinary(status.TargetPath)
	sourceDigest := unavailableSourceDigest
	goos := runtime.GOOS
	goarch := runtime.GOARCH
	if provenanceErr == nil {
		if strings.TrimSpace(version) != "" && provenance.Version != strings.TrimSpace(version) {
			return ReceiptInput{}, fmt.Errorf("binary version %q does not match requested version %q", provenance.Version, version)
		}
		sourceDigest = provenance.SourceDigest
		goos = provenance.GOOS
		goarch = provenance.GOARCH
	}
	return ReceiptInput{
		Manager: ManagerSource, Channel: ChannelSource, Version: version,
		SourceRepository: "local-source", ArtifactName: filepath.Base(status.SourcePath),
		ArtifactSHA256: status.ExpectedSHA256, BinaryPath: binaryPath,
		GOOS: goos, GOARCH: goarch, SourceDigest: sourceDigest,
		ProvenanceState: ProvenanceSourceLocal, InstalledAt: installedAt,
	}, nil
}

func directReceiptInput(status *Status, version string, channel Channel, artifactName string, releaseTag string, provenanceState ProvenanceState, installedAt time.Time) (ReceiptInput, error) {
	if status == nil || !status.Ready {
		return ReceiptInput{}, errors.New("ready user CLI status is required for direct receipt publication")
	}
	provenance, err := buildprovenance.InspectBinary(status.TargetPath)
	if err != nil {
		return ReceiptInput{}, fmt.Errorf("inspect direct-install provenance: %w", err)
	}
	if provenance.Version != strings.TrimSpace(version) {
		return ReceiptInput{}, fmt.Errorf("direct binary version %q does not match requested version %q", provenance.Version, version)
	}
	if provenance.GOOS != runtime.GOOS || provenance.GOARCH != runtime.GOARCH {
		return ReceiptInput{}, fmt.Errorf("direct binary target %s/%s does not match host %s/%s", provenance.GOOS, provenance.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	binaryPath, err := pathidentity.ResolveExisting(status.TargetPath)
	if err != nil {
		return ReceiptInput{}, fmt.Errorf("resolve installed direct binary identity: %w", err)
	}
	tag := strings.TrimSpace(releaseTag)
	return ReceiptInput{
		Manager: ManagerDirect, Channel: channel, Version: version,
		SourceRepository: "Christopher-Schulze/reconc", ReleaseTag: &tag,
		ArtifactName: artifactName, ArtifactSHA256: status.ExpectedSHA256,
		BinaryPath: binaryPath, GOOS: provenance.GOOS, GOARCH: provenance.GOARCH,
		SourceDigest: provenance.SourceDigest, ProvenanceState: provenanceState,
		InstalledAt: installedAt,
	}, nil
}

func resolveReceiptPaths() (receiptPaths, error) {
	home := strings.TrimSpace(os.Getenv("RECONC_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return receiptPaths{}, fmt.Errorf("resolve user home for installation receipt: %w", err)
		}
		home = filepath.Join(userHome, ".reconc")
	}
	absolute, err := filepath.Abs(home)
	if err != nil {
		return receiptPaths{}, fmt.Errorf("resolve RECONC_HOME for installation receipt: %w", err)
	}
	requestedDirectory := filepath.Join(filepath.Clean(absolute), installationStateDirName)
	if info, statErr := os.Lstat(requestedDirectory); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return receiptPaths{}, fmt.Errorf("installation state directory cannot be a symlink: %s", requestedDirectory)
		}
	} else if !os.IsNotExist(statErr) {
		return receiptPaths{}, fmt.Errorf("inspect installation state directory: %w", statErr)
	}
	directory, err := pathidentity.ResolveProspective(requestedDirectory)
	if err != nil {
		return receiptPaths{}, fmt.Errorf("resolve installation state directory: %w", err)
	}
	return receiptPaths{
		directory: directory,
		receipt:   filepath.Join(directory, receiptFileName),
		lock:      filepath.Join(directory, receiptLockFileName),
	}, nil
}

func withReceiptLock(paths receiptPaths, operation func() error) (resultErr error) {
	if err := ensurePrivateDirectory(paths.directory); err != nil {
		return err
	}
	lockFile, err := privatefs.OpenLock(paths.lock)
	if err != nil {
		return fmt.Errorf("open installation receipt lock: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, lockFile.Close())
	}()
	unlock, err := filelock.LockContext(context.Background(), lockFile, filelock.DefaultTimeout)
	if err != nil {
		return fmt.Errorf("lock installation receipt: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, unlock())
	}()
	return operation()
}

func ensurePrivateDirectory(path string) error {
	return privatefs.SecureDirectory(path)
}

func loadReceiptFile(path string) (*Receipt, error) {
	body, err := boundedio.ReadRegularFile(path, maxInstallationReceipt)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, os.ErrNotExist
		}
		return nil, fmt.Errorf("read installation receipt: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var receipt Receipt
	if err := decoder.Decode(&receipt); err != nil {
		return nil, fmt.Errorf("decode installation receipt: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode installation receipt: %w", err)
	}
	if err := validateReceipt(&receipt); err != nil {
		return nil, fmt.Errorf("validate installation receipt: %w", err)
	}
	return &receipt, nil
}

func writeReceiptUnlocked(path string, receipt *Receipt) (bool, error) {
	if err := validateReceipt(receipt); err != nil {
		return false, err
	}
	body, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal installation receipt: %w", err)
	}
	body = append(body, '\n')
	if len(body) > maxInstallationReceipt {
		return false, fmt.Errorf("installation receipt exceeds %d bytes", maxInstallationReceipt)
	}
	changed, err := privatefs.WritePrivateIfChanged(path, body, 0o600)
	if err != nil {
		return false, fmt.Errorf("publish installation receipt: %w", err)
	}
	return changed, nil
}

func validateReceipt(receipt *Receipt) error {
	if receipt == nil {
		return errors.New("installation receipt is required")
	}
	if !schema.AcceptsFormat(schema.InstallationReceipt, receipt.Schema, receipt.FormatVersion) {
		return errors.New("unsupported installation receipt schema or format")
	}
	if !validManager(receipt.Manager) || !validChannel(receipt.Channel) {
		return errors.New("invalid installation manager or channel")
	}
	if receipt.Manager == ManagerSource && receipt.Channel != ChannelSource ||
		receipt.Manager != ManagerSource && receipt.Channel == ChannelSource {
		return errors.New("source manager and source channel must be used together")
	}
	if !buildVersionPattern.MatchString(receipt.Version) {
		return fmt.Errorf("invalid installation version %q", receipt.Version)
	}
	if receipt.SourceRepository != "Christopher-Schulze/reconc" && receipt.SourceRepository != "local-source" {
		return fmt.Errorf("invalid installation source repository %q", receipt.SourceRepository)
	}
	if receipt.Manager == ManagerSource && receipt.SourceRepository != "local-source" ||
		receipt.Manager != ManagerSource && receipt.SourceRepository != "Christopher-Schulze/reconc" {
		return errors.New("installation manager and source repository disagree")
	}
	if receipt.Manager == ManagerSource && receipt.ReleaseTag != nil {
		return errors.New("source installation cannot have a release tag")
	}
	if receipt.Manager != ManagerSource {
		if receipt.ReleaseTag == nil || *receipt.ReleaseTag != "reconc-v"+receipt.Version {
			return errors.New("release installation tag does not match version")
		}
	}
	if !artifactNamePattern.MatchString(receipt.ArtifactName) ||
		filepath.Base(receipt.ArtifactName) != receipt.ArtifactName {
		return fmt.Errorf("invalid installation artifact name %q", receipt.ArtifactName)
	}
	if !sha256Pattern.MatchString(receipt.ArtifactSHA256) {
		return errors.New("invalid installation artifact SHA-256")
	}
	if !filepath.IsAbs(receipt.BinaryPath) || filepath.Clean(receipt.BinaryPath) != receipt.BinaryPath {
		return errors.New("installation binary path must be a clean absolute path")
	}
	if !targetNamePattern.MatchString(receipt.GOOS) || !targetNamePattern.MatchString(receipt.GOARCH) {
		return errors.New("invalid installation build target")
	}
	if receipt.SourceDigest != unavailableSourceDigest && !sha256Pattern.MatchString(receipt.SourceDigest) {
		return errors.New("invalid installation source digest")
	}
	if receipt.SourceDigest == unavailableSourceDigest && receipt.ProvenanceState != ProvenanceSourceLocal {
		return errors.New("release installation requires embedded source provenance")
	}
	if !validProvenanceState(receipt.ProvenanceState) {
		return errors.New("invalid installation provenance state")
	}
	installedAt, err := time.Parse(time.RFC3339, receipt.InstalledAt)
	if err != nil || !strings.HasSuffix(receipt.InstalledAt, "Z") ||
		installedAt.UTC().Format(time.RFC3339) != receipt.InstalledAt {
		return errors.New("installation time must be canonical UTC RFC3339")
	}
	if !sha256Pattern.MatchString(receipt.ReceiptDigest) {
		return errors.New("invalid installation receipt digest")
	}
	expected, err := computeReceiptDigest(receipt)
	if err != nil {
		return err
	}
	if receipt.ReceiptDigest != expected {
		return errors.New("installation receipt digest mismatch")
	}
	return nil
}

func computeReceiptDigest(receipt *Receipt) (string, error) {
	if receipt == nil {
		return "", errors.New("installation receipt is required")
	}
	canonical := *receipt
	canonical.ReceiptDigest = ""
	body, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("marshal installation receipt digest: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return fmt.Errorf("unexpected trailing data: %w", err)
	}
	return nil
}

func validManager(manager Manager) bool {
	switch manager {
	case ManagerDirect, ManagerSource:
		return true
	default:
		return false
	}
}

func validChannel(channel Channel) bool {
	switch channel {
	case ChannelStable, ChannelPreview, ChannelExact, ChannelSource:
		return true
	default:
		return false
	}
}

func validProvenanceState(state ProvenanceState) bool {
	switch state {
	case ProvenanceGitHubVerified, ProvenanceEmbeddedVerified, ProvenanceSourceLocal:
		return true
	default:
		return false
	}
}
