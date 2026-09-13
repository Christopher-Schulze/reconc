package usercli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	skillbundle "reconc.dev/reconc/skills/reconc"
)

func TestOwnedSkillInstallIsIdempotentAndPreservesModifiedContent(t *testing.T) {
	installDir := t.TempDir()
	skillDir := filepath.Join(t.TempDir(), "reconc")
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	options := InstallOptions{Version: "test", SkillMode: SkillInstall, SkillDir: skillDir}

	first, err := InstallCurrentWithReceipt("", options)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Status.Ready || first.Receipt == nil || first.Receipt.Skill == nil ||
		first.Skill == nil || !first.Skill.Changed || !first.Changed {
		t.Fatalf("first owned skill install = %+v", first)
	}
	if err := verifySkillTree(skillDir, first.Receipt.Skill.Files); err != nil {
		t.Fatalf("installed payload: %v", err)
	}
	second, err := InstallCurrentWithReceipt("", options)
	if err != nil {
		t.Fatal(err)
	}
	if second.Changed || second.Skill == nil || second.Skill.Changed ||
		second.Receipt.ReceiptDigest != first.Receipt.ReceiptDigest {
		t.Fatalf("repeated install changed owned state: %+v", second)
	}

	skillFile := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillFile, []byte("user modification\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCurrentWithReceipt("", options); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Fatalf("modified owned skill was not preserved: %v", err)
	}
	contents, err := os.ReadFile(skillFile)
	if err != nil || string(contents) != "user modification\n" {
		t.Fatalf("modified content was overwritten: %q, %v", contents, err)
	}
	loaded, _, err := LoadReceipt()
	if err != nil || loaded.ReceiptDigest != first.Receipt.ReceiptDigest {
		t.Fatalf("failed install changed receipt: %+v, %v", loaded, err)
	}
}

func TestSkillOnlyAddsOwnershipWithoutReplacingBinary(t *testing.T) {
	installDir := t.TempDir()
	skillDir := filepath.Join(t.TempDir(), "reconc")
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	previous, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test", SkillMode: SkillSkip})
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(previous.Status.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	report, err := InstallCurrentWithReceipt("", InstallOptions{
		Version: "test", SkillMode: SkillOnly, SkillDir: skillDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(previous.Status.TargetPath)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) || report.Receipt == nil || report.Receipt.Skill == nil ||
		report.Skill == nil || !report.Skill.Changed {
		t.Fatalf("skill-only changed binary or missed ownership: %+v", report)
	}
	files, err := skillbundle.Files()
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Receipt.Skill.Files) != len(files) {
		t.Fatalf("receipt omitted embedded files: %+v", report.Receipt.Skill)
	}
}

func TestSkillInstallRefusesForeignDirectoryBeforeBinaryChange(t *testing.T) {
	installDir := t.TempDir()
	skillDir := filepath.Join(t.TempDir(), "reconc")
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	previous, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test", SkillMode: SkillSkip})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(foreign, []byte("foreign skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCurrentWithReceipt("", InstallOptions{
		Version: "test", SkillMode: SkillInstall, SkillDir: skillDir,
	}); err == nil || !strings.Contains(err.Error(), "unmanaged") {
		t.Fatalf("foreign skill conflict = %v", err)
	}
	current, _, err := LoadReceipt()
	if err != nil || current.ReceiptDigest != previous.Receipt.ReceiptDigest || current.Skill != nil {
		t.Fatalf("foreign conflict changed receipt: %+v, %v", current, err)
	}
	contents, err := os.ReadFile(foreign)
	if err != nil || string(contents) != "foreign skill\n" {
		t.Fatalf("foreign skill changed: %q, %v", contents, err)
	}
}

func TestOwnedSkillRemovalRequiresExplicitFlagAndUntouchedFiles(t *testing.T) {
	for _, removeSkill := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserve", true: "remove"}[removeSkill], func(t *testing.T) {
			installDir := t.TempDir()
			skillDir := filepath.Join(t.TempDir(), "reconc")
			t.Setenv("RECONC_HOME", t.TempDir())
			t.Setenv("RECONC_INSTALL_DIR", installDir)
			t.Setenv("PATH", installDir)
			installed, err := InstallCurrentWithReceipt("", InstallOptions{
				Version: "test", SkillMode: SkillInstall, SkillDir: skillDir,
			})
			if err != nil {
				t.Fatal(err)
			}
			report, err := Uninstall(context.Background(), "test", UninstallRequest{RemoveSkill: removeSkill})
			if err != nil || report.Status != LifecycleUninstalled {
				t.Fatalf("uninstall = %+v, %v", report, err)
			}
			if _, err := os.Lstat(installed.Status.TargetPath); !os.IsNotExist(err) {
				t.Fatalf("binary was retained: %v", err)
			}
			_, err = os.Lstat(skillDir)
			if removeSkill && !os.IsNotExist(err) || !removeSkill && err != nil {
				t.Fatalf("skill preservation differs from explicit choice: %v", err)
			}
		})
	}
}

func TestOwnedSkillRemovalRefusesModifiedSkillWithoutUninstallingBinary(t *testing.T) {
	installDir := t.TempDir()
	skillDir := filepath.Join(t.TempDir(), "reconc")
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	installed, err := InstallCurrentWithReceipt("", InstallOptions{
		Version: "test", SkillMode: SkillInstall, SkillDir: skillDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := Uninstall(context.Background(), "test", UninstallRequest{RemoveSkill: true})
	if err != nil || report.Status != LifecycleRefused || report.Changed {
		t.Fatalf("modified skill removal = %+v, %v", report, err)
	}
	if _, err := os.Lstat(installed.Status.TargetPath); err != nil {
		t.Fatalf("binary was removed after skill conflict: %v", err)
	}
	if _, _, err := LoadReceipt(); err != nil {
		t.Fatalf("receipt was removed after skill conflict: %v", err)
	}
}

func TestSkillPublicationFailureRestoresPreviousReceiptAndLeavesNoSkill(t *testing.T) {
	installDir := t.TempDir()
	skillDir := filepath.Join(t.TempDir(), "reconc")
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	previous, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test", SkillMode: SkillSkip})
	if err != nil {
		t.Fatal(err)
	}
	previousHook := beforeSkillReceiptPublish
	t.Cleanup(func() { beforeSkillReceiptPublish = previousHook })
	beforeSkillReceiptPublish = func(string) error { return errors.New("injected receipt publication failure") }
	if _, err := InstallCurrentWithReceipt("", InstallOptions{
		Version: "test", SkillMode: SkillInstall, SkillDir: skillDir,
	}); err == nil || !strings.Contains(err.Error(), "injected receipt publication failure") {
		t.Fatalf("publication failure was lost: %v", err)
	}
	if _, err := os.Lstat(skillDir); !os.IsNotExist(err) {
		t.Fatalf("failed publication retained an unreceipted skill: %v", err)
	}
	loaded, _, err := LoadReceipt()
	if err != nil || loaded.ReceiptDigest != previous.Receipt.ReceiptDigest {
		t.Fatalf("failed publication changed old receipt: %+v, %v", loaded, err)
	}
	if status, err := InspectCurrent(installDir); err != nil || !status.Ready {
		t.Fatalf("failed publication changed owned binary: %+v, %v", status, err)
	}
}

func TestOwnedSkillRemovalFailureRestoresSkillAndBinary(t *testing.T) {
	installDir := t.TempDir()
	skillDir := filepath.Join(t.TempDir(), "reconc")
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	installed, err := InstallCurrentWithReceipt("", InstallOptions{
		Version: "test", SkillMode: SkillInstall, SkillDir: skillDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	previousHook := beforeUninstallRemoval
	t.Cleanup(func() { beforeUninstallRemoval = previousHook })
	beforeUninstallRemoval = func(path string) error {
		if samePath(path, installed.Status.TargetPath) {
			return errors.New("injected binary removal failure")
		}
		return nil
	}
	report, err := Uninstall(context.Background(), "test", UninstallRequest{RemoveSkill: true})
	if err != nil || report.Status != LifecycleRefused || report.Changed {
		t.Fatalf("removal failure = %+v, %v", report, err)
	}
	if err := verifySkillTree(skillDir, installed.Receipt.Skill.Files); err != nil {
		t.Fatalf("failed uninstall lost the skill: %v", err)
	}
	if _, err := os.Lstat(installed.Status.TargetPath); err != nil {
		t.Fatalf("failed uninstall lost the binary: %v", err)
	}
	loaded, _, err := LoadReceipt()
	if err != nil || loaded.ReceiptDigest != installed.Receipt.ReceiptDigest {
		t.Fatalf("failed uninstall changed receipt: %+v, %v", loaded, err)
	}
}

func TestOwnedSkillRejectsMissingExtraAndLinkedEntries(t *testing.T) {
	for _, scenario := range []string{"missing reference", "extra file", "linked reference"} {
		t.Run(scenario, func(t *testing.T) {
			installDir := t.TempDir()
			skillDir := filepath.Join(t.TempDir(), "reconc")
			t.Setenv("RECONC_HOME", t.TempDir())
			t.Setenv("RECONC_INSTALL_DIR", installDir)
			t.Setenv("PATH", installDir)
			options := InstallOptions{Version: "test", SkillMode: SkillInstall, SkillDir: skillDir}
			installed, err := InstallCurrentWithReceipt("", options)
			if err != nil {
				t.Fatal(err)
			}
			reference := filepath.Join(skillDir, "references", "workflow-and-evidence.md")
			switch scenario {
			case "missing reference":
				err = os.Remove(reference)
			case "extra file":
				err = os.WriteFile(filepath.Join(skillDir, "references", "foreign.md"), []byte("foreign\n"), 0o644)
			case "linked reference":
				if runtime.GOOS == "windows" {
					t.Skip("symlink creation requires Windows developer privileges")
				}
				if err = os.Remove(reference); err == nil {
					err = os.Symlink(filepath.Join(skillDir, "SKILL.md"), reference)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := InstallCurrentWithReceipt("", options); err == nil || !strings.Contains(err.Error(), "modified") {
				t.Fatalf("unsafe skill was accepted: %v", err)
			}
			loaded, _, err := LoadReceipt()
			if err != nil || loaded.ReceiptDigest != installed.Receipt.ReceiptDigest {
				t.Fatalf("unsafe skill changed receipt: %+v, %v", loaded, err)
			}
		})
	}
}

func TestConcurrentOwnedSkillInstallsSerialize(t *testing.T) {
	installDir := t.TempDir()
	skillDir := filepath.Join(t.TempDir(), "reconc")
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	options := InstallOptions{Version: "test", SkillMode: SkillInstall, SkillDir: skillDir}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := InstallCurrentWithReceipt("", options)
			results <- err
		}()
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent install failed: %v", err)
		}
	}
	loaded, _, err := LoadReceipt()
	if err != nil || loaded.Skill == nil {
		t.Fatalf("concurrent install lost ownership: %+v, %v", loaded, err)
	}
	if err := verifySkillTree(skillDir, loaded.Skill.Files); err != nil {
		t.Fatalf("concurrent install changed skill: %v", err)
	}
}

func TestGlobalDoctorSeparatesSkillIntegrityFromBinaryReadiness(t *testing.T) {
	installDir := t.TempDir()
	skillDir := filepath.Join(t.TempDir(), "reconc")
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	if _, err := InstallCurrentWithReceipt("", InstallOptions{
		Version: "test", SkillMode: SkillInstall, SkillDir: skillDir,
	}); err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		diagnostic, err := DiagnoseGlobal("test")
		if err != nil {
			t.Fatal(err)
		}
		if diagnostic.Status != DiagnosticHealthy || !diagnostic.ChecksumIdentity {
			t.Fatalf("skill changed binary diagnosis: %+v", diagnostic)
		}
		for _, item := range diagnostic.Checks {
			if item.Name == "skill-installation" {
				if item.Status != want {
					t.Fatalf("skill installation check = %+v, want %s", item, want)
				}
				return
			}
		}
		t.Fatal("skill installation check was omitted")
	}
	check("pass")
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	check("fail")
}

func TestReadOnlySkillHomeFailsBeforeBinaryPublication(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("POSIX permission behavior requires an unprivileged account")
	}
	home := t.TempDir()
	installDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	if err := os.Chmod(home, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o755) })
	if _, err := InstallCurrentWithReceipt("", InstallOptions{Version: "test", SkillMode: SkillInstall}); err == nil {
		t.Fatal("read-only skill home allowed global installation")
	}
	if _, err := os.Lstat(filepath.Join(installDir, executableName())); !os.IsNotExist(err) {
		t.Fatalf("failed skill preflight published a binary: %v", err)
	}
	if _, _, err := LoadReceipt(); !os.IsNotExist(err) {
		t.Fatalf("failed skill preflight published a receipt: %v", err)
	}
}

func TestStagedSkillCleanupPreservesSubstitutedReferences(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires Windows developer privileges")
	}
	transaction, err := prepareSkillInstallation(filepath.Join(t.TempDir(), "reconc"), nil)
	if err != nil {
		t.Fatal(err)
	}
	references := filepath.Join(transaction.stage, "references")
	saved := filepath.Join(t.TempDir(), "references")
	if err := os.Rename(references, saved); err != nil {
		t.Fatal(err)
	}
	foreign := t.TempDir()
	foreignFile := filepath.Join(foreign, "workflow-and-evidence.md")
	if err := os.WriteFile(foreignFile, []byte("foreign\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, references); err != nil {
		t.Fatal(err)
	}
	if err := transaction.cleanup(); err == nil || !strings.Contains(err.Error(), "changed identity") {
		t.Fatalf("substituted references were accepted: %v", err)
	}
	if body, err := os.ReadFile(foreignFile); err != nil || string(body) != "foreign\n" {
		t.Fatalf("cleanup touched foreign content: %q, %v", body, err)
	}
	if err := os.Remove(references); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(saved, references); err != nil {
		t.Fatal(err)
	}
	if err := transaction.cleanup(); err != nil {
		t.Fatalf("cleanup after restoring identity: %v", err)
	}
}

func TestSkillReceiptRejectsManifestDigestContradictingFiles(t *testing.T) {
	receipt, _, err := embeddedSkillReceipt(filepath.Join(t.TempDir(), "reconc"))
	if err != nil {
		t.Fatal(err)
	}
	receipt.ManifestDigest = strings.Repeat("0", 64)
	if err := validateSkillReceipt(receipt); err == nil || !strings.Contains(err.Error(), "manifest digest mismatch") {
		t.Fatalf("contradictory skill manifest digest was accepted: %v", err)
	}
}
