package actionstate

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	MaxExecutableBytes         = 256 << 20
	MaxServerArgvValues        = 4096
	MaxServerArgvBytes         = 1 << 20
	MaxEnvironmentBindings     = 4096
	MaxEnvironmentBindingBytes = 8 << 20
	MaxEnvironmentNameBytes    = 256
	MaxCredentialIdentityBytes = 1 << 20
)

type IdentityDomain string

const (
	DomainServer           IdentityDomain = "reconc/action/server/v1"
	DomainRepository       IdentityDomain = "reconc/action/repository/v1"
	DomainWorkingDirectory IdentityDomain = "reconc/action/working-directory/v1"
	DomainArgv             IdentityDomain = "reconc/action/argv/v1"
	DomainEnvironmentName  IdentityDomain = "reconc/action/environment-names/v1"
	DomainEnvironment      IdentityDomain = "reconc/action/environment-value/v1"
	DomainArgument         IdentityDomain = "reconc/action/argument/v1"
	DomainResult           IdentityDomain = "reconc/action/result/v1"
	DomainUpstream         IdentityDomain = "reconc/action/upstream-request/v1"
	DomainApproval         IdentityDomain = "reconc/action/approval/v1"
	DomainBudget           IdentityDomain = "reconc/action/budget/v1"
	DomainLedger           IdentityDomain = "reconc/action/ledger/v1"
	DomainRun              IdentityDomain = "reconc/action/run/v1"
	DomainSession          IdentityDomain = "reconc/action/session/v1"
	DomainContext          IdentityDomain = "reconc/action/context/v1"
	DomainState            IdentityDomain = "reconc/action/state/v1"
	DomainTransaction      IdentityDomain = "reconc/action/state-transaction/v1"
	DomainCredential       IdentityDomain = "reconc/action/credential/v1"
)

type IdentityKey struct {
	id       string
	material [32]byte
}

func newIdentityKey(material []byte) (*IdentityKey, error) {
	if len(material) != 32 {
		return nil, fmt.Errorf("identity key must contain exactly 32 bytes")
	}
	key := &IdentityKey{}
	copy(key.material[:], material)
	digest := sha256.Sum256(key.material[:])
	key.id = hex.EncodeToString(digest[:16])
	return key, nil
}

func generateIdentityKey(reader io.Reader) (*IdentityKey, error) {
	if reader == nil {
		return nil, fmt.Errorf("identity key entropy source is unavailable")
	}
	material := make([]byte, 32)
	read, err := io.ReadFull(reader, material)
	if err != nil || read != len(material) {
		return nil, fmt.Errorf("read identity key entropy: %w", err)
	}
	key, err := newIdentityKey(material)
	for index := range material {
		material[index] = 0
	}
	return key, err
}

func (k *IdentityKey) ID() string {
	if k == nil {
		return ""
	}
	return k.id
}

func (k *IdentityKey) Identity(domain IdentityDomain, parts ...[]byte) string {
	if k == nil || !domain.Valid() {
		return ""
	}
	mac := hmac.New(sha256.New, k.material[:])
	if err := writeFramed(mac, []byte(domain)); err != nil {
		return ""
	}
	for _, part := range parts {
		if err := writeFramed(mac, part); err != nil {
			return ""
		}
	}
	return "hmac-sha256:v1:" + k.id + ":" + hex.EncodeToString(mac.Sum(nil))
}

func (d IdentityDomain) Valid() bool {
	switch d {
	case DomainServer, DomainRepository, DomainWorkingDirectory, DomainArgv, DomainEnvironmentName,
		DomainEnvironment, DomainArgument, DomainResult, DomainUpstream,
		DomainApproval, DomainBudget, DomainLedger, DomainRun, DomainSession,
		DomainContext, DomainState, DomainTransaction, DomainCredential:
		return true
	default:
		return false
	}
}

func writeFramed(writer io.Writer, value []byte) error {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	written, err := writer.Write(size[:])
	if err != nil || written != len(size) {
		return fmt.Errorf("write identity frame size: %w", err)
	}
	written, err = writer.Write(value)
	if err != nil || written != len(value) {
		return fmt.Errorf("write identity frame value: %w", err)
	}
	return nil
}

type EnvironmentBinding struct {
	Name  string
	Value string
}

type ObservedEnvironment struct {
	Name     string `json:"name"`
	Identity string `json:"identity"`
}

type ObservedServer struct {
	ExecutablePath      string                `json:"-"`
	ExecutableDigest    string                `json:"executable_digest"`
	ArgvIdentity        string                `json:"argv_identity"`
	WorkingDirectory    string                `json:"-"`
	WorkingDirIdentity  string                `json:"working_directory_identity"`
	EnvironmentNames    []string              `json:"environment_names"`
	Environment         []ObservedEnvironment `json:"environment"`
	EnvironmentIdentity string                `json:"environment_identity"`
	ServerIdentity      string                `json:"server_identity"`
}

func ObserveRepository(key *IdentityKey, root string) (string, string, error) {
	if key == nil {
		return "", "", fmt.Errorf("identity key is unavailable")
	}
	resolved, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return "", "", fmt.Errorf("resolve repository identity: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("repository root is not a directory")
		}
		return "", "", fmt.Errorf("inspect repository identity: %w", err)
	}
	objectIdentity, err := filesystemObjectIdentity(resolved)
	if err != nil {
		return "", "", fmt.Errorf("observe repository filesystem object: %w", err)
	}
	return resolved, key.Identity(
		DomainRepository, []byte(filepath.Clean(resolved)), []byte(objectIdentity),
	), nil
}

func ObserveServer(
	key *IdentityKey,
	command string,
	argv []string,
	workingDirectory string,
	environment []EnvironmentBinding,
) (ObservedServer, error) {
	if key == nil {
		return ObservedServer{}, fmt.Errorf("identity key is unavailable")
	}
	executable, err := resolveExecutable(command)
	if err != nil {
		return ObservedServer{}, err
	}
	executableDigest, err := observeExecutable(executable)
	if err != nil {
		return ObservedServer{}, err
	}
	argvIdentity, err := observeArgv(key, command, argv)
	if err != nil {
		return ObservedServer{}, err
	}
	resolvedCWD, err := pathidentity.ResolveExisting(workingDirectory)
	if err != nil {
		return ObservedServer{}, fmt.Errorf("resolve server working directory: %w", err)
	}
	cwdInfo, err := os.Stat(resolvedCWD)
	if err != nil || !cwdInfo.IsDir() {
		if err == nil {
			err = fmt.Errorf("server working directory is not a directory")
		}
		return ObservedServer{}, err
	}
	workingObjectIdentity, err := filesystemObjectIdentity(resolvedCWD)
	if err != nil {
		return ObservedServer{}, fmt.Errorf("observe working-directory filesystem object: %w", err)
	}
	workingIdentity := key.Identity(
		DomainWorkingDirectory, []byte(filepath.Clean(resolvedCWD)), []byte(workingObjectIdentity),
	)
	observedEnvironment, environmentIdentity, err := observeEnvironment(key, environment)
	if err != nil {
		return ObservedServer{}, err
	}
	names := make([]string, len(observedEnvironment))
	for index, entry := range observedEnvironment {
		names[index] = entry.Name
	}
	observed := ObservedServer{
		ExecutablePath: executable, ExecutableDigest: executableDigest,
		ArgvIdentity: argvIdentity, WorkingDirectory: resolvedCWD,
		WorkingDirIdentity: workingIdentity, EnvironmentNames: names,
		Environment: observedEnvironment, EnvironmentIdentity: environmentIdentity,
	}
	observed.ServerIdentity = observedServerIdentity(key, observed)
	return observed, nil
}

// Validate proves that every persisted-safe component of a fresh downstream
// observation is canonical, uses one active key generation, and recomputes the
// exact server identity. Raw paths and environment values are never required
// for persistence.
func (s ObservedServer) Validate(key *IdentityKey) error {
	if key == nil || !action.ValidSHA256Identity(s.ExecutableDigest) ||
		!identityUsesKey(s.ArgvIdentity, key.ID()) ||
		!identityUsesKey(s.WorkingDirIdentity, key.ID()) ||
		!identityUsesKey(s.EnvironmentIdentity, key.ID()) ||
		!identityUsesKey(s.ServerIdentity, key.ID()) || s.EnvironmentNames == nil || s.Environment == nil ||
		len(s.EnvironmentNames) != len(s.Environment) || len(s.Environment) > MaxEnvironmentBindings {
		return fmt.Errorf("downstream server identity components are unavailable")
	}
	nameParts := make([][]byte, len(s.Environment))
	for index, entry := range s.Environment {
		if entry.Name != canonicalEnvironmentName(entry.Name) || !validEnvironmentName(entry.Name) ||
			entry.Name != s.EnvironmentNames[index] || !identityUsesKey(entry.Identity, key.ID()) ||
			index > 0 && !environmentNameLess(s.Environment[index-1].Name, entry.Name) {
			return fmt.Errorf("downstream environment observation is invalid")
		}
		nameParts[index] = []byte(entry.Name)
	}
	wantEnvironment := key.Identity(DomainEnvironmentName, nameParts...)
	if !constantIdentityEqual(s.EnvironmentIdentity, wantEnvironment) ||
		!constantIdentityEqual(s.ServerIdentity, observedServerIdentity(key, s)) {
		return fmt.Errorf("downstream server observation identity does not match its exact components")
	}
	return nil
}

func observedServerIdentity(key *IdentityKey, observed ObservedServer) string {
	parts := [][]byte{
		[]byte(observed.ExecutableDigest), []byte(observed.ArgvIdentity),
		[]byte(observed.WorkingDirIdentity), []byte(observed.EnvironmentIdentity), []byte(key.ID()),
	}
	for _, entry := range observed.Environment {
		parts = append(parts, []byte(entry.Name), []byte(entry.Identity))
	}
	return key.Identity(DomainServer, parts...)
}

func observeExecutable(path string) (string, error) {
	hash := sha256.New()
	err := boundedio.WithRegularFileSnapshot(path, MaxExecutableBytes, func(file *os.File, info os.FileInfo) error {
		if !executableModeAllowed(info.Mode()) {
			return fmt.Errorf("resolved command is not executable")
		}
		written, err := io.Copy(hash, file)
		if err != nil {
			return err
		}
		if written != info.Size() {
			return fmt.Errorf("resolved executable changed while hashing")
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash resolved executable: %w", err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func resolveExecutable(command string) (string, error) {
	if command == "" || strings.IndexByte(command, 0) >= 0 {
		return "", fmt.Errorf("downstream command is empty or contains NUL")
	}
	path := command
	if !filepath.IsAbs(path) {
		resolved, err := exec.LookPath(command)
		if err != nil {
			return "", fmt.Errorf("resolve downstream executable: %w", err)
		}
		path = resolved
	}
	resolved, err := pathidentity.ResolveExisting(path)
	if err != nil {
		return "", fmt.Errorf("resolve downstream executable identity: %w", err)
	}
	return resolved, nil
}

func observeArgv(key *IdentityKey, command string, argv []string) (string, error) {
	if len(argv) >= MaxServerArgvValues {
		return "", fmt.Errorf("downstream argv exceeds %d values", MaxServerArgvValues)
	}
	parts := make([][]byte, 0, len(argv)+1)
	total := 0
	values := make([]string, 0, len(argv)+1)
	values = append(values, command)
	values = append(values, argv...)
	for _, value := range values {
		if strings.IndexByte(value, 0) >= 0 {
			return "", fmt.Errorf("downstream argv contains NUL")
		}
		if len(value) > MaxServerArgvBytes-total {
			return "", fmt.Errorf("downstream argv exceeds %d bytes", MaxServerArgvBytes)
		}
		total += len(value)
		parts = append(parts, []byte(value))
	}
	return key.Identity(DomainArgv, parts...), nil
}

func observeEnvironment(key *IdentityKey, input []EnvironmentBinding) ([]ObservedEnvironment, string, error) {
	if len(input) > MaxEnvironmentBindings {
		return nil, "", fmt.Errorf("inherited environment exceeds %d entries", MaxEnvironmentBindings)
	}
	entries := append([]EnvironmentBinding(nil), input...)
	for index := range entries {
		entries[index].Name = canonicalEnvironmentName(entries[index].Name)
	}
	sort.Slice(entries, func(i, j int) bool {
		if runtime.GOOS == "windows" {
			return strings.ToUpper(entries[i].Name) < strings.ToUpper(entries[j].Name)
		}
		return entries[i].Name < entries[j].Name
	})
	observed := make([]ObservedEnvironment, len(entries))
	nameParts := make([][]byte, len(entries))
	total := 0
	for index, entry := range entries {
		if !validEnvironmentName(entry.Name) || strings.IndexByte(entry.Value, 0) >= 0 {
			return nil, "", fmt.Errorf("inherited environment entry is invalid")
		}
		entryBytes := len(entry.Name) + len(entry.Value)
		if entryBytes > MaxEnvironmentBindingBytes-total {
			return nil, "", fmt.Errorf("inherited environment exceeds %d bytes", MaxEnvironmentBindingBytes)
		}
		total += entryBytes
		if index > 0 && environmentNamesEqual(entries[index-1].Name, entry.Name) {
			return nil, "", fmt.Errorf("inherited environment name %q is duplicated", entry.Name)
		}
		nameParts[index] = []byte(entry.Name)
		observed[index] = ObservedEnvironment{
			Name:     entry.Name,
			Identity: key.Identity(DomainEnvironment, []byte(entry.Name), []byte(entry.Value)),
		}
	}
	return observed, key.Identity(DomainEnvironmentName, nameParts...), nil
}

func validEnvironmentName(name string) bool {
	if name == "" || len(name) > MaxEnvironmentNameBytes || !utf8.ValidString(name) {
		return false
	}
	for _, character := range name {
		if character < 0x21 || character > 0x7e || character == '=' {
			return false
		}
	}
	return true
}

func canonicalEnvironmentName(name string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(name)
	}
	return name
}

func environmentNamesEqual(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func environmentNameLess(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(left) < strings.ToUpper(right)
	}
	return left < right
}

func NewRandomCallID() (string, error) {
	return randomID("act_", rand.Reader)
}

func randomID(prefix string, reader io.Reader) (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz234567"
	raw := make([]byte, 26)
	read, err := io.ReadFull(reader, raw)
	if err != nil || read != len(raw) {
		return "", fmt.Errorf("read random identity: %w", err)
	}
	for index := range raw {
		raw[index] = alphabet[int(raw[index])%len(alphabet)]
	}
	return prefix + string(raw), nil
}

func CredentialIdentity(key *IdentityKey, label string, value []byte) (string, error) {
	if key == nil || !action.SafeLabel(label) || len(value) == 0 || len(value) > MaxCredentialIdentityBytes {
		return "", fmt.Errorf("credential identity input is invalid")
	}
	return key.Identity(DomainCredential, []byte(label), value), nil
}
