package ingest

import (
	"context"
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
	rootInfo       os.FileInfo
	defaultMatches map[string][]string
	configPath     string
	configIdentity os.FileInfo
}

// NewSourceLoadContext performs discovery once and captures the default
// fragment matches in the same deterministic pattern order used by loading.
func NewSourceLoadContext(repoStartPath string) (*SourceLoadContext, error) {
	return NewSourceLoadContextWithContext(context.Background(), repoStartPath)
}

// NewSourceLoadContextWithContext performs discovery and snapshot capture
// under the caller lifecycle. It never returns a partially bound context.
func NewSourceLoadContextWithContext(ctx context.Context, repoStartPath string) (*SourceLoadContext, error) {
	if ctx == nil {
		return nil, context.Canceled
	}
	discovery, err := DiscoverPolicyRepoWithContext(ctx, repoStartPath)
	if err != nil {
		return nil, err
	}
	return NewSourceLoadContextFromDiscoveryWithContext(ctx, discovery)
}

// NewSourceLoadContextFromDiscovery binds an already completed discovery to
// one source-load snapshot. Diagnostic callers use it to avoid repeating the
// same repository walk while preserving the ordinary identity checks.
func NewSourceLoadContextFromDiscovery(discovery DiscoveryResult) (*SourceLoadContext, error) {
	return NewSourceLoadContextFromDiscoveryWithContext(context.Background(), discovery)
}

// NewSourceLoadContextFromDiscoveryWithContext binds a completed discovery
// to a source-load snapshot while honoring cancellation during bounded inventory.
func NewSourceLoadContextFromDiscoveryWithContext(ctx context.Context, discovery DiscoveryResult) (*SourceLoadContext, error) {
	if ctx == nil {
		return nil, context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	loadContext := &SourceLoadContext{
		Discovery:      discovery,
		RepoRoot:       discovery.RepoRoot,
		defaultMatches: make(map[string][]string, len(DefaultPolicyGlobs)),
	}
	if !discovery.Discovered {
		return loadContext, nil
	}
	var err error
	loadContext.rootIdentity, err = pathidentity.ResolveExisting(discovery.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolve policy source root: %w", err)
	}
	loadContext.rootInfo, err = os.Stat(discovery.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect policy source root: %w", err)
	}
	loadContext.defaultMatches, err = defaultPolicyMatchesWithContext(ctx, discovery.RepoRoot)
	if err != nil {
		return nil, fmt.Errorf("capture default policy fragments: %w", err)
	}
	if discovery.ConfigPath != nil {
		loadContext.configPath = *discovery.ConfigPath
		loadContext.configIdentity, err = captureSourcePathInfo(discovery.RepoRoot, loadContext.configPath)
		if err != nil {
			return nil, fmt.Errorf("capture compiler config identity: %w", err)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return loadContext, nil
}

// Validate rechecks the root, default fragment inventory, and config identity
// before or after source reads. Any change invalidates this context rather
// than allowing a stale discovery result to steer loading.
func (c *SourceLoadContext) Validate() error {
	return c.ValidateWithContext(context.Background())
}

// ValidateWithContext rechecks the bound source snapshot while honoring the
// caller lifecycle during repeated inventory checks.
func (c *SourceLoadContext) ValidateWithContext(ctx context.Context) error {
	if ctx == nil {
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
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
	rootInfo, err := os.Stat(c.RepoRoot)
	if err != nil {
		return fmt.Errorf("inspect policy source root identity: %w", err)
	}
	if c.rootInfo == nil || !rootInfo.IsDir() || !os.SameFile(c.rootInfo, rootInfo) {
		return fmt.Errorf("policy source root identity changed while loading")
	}
	current, err := defaultPolicyMatchesWithContext(ctx, c.RepoRoot)
	if err != nil {
		return fmt.Errorf("revalidate default policy fragments: %w", err)
	}
	if !reflect.DeepEqual(current, c.defaultMatches) {
		return fmt.Errorf("default policy fragment inventory changed while loading")
	}
	if c.configPath != "" {
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := sourcePathInfo(c.RepoRoot, c.configPath)
		if err != nil {
			return fmt.Errorf("revalidate compiler config identity: %w", err)
		}
		if !sameSourceInfo(c.configIdentity, info) {
			return fmt.Errorf("compiler config identity changed while loading")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

func defaultPolicyMatchesWithContext(ctx context.Context, root string) (map[string][]string, error) {
	if ctx == nil {
		return nil, context.Canceled
	}
	matches := make(map[string][]string, len(DefaultPolicyGlobs))
	for _, pattern := range DefaultPolicyGlobs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		found, err := boundedPolicyGlobWithContext(ctx, root, pattern)
		if err != nil {
			return nil, err
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
	return matches, nil
}

func sourcePathInfo(root, relative string) (os.FileInfo, error) {
	return os.Stat(filepath.Join(root, filepath.FromSlash(relative)))
}

func captureSourcePathInfo(root, relative string) (os.FileInfo, error) {
	first, err := sourcePathInfo(root, relative)
	if err != nil {
		return nil, err
	}
	second, err := sourcePathInfo(root, relative)
	if err != nil {
		return nil, err
	}
	if !sameSourceInfo(first, second) {
		return nil, fmt.Errorf("source identity changed during capture")
	}
	return first, nil
}

func sameSourceInfo(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) &&
		left.Mode() == right.Mode() && left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}
