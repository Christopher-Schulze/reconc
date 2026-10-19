package agentsession

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
)

const maxGitLinkBytes = 4096

// LinkedWorktreeRoot returns the top-level directory of the Git checkout that
// contains dir when that checkout is a linked worktree of the repository at
// root, other than root itself, and "" otherwise. Claude Code keeps
// ${CLAUDE_PROJECT_DIR} on the main checkout after EnterWorktree, so a
// session working in .claude/worktrees/<name> reaches its hooks with the main
// root; policy paths only mean what they say relative to the checkout the
// agent works in. A submodule or an unrelated repository never matches: its
// gitdir has no commondir that names this repository's common directory.
func LinkedWorktreeRoot(root, dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	canonicalRoot, err := pathidentity.ResolveExisting(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	checkout, gitLink, err := enclosingCheckout(filepath.Clean(dir))
	if err != nil || checkout == "" {
		return "", err
	}
	canonicalCheckout, err := pathidentity.ResolveExisting(checkout)
	if err != nil {
		return "", fmt.Errorf("resolve checkout %s: %w", checkout, err)
	}
	if canonicalCheckout == canonicalRoot || !gitLink {
		return "", nil
	}
	worktreeCommon, linked, err := linkedWorktreeCommonDir(checkout)
	if err != nil || !linked {
		return "", err
	}
	rootCommon, err := checkoutCommonDir(canonicalRoot)
	if err != nil || rootCommon == "" {
		return "", err
	}
	if worktreeCommon != rootCommon {
		return "", nil
	}
	return canonicalCheckout, nil
}

// enclosingCheckout walks up from dir to the nearest directory with a .git
// entry and reports whether that entry is a gitdir link file.
func enclosingCheckout(dir string) (string, bool, error) {
	for current := dir; ; current = filepath.Dir(current) {
		info, err := os.Lstat(filepath.Join(current, ".git"))
		switch {
		case err == nil:
			return current, info.Mode().IsRegular(), nil
		case !errors.Is(err, fs.ErrNotExist):
			return "", false, fmt.Errorf("inspect %s: %w", filepath.Join(current, ".git"), err)
		}
		if parent := filepath.Dir(current); parent == current {
			return "", false, nil
		}
	}
}

// checkoutCommonDir resolves the common Git directory of a main checkout or a
// linked worktree.
func checkoutCommonDir(checkout string) (string, error) {
	info, err := os.Lstat(filepath.Join(checkout, ".git"))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", filepath.Join(checkout, ".git"), err)
	}
	if info.IsDir() {
		return pathidentity.ResolveExisting(filepath.Join(checkout, ".git"))
	}
	common, _, err := linkedWorktreeCommonDir(checkout)
	return common, err
}

// linkedWorktreeCommonDir follows a checkout's `gitdir:` link to its private
// Git directory and that directory's commondir file. Only linked worktrees
// carry commondir; a submodule's link has none.
func linkedWorktreeCommonDir(checkout string) (string, bool, error) {
	link, err := boundedio.ReadFile(filepath.Join(checkout, ".git"), maxGitLinkBytes)
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", filepath.Join(checkout, ".git"), err)
	}
	target, found := strings.CutPrefix(string(bytes.TrimSpace(link)), "gitdir:")
	if !found {
		return "", false, nil
	}
	gitDir := strings.TrimSpace(target)
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(checkout, gitDir)
	}
	common, err := boundedio.ReadFile(filepath.Join(gitDir, "commondir"), maxGitLinkBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read %s: %w", filepath.Join(gitDir, "commondir"), err)
	}
	commonDir := strings.TrimSpace(string(common))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(gitDir, commonDir)
	}
	resolved, err := pathidentity.ResolveExisting(commonDir)
	if err != nil {
		return "", false, fmt.Errorf("resolve common Git directory of %s: %w", checkout, err)
	}
	return resolved, true, nil
}

// linkedWorktreeWriteBlock denies a write whose target lies in another linked
// worktree of this repository: root-relative policy paths cannot describe that
// checkout, which Reconc governs once the session works inside it.
func linkedWorktreeWriteBlock(root string, targets []string) (string, error) {
	for _, target := range targets {
		if target == "" {
			continue
		}
		absolute := target
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(root, absolute)
		}
		worktree, err := LinkedWorktreeRoot(root, filepath.Dir(absolute))
		if err != nil {
			return "", err
		}
		if worktree != "" {
			return fmt.Sprintf("reconc blocked this file modification before execution.\n- %s belongs to the linked worktree %s; enter that worktree so Reconc evaluates the write against its own checkout.", target, worktree), nil
		}
	}
	return "", nil
}
