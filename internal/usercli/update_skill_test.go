package usercli

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	skillbundle "reconc.dev/reconc/skills/reconc"
)

func TestUpdateRejectsBundleThatDiffersFromCandidateBinary(t *testing.T) {
	root := repositoryRoot(t)
	oldBinary := buildReleaseBinary(t, root, "1.0.0")
	newBinary := buildReleaseBinary(t, root, "1.1.0")
	installDir := t.TempDir()
	installedBinary := filepath.Join(installDir, executableName())
	copyFileForTest(t, oldBinary, installedBinary, 0o755)
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	skillDir := filepath.Join(t.TempDir(), "reconc")
	writeDirectTestReceipt(t, installedBinary, "1.0.0")
	oldSkill := installOldOwnedSkillForUpdate(t, skillDir)
	oldReceipt, _, err := LoadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	manifestBody, archiveBody := alternateSkillPayload(t)
	releaseDir := t.TempDir()
	writeLocalReleaseWithSkillPayload(t, releaseDir, newBinary, "1.1.0", manifestBody, archiveBody)
	enableSuccessfulOfflineAttestation(t, releaseDir, targetArtifact("1.1.0"))
	check, err := CheckUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDir})
	if err != nil || check.Status != LifecycleUpdateAvailable {
		t.Fatalf("valid alternate release bundle check = %+v, %v", check, err)
	}
	applied, err := ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDir})
	if err != nil || applied.Status != LifecycleFailed || applied.Changed ||
		!strings.Contains(applied.Checks[len(applied.Checks)-1].Detail, "differs from the candidate binary") {
		t.Fatalf("mismatched candidate bundle was published: %+v, %v", applied, err)
	}
	current, _, err := LoadReceipt()
	if err != nil || current.ReceiptDigest != oldReceipt.ReceiptDigest ||
		verifySkillTree(skillDir, oldSkill.Files) != nil {
		t.Fatalf("mismatched candidate bundle changed ownership: %+v, %v", current, err)
	}
	digest, err := fileSHA256(installedBinary)
	if err != nil || digest != oldReceipt.ArtifactSHA256 {
		t.Fatalf("mismatched candidate bundle changed binary: %v", err)
	}
}

func TestUpdatePreservesCurrentOwnedSkillWhenBinaryChanges(t *testing.T) {
	root := repositoryRoot(t)
	oldBinary := buildReleaseBinary(t, root, "1.0.0")
	newBinary := buildReleaseBinary(t, root, "1.1.0")
	installDir := t.TempDir()
	installedBinary := filepath.Join(installDir, executableName())
	copyFileForTest(t, oldBinary, installedBinary, 0o755)
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	skillDir := filepath.Join(t.TempDir(), "reconc")
	writeDirectTestReceipt(t, installedBinary, "1.0.0")
	installOldOwnedSkillForUpdate(t, skillDir)
	oldReleaseDir := t.TempDir()
	writeLocalReleaseWithSkill(t, oldReleaseDir, oldBinary, "1.0.0")
	if report, err := ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: oldReleaseDir}); err != nil || report.Status != LifecycleUpdated {
		t.Fatalf("establish current owned skill: %+v, %v", report, err)
	}
	before, err := os.Lstat(skillDir)
	if err != nil {
		t.Fatal(err)
	}
	newReleaseDir := t.TempDir()
	manifest := writeLocalReleaseWithSkill(t, newReleaseDir, newBinary, "1.1.0")
	enableSuccessfulOfflineAttestation(t, newReleaseDir, targetArtifact("1.1.0"))
	check, err := CheckUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: newReleaseDir})
	if err != nil || check.Status != LifecycleUpdateAvailable || check.Skill == nil || check.Skill.State != SkillCurrent {
		t.Fatalf("current owned skill/new binary check = %+v, %v", check, err)
	}
	applied, err := ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: newReleaseDir})
	if err != nil || applied.Status != LifecycleUpdated || !applied.Changed ||
		applied.Skill == nil || applied.Skill.State != SkillCurrent {
		t.Fatalf("current owned skill/new binary apply = %+v, %v", applied, err)
	}
	after, err := os.Lstat(skillDir)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("current owned skill was unnecessarily replaced: %v", err)
	}
	current, _, err := LoadReceipt()
	if err != nil || current.Version != "1.1.0" || current.Skill == nil ||
		verifySkillTree(skillDir, current.Skill.Files) != nil {
		t.Fatalf("new binary lost owned skill: %+v, %v", current, err)
	}
	digest, err := fileSHA256(installedBinary)
	if err != nil || digest != selectedBinaryDigest(t, manifest, "1.1.0") {
		t.Fatalf("binary did not update: %v", err)
	}
}

func TestCoordinatedUpdatePreservesExternalBinaryReplacementDuringRollback(t *testing.T) {
	root := repositoryRoot(t)
	oldBinary := buildReleaseBinary(t, root, "1.0.0")
	newBinary := buildReleaseBinary(t, root, "1.1.0")
	installDir := t.TempDir()
	installedBinary := filepath.Join(installDir, executableName())
	copyFileForTest(t, oldBinary, installedBinary, 0o755)
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	skillDir := filepath.Join(t.TempDir(), "reconc")
	writeDirectTestReceipt(t, installedBinary, "1.0.0")
	oldSkill := installOldOwnedSkillForUpdate(t, skillDir)
	oldReceipt, _, err := LoadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	releaseDir := t.TempDir()
	writeLocalReleaseWithSkill(t, releaseDir, newBinary, "1.1.0")
	enableSuccessfulOfflineAttestation(t, releaseDir, targetArtifact("1.1.0"))
	previousHook := beforeSkillReceiptPublish
	t.Cleanup(func() { beforeSkillReceiptPublish = previousHook })
	beforeSkillReceiptPublish = func(string) error {
		if err := mutateDirectUpdateTarget(installedBinary, "regular"); err != nil {
			return err
		}
		return errors.New("injected failure after external binary replacement")
	}
	report, err := ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDir})
	if err != nil || report.Status != LifecycleFailed || !report.Changed ||
		!strings.Contains(report.Checks[len(report.Checks)-1].Detail, "restore previous binary") {
		t.Fatalf("external replacement was not reported as partial failure: %+v, %v", report, err)
	}
	assertDirectUpdateTargetMutation(t, installedBinary, "regular")
	current, _, err := LoadReceipt()
	if err != nil || current.ReceiptDigest != oldReceipt.ReceiptDigest ||
		verifySkillTree(skillDir, oldSkill.Files) != nil {
		t.Fatalf("failed rollback changed receipt or owned skill: %+v, %v", current, err)
	}
	backups, err := filepath.Glob(filepath.Join(installDir, ".reconc-backup-*.private"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("previous binary backup was not retained: %v, %v", backups, err)
	}
	digest, err := fileSHA256(backups[0])
	if err != nil || digest != oldReceipt.ArtifactSHA256 {
		t.Fatalf("retained backup differs from previous owned binary: %v", err)
	}
}

func TestOwnedSkillUpdateSerializesWithExplicitInstall(t *testing.T) {
	root := repositoryRoot(t)
	binary := buildReleaseBinary(t, root, "1.0.0")
	installDir := t.TempDir()
	installedBinary := filepath.Join(installDir, executableName())
	copyFileForTest(t, binary, installedBinary, 0o755)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	skillDir := filepath.Join(t.TempDir(), "reconc")
	writeDirectTestReceipt(t, installedBinary, "1.0.0")
	installOldOwnedSkillForUpdate(t, skillDir)
	releaseDir := t.TempDir()
	writeLocalReleaseWithSkill(t, releaseDir, binary, "1.0.0")
	entered := make(chan struct{})
	proceed := make(chan struct{})
	defer func() {
		select {
		case <-proceed:
		default:
			close(proceed)
		}
	}()
	previousHook := beforeSkillReceiptPublish
	t.Cleanup(func() { beforeSkillReceiptPublish = previousHook })
	beforeSkillReceiptPublish = func(string) error {
		close(entered)
		<-proceed
		return nil
	}
	type updateResult struct {
		report *LifecycleReport
		err    error
	}
	updateDone := make(chan updateResult, 1)
	go func() {
		report, err := ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDir})
		updateDone <- updateResult{report: report, err: err}
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("owned skill update did not enter receipt publication")
	}
	install := exec.Command(installedBinary, "install-cli", "--skill-only", "--skill-dir", skillDir, "--json")
	if err := install.Start(); err != nil {
		t.Fatal(err)
	}
	installDone := make(chan error, 1)
	go func() { installDone <- install.Wait() }()
	select {
	case err := <-installDone:
		t.Fatalf("explicit install bypassed the held update lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(proceed)
	result := <-updateDone
	if result.err != nil || result.report.Status != LifecycleUpdated {
		t.Fatalf("held skill update failed: %+v, %v", result.report, result.err)
	}
	if err := <-installDone; err != nil {
		t.Fatalf("serialized explicit install failed: %v", err)
	}
	current, _, err := LoadReceipt()
	if err != nil || current.Skill == nil || verifySkillTree(skillDir, current.Skill.Files) != nil {
		t.Fatalf("concurrent update/install lost ownership: %+v, %v", current, err)
	}
}

func alternateSkillPayload(t *testing.T) ([]byte, []byte) {
	t.Helper()
	files, err := skillbundle.Files()
	if err != nil {
		t.Fatal(err)
	}
	files[0].Data = append(bytes.Clone(files[0].Data), []byte("\nAlternate valid release skill.\n")...)
	files[0].Size = len(files[0].Data)
	sum := sha256.Sum256(files[0].Data)
	files[0].SHA256 = hex.EncodeToString(sum[:])
	digest, err := skillbundle.DigestFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	manifestBody, err := json.Marshal(skillbundle.Manifest{FormatVersion: "1", Name: skillbundle.Name, Files: files, Digest: digest})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, file := range files {
		header := &zip.FileHeader{Name: skillbundle.Name + "/" + file.Path, Method: zip.Deflate}
		header.SetMode(0o644)
		entry, err := archive.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(file.Data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return manifestBody, output.Bytes()
}

func TestUpdateChecksMissingSkillBeforeSameBinaryReturn(t *testing.T) {
	root := repositoryRoot(t)
	binary := buildReleaseBinary(t, root, "1.0.0")
	installDir := t.TempDir()
	installedBinary := filepath.Join(installDir, executableName())
	copyFileForTest(t, binary, installedBinary, 0o755)
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	t.Setenv("HOME", t.TempDir())
	writeDirectTestReceipt(t, installedBinary, "1.0.0")
	releaseDir := t.TempDir()
	writeLocalReleaseWithSkill(t, releaseDir, binary, "1.0.0")
	before, err := os.Lstat(installedBinary)
	if err != nil {
		t.Fatal(err)
	}
	check, err := CheckUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDir})
	if err != nil || check.Status != LifecycleUpdateAvailable || check.Changed ||
		check.Skill == nil || check.Skill.State != SkillMissing || check.Skill.Action == nil {
		t.Fatalf("missing skill check = %+v, %v", check, err)
	}
	if got := check.Skill.Action.Argv; len(got) != 3 || got[0] != "reconc" || got[1] != "install-cli" || got[2] != "--skill-only" ||
		check.Skill.Action.Cwd == "" || check.Skill.Action.Authorization != "explicit-user-action" {
		t.Fatalf("missing skill action = %+v", check.Skill.Action)
	}
	apply, err := ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDir})
	if err != nil || apply.Status != LifecycleRefused || apply.Changed {
		t.Fatalf("missing skill apply = %+v, %v", apply, err)
	}
	after, err := os.Lstat(installedBinary)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("missing skill update replaced binary: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(os.Getenv("HOME"), ".agents", "skills", "reconc")); !os.IsNotExist(err) {
		t.Fatalf("missing skill was installed silently: %v", err)
	}
}

func TestUpdateCLIRequiresExplicitMissingSkillAction(t *testing.T) {
	root := repositoryRoot(t)
	binary := buildReleaseBinary(t, root, "1.0.0")
	installDir := t.TempDir()
	installedBinary := filepath.Join(installDir, executableName())
	copyFileForTest(t, binary, installedBinary, 0o755)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	writeDirectTestReceipt(t, installedBinary, "1.0.0")
	releaseDir := t.TempDir()
	writeLocalReleaseWithSkill(t, releaseDir, binary, "1.0.0")
	check, err := runUpdateCLI(t, installedBinary, "update", "check", "--from-dir", releaseDir, "--json")
	if err != nil || check.Status != LifecycleUpdateAvailable || check.Skill == nil || check.Skill.Action == nil {
		t.Fatalf("CLI omitted missing-skill action: %+v, %v", check, err)
	}
	apply, err := runUpdateCLI(t, installedBinary, "update", "apply", "--from-dir", releaseDir, "--json")
	if err == nil || apply.Status != LifecycleRefused || apply.Changed {
		t.Fatalf("CLI installed missing skill without explicit action: %+v, %v", apply, err)
	}
	install := exec.Command(installedBinary, "install-cli", "--skill-only", "--json")
	if body, err := install.CombinedOutput(); err != nil {
		t.Fatalf("explicit skill action failed: %v\n%s", err, body)
	}
	current, err := runUpdateCLI(t, installedBinary, "update", "check", "--from-dir", releaseDir, "--json")
	if err != nil || current.Status != LifecycleCurrent || current.Skill == nil || current.Skill.State != SkillCurrent {
		t.Fatalf("CLI did not recognize explicit skill installation: %+v, %v", current, err)
	}
}

func runUpdateCLI(t *testing.T, installedBinary string, args ...string) (*LifecycleReport, error) {
	t.Helper()
	command := exec.Command(installedBinary, args...)
	body, commandErr := command.CombinedOutput()
	var report LifecycleReport
	if err := json.Unmarshal(body, &report); err != nil {
		t.Fatalf("CLI output is not one lifecycle document: %v\n%s", err, body)
	}
	return &report, commandErr
}

func TestUpdateRepairsStaleOwnedSkillWithoutReplacingCurrentBinary(t *testing.T) {
	root := repositoryRoot(t)
	binary := buildReleaseBinary(t, root, "1.0.0")
	installDir := t.TempDir()
	installedBinary := filepath.Join(installDir, executableName())
	copyFileForTest(t, binary, installedBinary, 0o755)
	t.Setenv("RECONC_HOME", t.TempDir())
	t.Setenv("RECONC_INSTALL_DIR", installDir)
	t.Setenv("PATH", installDir)
	skillDir := filepath.Join(t.TempDir(), "reconc")
	writeDirectTestReceipt(t, installedBinary, "1.0.0")
	previousSkill := installOldOwnedSkillForUpdate(t, skillDir)
	releaseDir := t.TempDir()
	writeLocalReleaseWithSkill(t, releaseDir, binary, "1.0.0")
	before, err := os.Lstat(installedBinary)
	if err != nil {
		t.Fatal(err)
	}
	check, err := runUpdateCLI(t, installedBinary, "update", "check", "--from-dir", releaseDir, "--json")
	if err != nil || check.Status != LifecycleUpdateAvailable || check.Changed || check.Skill == nil ||
		check.Skill.State != SkillStaleOwned || len(check.Actions) != 0 {
		t.Fatalf("stale owned skill check = %+v, %v", check, err)
	}
	if err := verifySkillTree(skillDir, previousSkill.Files); err != nil {
		t.Fatalf("read-only update check changed skill: %v", err)
	}
	apply, err := runUpdateCLI(t, installedBinary, "update", "apply", "--from-dir", releaseDir, "--json")
	if err != nil || apply.Status != LifecycleUpdated || !apply.Changed || apply.Skill == nil || apply.Skill.State != SkillCurrent {
		t.Fatalf("stale owned skill apply = %+v, %v", apply, err)
	}
	after, err := os.Lstat(installedBinary)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("skill-only update replaced binary: %v", err)
	}
	current, _, err := LoadReceipt()
	if err != nil || current.Skill == nil || current.Skill.ManifestDigest == previousSkill.ManifestDigest ||
		verifySkillTree(skillDir, current.Skill.Files) != nil {
		t.Fatalf("skill-only update lost ownership: %+v, %v", current, err)
	}
}

func TestUpdateRejectsModifiedOwnedSkillAndRestoresOnReceiptFailure(t *testing.T) {
	for _, scenario := range []string{"modified", "receipt failure"} {
		t.Run(scenario, func(t *testing.T) {
			root := repositoryRoot(t)
			binary := buildReleaseBinary(t, root, "1.0.0")
			installDir := t.TempDir()
			installedBinary := filepath.Join(installDir, executableName())
			copyFileForTest(t, binary, installedBinary, 0o755)
			t.Setenv("RECONC_HOME", t.TempDir())
			t.Setenv("RECONC_INSTALL_DIR", installDir)
			t.Setenv("PATH", installDir)
			skillDir := filepath.Join(t.TempDir(), "reconc")
			writeDirectTestReceipt(t, installedBinary, "1.0.0")
			oldSkill := installOldOwnedSkillForUpdate(t, skillDir)
			releaseDir := t.TempDir()
			writeLocalReleaseWithSkill(t, releaseDir, binary, "1.0.0")
			oldReceipt, _, err := LoadReceipt()
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "modified" {
				file, err := os.OpenFile(filepath.Join(skillDir, "SKILL.md"), os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := file.WriteString("\nuser edit\n"); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				oldHook := beforeSkillReceiptPublish
				t.Cleanup(func() { beforeSkillReceiptPublish = oldHook })
				beforeSkillReceiptPublish = func(string) error { return errors.New("injected skill receipt failure") }
			}
			var report *LifecycleReport
			if scenario == "modified" {
				before := readTestFile(t, filepath.Join(skillDir, "SKILL.md"))
				check, checkErr := runUpdateCLI(t, installedBinary, "update", "check", "--from-dir", releaseDir, "--json")
				if checkErr == nil || check.Status != LifecycleRefused || check.Skill == nil || check.Skill.State != SkillModifiedOwned || check.Changed {
					t.Fatalf("modified skill CLI check = %+v, %v", check, checkErr)
				}
				report, err = runUpdateCLI(t, installedBinary, "update", "apply", "--from-dir", releaseDir, "--json")
				if !bytes.Equal(before, readTestFile(t, filepath.Join(skillDir, "SKILL.md"))) {
					t.Fatal("modified skill CLI update changed user content")
				}
			} else {
				report, err = ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDir})
			}
			if (scenario == "modified" && err == nil) || (scenario != "modified" && err != nil) || report.Changed {
				t.Fatalf("unsafe skill update = %+v, %v", report, err)
			}
			if scenario == "modified" && report.Status != LifecycleRefused ||
				scenario == "receipt failure" && report.Status != LifecycleFailed {
				t.Fatalf("unsafe skill update status = %+v", report)
			}
			current, _, err := LoadReceipt()
			if err != nil || current.ReceiptDigest != oldReceipt.ReceiptDigest {
				t.Fatalf("unsafe skill update changed receipt: %+v, %v", current, err)
			}
			if scenario == "receipt failure" {
				if err := verifySkillTree(skillDir, oldSkill.Files); err != nil {
					t.Fatalf("failed skill update did not restore old payload: %v", err)
				}
			}
		})
	}
}

func TestUpdateCoordinatesBinaryAndOwnedSkillPublication(t *testing.T) {
	root := repositoryRoot(t)
	oldBinary := buildReleaseBinary(t, root, "1.0.0")
	newBinary := buildReleaseBinary(t, root, "1.1.0")
	for _, failReceipt := range []bool{false, true} {
		name := "success"
		if failReceipt {
			name = "receipt failure"
		}
		t.Run(name, func(t *testing.T) {
			installDir := t.TempDir()
			installedBinary := filepath.Join(installDir, executableName())
			copyFileForTest(t, oldBinary, installedBinary, 0o755)
			t.Setenv("RECONC_HOME", t.TempDir())
			t.Setenv("RECONC_INSTALL_DIR", installDir)
			t.Setenv("PATH", installDir)
			skillDir := filepath.Join(t.TempDir(), "reconc")
			writeDirectTestReceipt(t, installedBinary, "1.0.0")
			oldSkill := installOldOwnedSkillForUpdate(t, skillDir)
			oldReceipt, _, err := LoadReceipt()
			if err != nil {
				t.Fatal(err)
			}
			releaseDir := t.TempDir()
			manifest := writeLocalReleaseWithSkill(t, releaseDir, newBinary, "1.1.0")
			enableSuccessfulOfflineAttestation(t, releaseDir, targetArtifact("1.1.0"))
			if failReceipt {
				oldHook := beforeSkillReceiptPublish
				t.Cleanup(func() { beforeSkillReceiptPublish = oldHook })
				beforeSkillReceiptPublish = func(string) error { return errors.New("injected coordinated receipt failure") }
			}
			check, err := CheckUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDir})
			if err != nil || check.Status != LifecycleUpdateAvailable || check.Skill == nil ||
				check.Skill.State != SkillStaleOwned || len(check.Actions) != 1 {
				t.Fatalf("coordinated update check = %+v, %v", check, err)
			}
			applied, err := ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDir})
			if err != nil {
				t.Fatal(err)
			}
			current, _, err := LoadReceipt()
			if err != nil {
				t.Fatal(err)
			}
			actualBinary, err := fileSHA256(installedBinary)
			if err != nil {
				t.Fatal(err)
			}
			if failReceipt {
				if applied.Status != LifecycleFailed || applied.Changed || current.ReceiptDigest != oldReceipt.ReceiptDigest ||
					actualBinary != oldReceipt.ArtifactSHA256 || verifySkillTree(skillDir, oldSkill.Files) != nil {
					t.Fatalf("failed coordinated update changed owned state: %+v receipt=%+v", applied, current)
				}
				return
			}
			if applied.Status != LifecycleUpdated || !applied.Changed || applied.Skill == nil || applied.Skill.State != SkillCurrent ||
				current.Skill == nil || current.Skill.ManifestDigest == oldSkill.ManifestDigest ||
				actualBinary != selectedBinaryDigest(t, manifest, "1.1.0") || verifySkillTree(skillDir, current.Skill.Files) != nil {
				t.Fatalf("coordinated update omitted a component: %+v receipt=%+v", applied, current)
			}
		})
	}
}

func selectedBinaryDigest(t *testing.T, manifest ReleaseManifest, version string) string {
	t.Helper()
	asset, ok := releaseAssetByName(manifest, targetArtifact(version))
	if !ok {
		t.Fatal("selected release omitted binary")
	}
	return asset.SHA256
}

func TestUpdateRefusesUnavailableOrCorruptSelectedSkillBundle(t *testing.T) {
	root := repositoryRoot(t)
	binary := buildReleaseBinary(t, root, "1.0.0")
	for _, scenario := range []string{"unavailable", "corrupt archive"} {
		t.Run(scenario, func(t *testing.T) {
			installDir := t.TempDir()
			installedBinary := filepath.Join(installDir, executableName())
			copyFileForTest(t, binary, installedBinary, 0o755)
			t.Setenv("RECONC_HOME", t.TempDir())
			t.Setenv("RECONC_INSTALL_DIR", installDir)
			t.Setenv("PATH", installDir)
			skillDir := filepath.Join(t.TempDir(), "reconc")
			writeDirectTestReceipt(t, installedBinary, "1.0.0")
			oldSkill := installOldOwnedSkillForUpdate(t, skillDir)
			oldReceipt, _, err := LoadReceipt()
			if err != nil {
				t.Fatal(err)
			}
			releaseDir := t.TempDir()
			if scenario == "unavailable" {
				writeLocalRelease(t, releaseDir, binary, "1.0.0")
			} else {
				manifest, err := skillbundle.EncodeManifest()
				if err != nil {
					t.Fatal(err)
				}
				writeLocalReleaseWithSkillPayload(t, releaseDir, binary, "1.0.0", manifest, []byte("invalid ZIP with a valid release checksum"))
			}
			check, err := CheckUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDir})
			if err != nil || check.Skill == nil {
				t.Fatalf("selected bundle check = %+v, %v", check, err)
			}
			if scenario == "unavailable" {
				if check.Status != LifecycleRefused || check.Skill.State != SkillUnavailable {
					t.Fatalf("missing bundle was accepted: %+v", check)
				}
			} else if check.Status != LifecycleUpdateAvailable || check.Skill.State != SkillStaleOwned {
				t.Fatalf("manifest was not independently checked: %+v", check)
			}
			applied, err := ApplyUpdate(context.Background(), "1.0.0", UpdateRequest{FromDir: releaseDir})
			if err != nil || applied.Changed ||
				scenario == "unavailable" && applied.Status != LifecycleRefused ||
				scenario == "corrupt archive" && applied.Status != LifecycleFailed {
				t.Fatalf("unsafe bundle apply = %+v, %v", applied, err)
			}
			current, _, err := LoadReceipt()
			if err != nil || current.ReceiptDigest != oldReceipt.ReceiptDigest || verifySkillTree(skillDir, oldSkill.Files) != nil {
				t.Fatalf("unsafe bundle changed owned skill: %+v, %v", current, err)
			}
		})
	}
}

func writeLocalReleaseWithSkill(t *testing.T, directory, binary, version string) ReleaseManifest {
	t.Helper()
	manifestBody, err := skillbundle.EncodeManifest()
	if err != nil {
		t.Fatal(err)
	}
	archiveBody, err := skillbundle.Archive()
	if err != nil {
		t.Fatal(err)
	}
	return writeLocalReleaseWithSkillPayload(t, directory, binary, version, manifestBody, archiveBody)
}

func writeLocalReleaseWithSkillPayload(t *testing.T, directory, binary, version string, manifestBody, archiveBody []byte) ReleaseManifest {
	t.Helper()
	assetName := targetArtifact(version)
	copyFileForTest(t, binary, filepath.Join(directory, assetName), 0o755)
	assets := []struct {
		name string
		body []byte
	}{
		{name: assetName, body: readTestFile(t, filepath.Join(directory, assetName))},
		{name: "reconc-skill-" + version + ".json", body: manifestBody},
		{name: "reconc-skill-" + version + ".zip", body: archiveBody},
	}
	manifest := ReleaseManifest{
		FormatVersion: releaseManifestFormat, Repository: releaseRepository,
		Tag: "reconc-v" + version, Version: version,
	}
	for _, asset := range assets {
		if asset.name != assetName {
			if err := os.WriteFile(filepath.Join(directory, asset.name), asset.body, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		sum := sha256.Sum256(asset.body)
		manifest.Assets = append(manifest.Assets, ReleaseAsset{Name: asset.name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(asset.body))})
	}
	sort.Slice(manifest.Assets, func(left, right int) bool { return manifest.Assets[left].Name < manifest.Assets[right].Name })
	releaseBody, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	releaseBody = append(releaseBody, '\n')
	if err := os.WriteFile(filepath.Join(directory, releaseManifestName), releaseBody, 0o600); err != nil {
		t.Fatal(err)
	}
	manifestSum := sha256.Sum256(releaseBody)
	var checksums strings.Builder
	for _, asset := range manifest.Assets {
		checksums.WriteString(asset.SHA256 + "  " + asset.Name + "\n")
	}
	checksums.WriteString(hex.EncodeToString(manifestSum[:]) + "  " + releaseManifestName + "\n")
	if err := os.WriteFile(filepath.Join(directory, releaseChecksumsName), []byte(checksums.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func installOldOwnedSkillForUpdate(t *testing.T, path string) *SkillReceipt {
	t.Helper()
	files, err := skillbundle.Files()
	if err != nil {
		t.Fatal(err)
	}
	files[0].Data = append(bytes.Clone(files[0].Data), []byte("\nPrevious owned skill.\n")...)
	files[0].Size = len(files[0].Data)
	sum := sha256.Sum256(files[0].Data)
	files[0].SHA256 = hex.EncodeToString(sum[:])
	digest, err := skillbundle.DigestFiles(files)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	skill := &SkillReceipt{Path: path, ManifestDigest: digest}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(path, filepath.FromSlash(file.Path)), file.Data, 0o644); err != nil {
			t.Fatal(err)
		}
		skill.Files = append(skill.Files, SkillFileReceipt{Path: file.Path, Size: file.Size, SHA256: file.SHA256})
	}
	previous, _, err := LoadReceipt()
	if err != nil {
		t.Fatal(err)
	}
	previous.Skill = skill
	previous.InstalledAt = time.Unix(2, 0).UTC().Format(time.RFC3339)
	previous.ReceiptDigest, err = computeReceiptDigest(previous)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := WriteReceipt(previous); err != nil {
		t.Fatal(err)
	}
	return skill
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return body
}
