package ingest

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"reconc.dev/reconc/internal/pathidentity"
)

// SourceLoadContext binds one policy load to the discovery and filesystem
// snapshot that selected its root, config, and default fragment inventory.
// It is evaluation-scoped and must not be retained across compile attempts.
type SourceLoadContext struct {
	Discovery DiscoveryResult
	RepoRoot  string

	rootIdentity   string
	defaultMatches map[string][]string
	configPath     string
	configIdentity os.FileInfo
}

// NewSourceLoadContext performs discovery once and captures the default
// fragment matches in the same deterministic pattern order used by loading.
func NewSourceLoadContext(repoStartPath string) (*SourceLoadContext, error) {
	discovery, err := DiscoverPolicyRepo(repoStartPath)
	if err != nil {
		return nil, err
	}
	context := &SourceLoadContext{
		Discovery:      discovery,
		RepoRoot:       discovery.RepoRoot,
		defaultMatches: make(map[string][]string, len(DefaultPolicyGlobs)),
	}
	if !discovery.Discovered {
		return context, nil
	}
	context.rootIdentity, err = pathidentity.ResolveExisting(discovery.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve policy source root: %w", err)
	}
	context.defaultMatches = defaultPolicyMatches(discovery.RepoRoot)
	if discovery.ConfigPath != nil {
		context.configPath = *discovery.ConfigPath
		context.configIdentity, err = sourcePathInfo(discovery.RepoRoot, context.configPath)
		if err != nil {
			return nil, fmt.Errorf("capture compiler config identity: %w", err)
		}
	}
	return context, nil
}

// Validate rechecks the root, default fragment inventory, and config identity
// before or after source reads. Any change invalidates this context rather
// than allowing a stale discovery result to steer loading.
func (c *SourceLoadContext) Validate() error {
	if c == nil {
		return fmt.Errorf("policy source load context is nil")
	}
	if !c.Discovery.Discovered {
		return nil
	}
	rootIdentity, err := pathidentity.ResolveExisting(c.RepoRoot)
	if err != nil {
		return fmt.Errorf("revalidate policy source root: %w", err)
	}
	if rootIdentity != c.rootIdentity {
		return fmt.Errorf("policy source root identity changed while loading")
	}
	current := defaultPolicyMatches(c.RepoRoot)
	if !reflect.DeepEqual(current, c.defaultMatches) {
		return fmt.Errorf("default policy fragment inventory changed while loading")
	}
	if c.configPath != "" {
		info, err := sourcePathInfo(c.RepoRoot, c.configPath)
		if err != nil {
			return fmt.Errorf("revalidate compiler config identity: %w", err)
		}
		if !sameSourceInfo(c.configIdentity, info) {
			return fmt.Errorf("compiler config identity changed while loading")
		}
	}
	return nil
}

func defaultPolicyMatches(root string) map[string][]string {
	matches := make(map[string][]string, len(DefaultPolicyGlobs))
	for _, pattern := range DefaultPolicyGlobs {
		found, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil {
			matches[pattern] = []string{}
			continue
		}
		paths := make([]string, 0, len(found))
		for _, full := range found {
			if !isRegularFile(full) {
				continue
			}
			rel, err := filepath.Rel(root, full)
			if err == nil {
				paths = append(paths, filepath.ToSlash(rel))
			}
		}
		sort.Strings(paths)
		matches[pattern] = paths
	}
	return matches
}

func sourcePathInfo(root, relative string) (os.FileInfo, error) {
	return os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
}

func sameSourceInfo(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) &&
		left.Mode() == right.Mode() && left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}
