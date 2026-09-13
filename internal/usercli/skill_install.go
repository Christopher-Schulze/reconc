package usercli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reconc.dev/reconc/internal/boundedio"
	skillbundle "reconc.dev/reconc/skills/reconc"
)

type SkillInstallReport struct {
	Path           string `json:"path"`
	ManifestDigest string `json:"manifest_digest"`
	Changed        bool   `json:"changed"`
	Discovery      string `json:"discovery"`
}

var beforeSkillReceiptPublish = func(string) error { return nil }

func skillDiscoveryDetail(path string) string {
	defaultPath, err := resolveSkillDirectory("")
	if err != nil || !samePath(path, defaultPath) {
		return "custom destination; configure each host explicitly and verify loading"
	}
	return "eligible for local shared-root discovery; host loading and shadowing are unverified"
}

type skillInstallation struct {
	target             string
	stage              string
	stageIdentity      os.FileInfo
	referencesIdentity os.FileInfo
	stagedFiles        []string
	backupDir          string
	backupIdentity     os.FileInfo
	previous           *SkillReceipt
	desired            *SkillReceipt
	priorExists        bool
	changed            bool
	published          bool
	committed          bool
}

func resolveSkillDirectory(explicit string) (string, error) {
	path := strings.TrimSpace(explicit)
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home for skill: %w", err)
		}
		path = filepath.Join(home, ".agents", "skills", skillbundle.Name)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve skill directory: %w", err)
	}
	absolute = filepath.Clean(absolute)
	if filepath.Base(absolute) != skillbundle.Name {
		return "", fmt.Errorf("skill directory must end in %q", skillbundle.Name)
	}
	return absolute, nil
}

func embeddedSkillReceipt(path string) (*SkillReceipt, []skillbundle.File, error) {
	manifest, err := skillbundle.BuildManifest()
	if err != nil {
		return nil, nil, err
	}
	files := make([]SkillFileReceipt, 0, len(manifest.Files))
	for _, file := range manifest.Files {
		files = append(files, SkillFileReceipt{Path: file.Path, Size: file.Size, SHA256: file.SHA256})
	}
	return &SkillReceipt{Path: path, ManifestDigest: manifest.Digest, Files: files}, manifest.Files, nil
}

func prepareSkillInstallation(explicit string, previous *Receipt) (*skillInstallation, error) {
	if strings.TrimSpace(explicit) == "" && previous != nil && previous.Skill != nil {
		explicit = previous.Skill.Path
	}
	target, err := resolveSkillDirectory(explicit)
	if err != nil {
		return nil, err
	}
	desired, files, err := embeddedSkillReceipt(target)
	if err != nil {
		return nil, err
	}
	transaction := &skillInstallation{target: target, desired: desired}
	if previous != nil {
		transaction.previous = previous.Skill
	}
	if transaction.previous != nil && !samePath(transaction.previous.Path, target) {
		return nil, fmt.Errorf("owned skill already installed at %s; use that path or remove it explicitly", transaction.previous.Path)
	}
	info, err := os.Lstat(target)
	switch {
	case errors.Is(err, os.ErrNotExist):
		transaction.changed = true
	case err != nil:
		return nil, fmt.Errorf("inspect skill destination: %w", err)
	case !info.IsDir() || info.Mode()&os.ModeSymlink != 0:
		return nil, fmt.Errorf("skill destination is not a real directory: %s", target)
	case transaction.previous == nil:
		return nil, fmt.Errorf("skill destination is unmanaged; preserving %s", target)
	default:
		if err := verifySkillTree(target, transaction.previous.Files); err != nil {
			return nil, fmt.Errorf("owned skill was modified; preserving %s: %w", target, err)
		}
		transaction.priorExists = true
		transaction.changed = !sameSkillReceipt(transaction.previous, desired)
	}
	if !transaction.changed {
		return transaction, nil
	}
	parent := filepath.Dir(target)
	if err := ensureRealDirectory(parent); err != nil {
		return nil, fmt.Errorf("prepare skill parent: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".reconc-skill-stage-*")
	if err != nil {
		return nil, fmt.Errorf("stage skill directory: %w", err)
	}
	transaction.stage = stage
	transaction.stageIdentity, err = os.Lstat(stage)
	if err != nil {
		return transaction, fmt.Errorf("inspect staged skill identity: %w", err)
	}
	if err := os.Mkdir(filepath.Join(stage, "references"), 0o755); err != nil {
		return transaction, err
	}
	transaction.referencesIdentity, err = os.Lstat(filepath.Join(stage, "references"))
	if err != nil {
		return transaction, fmt.Errorf("inspect staged references identity: %w", err)
	}
	for _, file := range files {
		name := filepath.Join(stage, filepath.FromSlash(file.Path))
		transaction.stagedFiles = append(transaction.stagedFiles, file.Path)
		if err := os.WriteFile(name, file.Data, 0o644); err != nil {
			return transaction, fmt.Errorf("stage skill %s: %w", file.Path, err)
		}
	}
	if err := verifySkillTree(stage, desired.Files); err != nil {
		return transaction, fmt.Errorf("verify staged skill: %w", err)
	}
	return transaction, nil
}

func sameSkillReceipt(left, right *SkillReceipt) bool {
	if left == nil || right == nil || left.Path != right.Path || left.ManifestDigest != right.ManifestDigest || len(left.Files) != len(right.Files) {
		return false
	}
	for index := range left.Files {
		if left.Files[index] != right.Files[index] {
			return false
		}
	}
	return true
}

func verifySkillTree(path string, expected []SkillFileReceipt) error {
	root, err := os.Lstat(path)
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("skill root is absent, linked, or not a directory: %w", err)
	}
	allowedRoot := map[string]bool{"references": true}
	allowedReferences := map[string]bool{}
	for _, file := range expected {
		parts := strings.Split(file.Path, "/")
		if len(parts) == 1 {
			allowedRoot[parts[0]] = true
		} else {
			allowedReferences[parts[1]] = true
		}
		body, _, err := boundedio.ReadRegularFileSnapshot(filepath.Join(path, filepath.FromSlash(file.Path)), 64<<10)
		if err != nil {
			return fmt.Errorf("read %s: %w", file.Path, err)
		}
		digest := sha256.Sum256(body)
		if len(body) != file.Size || hex.EncodeToString(digest[:]) != file.SHA256 {
			return fmt.Errorf("checksum or size differs for %s", file.Path)
		}
	}
	for _, inventory := range []struct {
		directory string
		allowed   map[string]bool
	}{{path, allowedRoot}, {filepath.Join(path, "references"), allowedReferences}} {
		info, err := os.Lstat(inventory.directory)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("skill directory is absent, linked, or not a directory: %s: %w", inventory.directory, err)
		}
		entries, err := boundedio.ReadDirNoSymlink(inventory.directory, len(inventory.allowed)+1)
		if err != nil {
			return fmt.Errorf("inventory %s: %w", inventory.directory, err)
		}
		if len(entries) != len(inventory.allowed) {
			return fmt.Errorf("unexpected file count in %s", inventory.directory)
		}
		for _, entry := range entries {
			if !inventory.allowed[entry.Name()] || entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("unexpected or linked skill entry %s", filepath.Join(inventory.directory, entry.Name()))
			}
		}
	}
	return nil
}

func (installation *skillInstallation) publish() error {
	if installation == nil || !installation.changed {
		return nil
	}
	if installation.stage == "" {
		return errors.New("staged skill is unavailable")
	}
	if installation.priorExists {
		if err := verifySkillTree(installation.target, installation.previous.Files); err != nil {
			return fmt.Errorf("revalidate owned skill: %w", err)
		}
		backupDir, err := os.MkdirTemp(filepath.Dir(installation.target), ".reconc-skill-backup-*")
		if err != nil {
			return err
		}
		installation.backupDir = backupDir
		installation.backupIdentity, err = os.Lstat(backupDir)
		if err != nil {
			return fmt.Errorf("inspect skill backup identity: %w", err)
		}
		if err := os.Rename(installation.target, filepath.Join(backupDir, skillbundle.Name)); err != nil {
			return fmt.Errorf("backup owned skill: %w", err)
		}
	} else if _, err := os.Lstat(installation.target); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("skill destination appeared before publication: %v", err)
	}
	if err := os.Rename(installation.stage, installation.target); err != nil {
		return fmt.Errorf("publish skill: %w", err)
	}
	installation.stage = ""
	installation.published = true
	return verifySkillTree(installation.target, installation.desired.Files)
}

func prepareOwnedSkillRemoval(previous *SkillReceipt) (*skillInstallation, error) {
	if err := validateSkillReceipt(previous); err != nil {
		return nil, err
	}
	if err := verifySkillTree(previous.Path, previous.Files); err != nil {
		return nil, fmt.Errorf("owned skill was modified; preserving %s: %w", previous.Path, err)
	}
	return &skillInstallation{target: previous.Path, previous: previous, changed: true, priorExists: true}, nil
}

func (installation *skillInstallation) removeOwned() error {
	if installation == nil || installation.previous == nil || !installation.priorExists {
		return errors.New("owned skill removal has no verified source")
	}
	if err := verifySkillTree(installation.target, installation.previous.Files); err != nil {
		return fmt.Errorf("revalidate owned skill before removal: %w", err)
	}
	backupDir, err := os.MkdirTemp(filepath.Dir(installation.target), ".reconc-skill-backup-*")
	if err != nil {
		return err
	}
	installation.backupDir = backupDir
	installation.backupIdentity, err = os.Lstat(backupDir)
	if err != nil {
		return fmt.Errorf("inspect skill removal backup identity: %w", err)
	}
	if err := os.Rename(installation.target, filepath.Join(backupDir, skillbundle.Name)); err != nil {
		return fmt.Errorf("hold owned skill for removal: %w", err)
	}
	return nil
}

func (installation *skillInstallation) rollback(cause error) error {
	if installation == nil {
		return cause
	}
	if installation.published {
		if err := verifySkillTree(installation.target, installation.desired.Files); err != nil {
			return errors.Join(cause, fmt.Errorf("preserving modified published skill during rollback: %w", err))
		}
		if err := removeVerifiedSkillTree(installation.target, installation.desired.Files); err != nil {
			return errors.Join(cause, fmt.Errorf("remove failed skill publication: %w", err))
		}
	}
	if installation.backupDir != "" && installation.previous != nil {
		if err := installation.validateBackupIdentity(); err != nil {
			return errors.Join(cause, err)
		}
		backup := filepath.Join(installation.backupDir, skillbundle.Name)
		if _, err := os.Lstat(backup); errors.Is(err, os.ErrNotExist) {
			return cause
		} else if err != nil {
			return errors.Join(cause, fmt.Errorf("inspect previous skill backup: %w", err))
		}
		if err := verifySkillTree(backup, installation.previous.Files); err != nil {
			return errors.Join(cause, fmt.Errorf("preserving previous skill backup: %w", err))
		}
		if err := os.Rename(backup, installation.target); err != nil {
			return errors.Join(cause, fmt.Errorf("restore previous skill from %s: %w", backup, err))
		}
	}
	return cause
}

func (installation *skillInstallation) cleanup() error {
	if installation == nil {
		return nil
	}
	if installation.stage != "" {
		if err := removeStagedSkillTree(installation.stage, installation.stageIdentity, installation.referencesIdentity, installation.stagedFiles); err != nil {
			return fmt.Errorf("clean staged skill: %w", err)
		}
	}
	if installation.backupDir != "" {
		if err := installation.validateBackupIdentity(); err != nil {
			return err
		}
		backup := filepath.Join(installation.backupDir, skillbundle.Name)
		if _, err := os.Lstat(backup); err == nil {
			if !installation.committed {
				return fmt.Errorf("preserving uncommitted previous skill backup at %s", backup)
			}
			if installation.previous == nil {
				return errors.New("previous skill backup has no owner")
			}
			if err := verifySkillTree(backup, installation.previous.Files); err != nil {
				return fmt.Errorf("preserving modified previous skill backup: %w", err)
			}
			if err := removeVerifiedSkillTree(backup, installation.previous.Files); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Remove(installation.backupDir)
	}
	return nil
}

func (installation *skillInstallation) validateBackupIdentity() error {
	info, err := os.Lstat(installation.backupDir)
	if err != nil || installation.backupIdentity == nil || !info.IsDir() ||
		info.Mode()&os.ModeSymlink != 0 || !os.SameFile(installation.backupIdentity, info) {
		return fmt.Errorf("skill backup directory changed identity: %s: %w", installation.backupDir, err)
	}
	return nil
}

func removeStagedSkillTree(path string, identity, referencesIdentity os.FileInfo, files []string) error {
	info, err := os.Lstat(path)
	if err != nil || identity == nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, info) {
		return fmt.Errorf("staged skill directory changed identity: %s: %w", path, err)
	}
	references := filepath.Join(path, "references")
	if referencesIdentity != nil {
		if err := verifyStagedReferencesIdentity(references, referencesIdentity); err != nil {
			return err
		}
	} else if len(files) != 0 {
		return errors.New("staged skill files have no references directory identity")
	}
	for _, relative := range files {
		name := filepath.Join(path, filepath.FromSlash(relative))
		entry, err := os.Lstat(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || !entry.Mode().IsRegular() {
			return fmt.Errorf("staged skill file changed type: %s: %w", name, err)
		}
		if err := os.Remove(name); err != nil {
			return err
		}
	}
	if referencesIdentity != nil {
		if err := verifyStagedReferencesIdentity(references, referencesIdentity); err != nil {
			return err
		}
		if err := os.Remove(references); err != nil {
			return err
		}
	}
	return os.Remove(path)
}

func verifyStagedReferencesIdentity(path string, identity os.FileInfo) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(identity, info) {
		return fmt.Errorf("staged references directory changed identity: %s: %w", path, err)
	}
	return nil
}

func removeVerifiedSkillTree(path string, files []SkillFileReceipt) error {
	if err := verifySkillTree(path, files); err != nil {
		return err
	}
	for _, file := range files {
		if err := os.Remove(filepath.Join(path, filepath.FromSlash(file.Path))); err != nil {
			return err
		}
	}
	if err := os.Remove(filepath.Join(path, "references")); err != nil {
		return err
	}
	return os.Remove(path)
}
