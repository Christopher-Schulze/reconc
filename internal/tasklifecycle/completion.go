package tasklifecycle

import "strings"

// DirtyCompletionPaths returns the Git-dirty paths owned by the configured
// TASK control plane. Both the final completion gate and terminal Stop hook
// use this exact path contract for completion.require_committed.
func DirtyCompletionPaths(cfg Config, dirtyPaths []string) []string {
	paths := make([]string, 0)
	detailDir := strings.TrimSuffix(cfg.DetailDir, "/")
	for _, path := range dirtyPaths {
		dirtyDir := strings.TrimSuffix(path, "/")
		if path == cfg.OverviewPath || dirtyDir == detailDir ||
			strings.HasPrefix(path, detailDir+"/") ||
			(strings.HasSuffix(path, "/") && (strings.HasPrefix(cfg.OverviewPath, dirtyDir+"/") || strings.HasPrefix(detailDir, dirtyDir+"/"))) {
			paths = append(paths, path)
		}
	}
	return paths
}
