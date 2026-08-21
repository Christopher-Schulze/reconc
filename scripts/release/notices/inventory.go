package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
)

const (
	maxGoListBytes     = 32 << 20
	maxGoEnvBytes      = 1 << 20
	maxModuleEntries   = 4096
	maxComponents      = 4096
	maxLicenseFileSize = 1 << 20
	maxNoticeBytes     = 16 << 20
)

var errNoLicenseFiles = errors.New("no root license file found")

type noticeInventory struct {
	Targets    []string
	Components []noticeComponent
}

type noticeComponent struct {
	Identity string
	Files    []noticeFile
}

type noticeFile struct {
	Name   string
	Digest string
	Body   []byte
}

type listedPackage struct {
	Module *listedModule `json:"Module"`
}

type listedModule struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Dir     string        `json:"Dir"`
	Main    bool          `json:"Main"`
	Replace *listedModule `json:"Replace"`
}

type goEnvironment struct {
	Root    string `json:"GOROOT"`
	Version string `json:"GOVERSION"`
}

func discoverInventory(root, goBinary string, rawTargets []string) (noticeInventory, error) {
	targets, err := normalizeTargets(rawTargets)
	if err != nil {
		return noticeInventory{}, err
	}
	modules := map[string]listedModule{}
	for _, target := range targets {
		if err := discoverTargetModules(root, goBinary, target, modules); err != nil {
			return noticeInventory{}, err
		}
	}
	components, err := collectModuleNotices(modules)
	if err != nil {
		return noticeInventory{}, err
	}
	toolchain, err := collectToolchainNotice(root, goBinary)
	if err != nil {
		return noticeInventory{}, err
	}
	components = append(components, toolchain)
	if noticeBodyBytes(components) > maxNoticeBytes {
		return noticeInventory{}, fmt.Errorf("release license notices exceed %d bytes", maxNoticeBytes)
	}
	sort.Slice(components, func(i, j int) bool {
		return components[i].Identity < components[j].Identity
	})
	return noticeInventory{Targets: targets, Components: components}, nil
}

func normalizeTargets(rawTargets []string) ([]string, error) {
	seen := map[string]struct{}{}
	targets := make([]string, 0, len(rawTargets))
	for _, target := range rawTargets {
		parts := strings.Split(target, "/")
		if len(parts) != 2 || !safeTargetPart(parts[0]) || !safeTargetPart(parts[1]) {
			return nil, fmt.Errorf("invalid release target %q", target)
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil, errors.New("at least one release target is required")
	}
	sort.Strings(targets)
	return targets, nil
}

func safeTargetPart(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func discoverTargetModules(root, goBinary, target string, modules map[string]listedModule) error {
	parts := strings.Split(target, "/")
	command := exec.Command(goBinary, "list", "-mod=readonly", "-deps", "-json", "./cmd/reconc")
	command.Dir = root
	command.Env = targetEnvironment(os.Environ(), parts[0], parts[1])
	body, err := boundedexec.Output(command, maxGoListBytes)
	if err != nil {
		return fmt.Errorf("list dependencies for %s: %w", target, err)
	}
	discovered, err := decodeListedModules(bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("decode dependencies for %s: %w", target, err)
	}
	for _, module := range discovered {
		identity := module.Path + "@" + module.Version
		if prior, ok := modules[identity]; ok && prior.Dir != module.Dir {
			return fmt.Errorf("module %s resolved to multiple directories", identity)
		}
		modules[identity] = module
		if len(modules) > maxComponents {
			return fmt.Errorf("release dependency inventory exceeds %d modules", maxComponents)
		}
	}
	return nil
}

func decodeListedModules(input io.Reader) ([]listedModule, error) {
	decoder := json.NewDecoder(input)
	modules := map[string]listedModule{}
	for {
		var pkg listedPackage
		err := decoder.Decode(&pkg)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if pkg.Module == nil || pkg.Module.Main {
			continue
		}
		module := *pkg.Module
		if module.Replace != nil {
			return nil, fmt.Errorf("release dependency %s uses a module replacement", module.Path)
		}
		if module.Path == "" || module.Version == "" || module.Dir == "" {
			return nil, errors.New("release dependency has incomplete module identity")
		}
		identity := module.Path + "@" + module.Version
		if prior, ok := modules[identity]; ok && prior.Dir != module.Dir {
			return nil, fmt.Errorf("module %s resolved to multiple directories", identity)
		}
		modules[identity] = module
		if len(modules) > maxComponents {
			return nil, fmt.Errorf("release dependency inventory exceeds %d modules", maxComponents)
		}
	}
	result := make([]listedModule, 0, len(modules))
	for _, module := range modules {
		result = append(result, module)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Path+"@"+result[i].Version < result[j].Path+"@"+result[j].Version
	})
	return result, nil
}

func targetEnvironment(current []string, goos, goarch string) []string {
	environment := make([]string, 0, len(current)+3)
	for _, value := range current {
		if strings.HasPrefix(value, "GOOS=") || strings.HasPrefix(value, "GOARCH=") ||
			strings.HasPrefix(value, "CGO_ENABLED=") {
			continue
		}
		environment = append(environment, value)
	}
	return append(environment, "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
}

func collectModuleNotices(modules map[string]listedModule) ([]noticeComponent, error) {
	identities := make([]string, 0, len(modules))
	for identity := range modules {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	components := make([]noticeComponent, 0, len(identities))
	total := 0
	for _, identity := range identities {
		files, err := collectLicenseFiles(modules[identity].Dir)
		if err != nil {
			return nil, fmt.Errorf("collect license for %s: %w", identity, err)
		}
		components = append(components, noticeComponent{Identity: identity, Files: files})
		total += noticeFileBytes(files)
		if total > maxNoticeBytes {
			return nil, fmt.Errorf("release license notices exceed %d bytes", maxNoticeBytes)
		}
	}
	return components, nil
}

func collectToolchainNotice(root, goBinary string) (noticeComponent, error) {
	environment, err := loadGoEnvironment(root, goBinary)
	if err != nil {
		return noticeComponent{}, err
	}
	files, err := collectToolchainNoticeFiles(environment.Root)
	if err != nil {
		return noticeComponent{}, err
	}
	return noticeComponent{Identity: "go@" + environment.Version, Files: files}, nil
}

func loadGoEnvironment(root, goBinary string) (goEnvironment, error) {
	command := exec.Command(goBinary, "env", "-json", "GOROOT", "GOVERSION")
	command.Dir = root
	body, err := boundedexec.Output(command, maxGoEnvBytes)
	if err != nil {
		return goEnvironment{}, fmt.Errorf("inspect Go toolchain: %w", err)
	}
	var environment goEnvironment
	if err := json.Unmarshal(body, &environment); err != nil {
		return goEnvironment{}, fmt.Errorf("decode Go toolchain identity: %w", err)
	}
	if environment.Root == "" || environment.Version == "" {
		return goEnvironment{}, errors.New("go toolchain identity is incomplete")
	}
	return environment, nil
}

func collectToolchainNoticeFiles(root string) ([]noticeFile, error) {
	resolvedRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return nil, fmt.Errorf("resolve Go toolchain root: %w", err)
	}
	files, hasLicense, err := collectNoticeFiles(resolvedRoot)
	if err != nil {
		return nil, fmt.Errorf("collect Go toolchain notices: %w", err)
	}
	if !hasLicense && filepath.Base(resolvedRoot) == "libexec" {
		parentFiles, parentHasLicense, parentErr := collectNoticeFiles(filepath.Dir(resolvedRoot))
		if parentErr != nil {
			return nil, fmt.Errorf("collect packaged Go toolchain license: %w", parentErr)
		}
		files, parentErr = mergeNoticeFiles(files, parentFiles)
		if parentErr != nil {
			return nil, fmt.Errorf("merge packaged Go toolchain notices: %w", parentErr)
		}
		hasLicense = parentHasLicense
	}
	if !hasLicense {
		return nil, fmt.Errorf("collect Go toolchain license: %w", errNoLicenseFiles)
	}
	return files, nil
}

func collectLicenseFiles(directory string) ([]noticeFile, error) {
	files, hasLicense, err := collectNoticeFiles(directory)
	if err != nil {
		return nil, err
	}
	if !hasLicense {
		return nil, errNoLicenseFiles
	}
	return files, nil
}

func collectNoticeFiles(directory string) ([]noticeFile, bool, error) {
	entries, err := boundedio.ReadDirNoSymlink(directory, maxModuleEntries)
	if err != nil {
		return nil, false, err
	}
	files := []noticeFile{}
	total := 0
	hasLicense := false
	for _, entry := range entries {
		if !isLicenseFileName(entry.Name()) {
			continue
		}
		body, err := boundedio.ReadRegularFile(filepath.Join(directory, entry.Name()), maxLicenseFileSize)
		if err != nil {
			return nil, false, err
		}
		if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
			return nil, false, fmt.Errorf("license file %s is not safe UTF-8 text", entry.Name())
		}
		total += len(body)
		if total > maxNoticeBytes {
			return nil, false, fmt.Errorf("license files exceed %d bytes", maxNoticeBytes)
		}
		digest := sha256.Sum256(body)
		files = append(files, noticeFile{
			Name: entry.Name(), Digest: hex.EncodeToString(digest[:]), Body: body,
		})
		hasLicense = hasLicense || isLicenseGrantFileName(entry.Name())
	}
	return files, hasLicense, nil
}

func mergeNoticeFiles(first, second []noticeFile) ([]noticeFile, error) {
	byName := make(map[string]noticeFile, len(first)+len(second))
	for _, file := range append(first, second...) {
		if prior, ok := byName[file.Name]; ok && prior.Digest != file.Digest {
			return nil, fmt.Errorf("conflicting notice file %s", file.Name)
		}
		byName[file.Name] = file
	}
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	merged := make([]noticeFile, 0, len(names))
	for _, name := range names {
		merged = append(merged, byName[name])
	}
	return merged, nil
}

func noticeBodyBytes(components []noticeComponent) int {
	total := 0
	for _, component := range components {
		total += noticeFileBytes(component.Files)
	}
	return total
}

func noticeFileBytes(files []noticeFile) int {
	total := 0
	for _, file := range files {
		total += len(file.Body)
	}
	return total
}

func isLicenseFileName(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range []string{"license", "copying", "notice", "patent", "patents"} {
		if lower == prefix || strings.HasPrefix(lower, prefix+".") ||
			strings.HasPrefix(lower, prefix+"-") {
			return true
		}
	}
	return false
}

func isLicenseGrantFileName(name string) bool {
	lower := strings.ToLower(name)
	for _, prefix := range []string{"license", "copying"} {
		if lower == prefix || strings.HasPrefix(lower, prefix+".") ||
			strings.HasPrefix(lower, prefix+"-") {
			return true
		}
	}
	return false
}
