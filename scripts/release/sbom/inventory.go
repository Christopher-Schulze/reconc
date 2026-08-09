package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
)

const maxSBOMCommandOutput = 16 << 20

var (
	versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	commitPattern  = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
)

type inventory struct {
	Version   string
	Commit    string
	Created   time.Time
	Toolchain string
	Modules   []moduleRecord
	Edges     []moduleEdge
}

type moduleRecord struct {
	Path    string
	Version string
	Sum     string
	Root    bool
}

type moduleEdge struct {
	From string
	To   string
}

type listedModule struct {
	Path    string
	Version string
	Sum     string
	Main    bool
	Replace *listedModule
}

type moduleRoot struct {
	path string
	dir  string
}

func collectInventory(ctx context.Context, options commandOptions) (inventory, error) {
	created, err := validateIdentity(options.version, options.commit, options.epoch)
	if err != nil {
		return inventory{}, err
	}
	roots := []moduleRoot{{path: "reconc.dev/reconc", dir: options.root}, {path: "reconc-harness/template", dir: filepath.Join(options.root, "harness/template")}}
	modules, err := collectModuleRecords(ctx, roots, options.version)
	if err != nil {
		return inventory{}, err
	}
	edges, err := collectModuleEdges(ctx, roots, modules)
	if err != nil {
		return inventory{}, err
	}
	toolchainBytes, err := runGo(ctx, options.root, "env", "GOVERSION")
	if err != nil {
		return inventory{}, err
	}
	toolchain := strings.TrimSpace(string(toolchainBytes))
	if toolchain == "" {
		return inventory{}, fmt.Errorf("go env GOVERSION returned an empty toolchain version")
	}
	return inventory{Version: options.version, Commit: options.commit, Created: created, Toolchain: toolchain, Modules: modules, Edges: edges}, nil
}

func validateIdentity(version, commit, epoch string) (time.Time, error) {
	if !versionPattern.MatchString(version) {
		return time.Time{}, fmt.Errorf("version must be stable semantic versioning: %q", version)
	}
	if !commitPattern.MatchString(commit) {
		return time.Time{}, fmt.Errorf("commit must be a lowercase Git object ID: %q", commit)
	}
	seconds, err := strconv.ParseInt(epoch, 10, 64)
	if err != nil || seconds < 0 {
		return time.Time{}, fmt.Errorf("source-date-epoch must be a non-negative Unix timestamp: %q", epoch)
	}
	return time.Unix(seconds, 0).UTC(), nil
}

func collectModuleRecords(ctx context.Context, roots []moduleRoot, releaseVersion string) ([]moduleRecord, error) {
	byKey := make(map[string]moduleRecord)
	for _, root := range roots {
		listed, err := listModules(ctx, root.dir)
		if err != nil {
			return nil, err
		}
		for _, module := range listed {
			record := normalizeModule(module, releaseVersion)
			if record.Path == "" || record.Version == "" {
				return nil, fmt.Errorf("module inventory contains an unversioned module: %q@%q", record.Path, record.Version)
			}
			existing := byKey[moduleKey(record.Path, record.Version)]
			record.Root = record.Root || existing.Root
			byKey[moduleKey(record.Path, record.Version)] = record
		}
	}
	modules := make([]moduleRecord, 0, len(byKey))
	for _, module := range byKey {
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool {
		return moduleKey(modules[i].Path, modules[i].Version) < moduleKey(modules[j].Path, modules[j].Version)
	})
	return modules, nil
}

func listModules(ctx context.Context, dir string) ([]listedModule, error) {
	body, err := runGo(ctx, dir, "list", "-m", "-json", "all")
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	modules := []listedModule{}
	for {
		var module listedModule
		if err := decoder.Decode(&module); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode go list modules in %s: %w", dir, err)
		}
		modules = append(modules, module)
	}
	return modules, nil
}

func normalizeModule(module listedModule, releaseVersion string) moduleRecord {
	version := module.Version
	if module.Main {
		version = "v" + releaseVersion
	}
	sum := module.Sum
	if module.Replace != nil && module.Replace.Sum != "" {
		sum = module.Replace.Sum
	}
	return moduleRecord{Path: module.Path, Version: version, Sum: sum, Root: module.Main}
}

func collectModuleEdges(ctx context.Context, roots []moduleRoot, modules []moduleRecord) ([]moduleEdge, error) {
	lookup := moduleLookup(modules)
	seen := make(map[string]moduleEdge)
	for _, root := range roots {
		body, err := runGo(ctx, root.dir, "mod", "graph")
		if err != nil {
			return nil, err
		}
		mergeGraphEdges(string(body), lookup, seen)
	}
	edges := make([]moduleEdge, 0, len(seen))
	for _, edge := range seen {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].From+"\x00"+edges[i].To < edges[j].From+"\x00"+edges[j].To })
	return edges, nil
}

func moduleLookup(modules []moduleRecord) map[string]string {
	lookup := make(map[string]string, len(modules)*2)
	for _, module := range modules {
		key := moduleKey(module.Path, module.Version)
		lookup[key] = key
		lookup[module.Path] = key
	}
	return lookup
}

func mergeGraphEdges(graph string, lookup map[string]string, seen map[string]moduleEdge) {
	for _, line := range strings.Split(graph, "\n") {
		parts := strings.Fields(line)
		if len(parts) != 2 {
			continue
		}
		from, fromOK := resolveModuleKey(parts[0], lookup)
		to, toOK := resolveModuleKey(parts[1], lookup)
		if !fromOK || !toOK || from == to {
			continue
		}
		seen[from+"\x00"+to] = moduleEdge{From: from, To: to}
	}
}

func resolveModuleKey(reference string, lookup map[string]string) (string, bool) {
	if key, ok := lookup[reference]; ok {
		return key, true
	}
	if index := strings.LastIndexByte(reference, '@'); index >= 0 {
		key, ok := lookup[reference[:index]]
		return key, ok
	}
	return "", false
}

func moduleKey(path, version string) string {
	return path + "@" + version
}

func runGo(ctx context.Context, dir string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = dir
	body, err := boundedexec.CombinedOutput(command, maxSBOMCommandOutput)
	if err != nil {
		return nil, fmt.Errorf("go %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(string(body)))
	}
	return body, nil
}
