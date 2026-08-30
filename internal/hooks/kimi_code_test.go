package hooks

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"reconc.dev/reconc/internal/usercli"
)

func TestKimiCodeInstallIsIsolatedIdempotentAndReversible(t *testing.T) {
	enableKimiCodeCLIForTest(t)
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	original := []byte(`default_model = "kimi-code"` + "\n")
	configPath, _, err := kimiCodeConfigPath(false)
	if err != nil {
		t.Fatalf("kimiCodeConfigPath: %v", err)
	}
	if err := os.WriteFile(configPath, original, 0o640); err != nil {
		t.Fatal(err)
	}

	report, err := Install(KindKimiCode, "ignored", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if report.RepoRoot != "global" || report.Action != "created" || report.TargetPath != configPath {
		t.Fatalf("install report = %#v", report)
	}
	installed := readKimiCodeTestFile(t, configPath)
	if !bytes.HasPrefix(installed, original) || strings.Count(string(installed), "[[hooks]]") != 16 {
		t.Fatalf("install did not preserve config or install all hooks:\n%s", installed)
	}
	if err := validateKimiCodeTOML(installed); err != nil {
		t.Fatalf("installed TOML: %v", err)
	}
	if mode := kimiCodeTestFileMode(t, configPath); runtime.GOOS != "windows" && mode != 0o640 {
		t.Fatalf("config mode = %o, want 640", mode)
	}

	report, err = Install(KindKimiCode, "ignored", false)
	if err != nil {
		t.Fatalf("second Install: %v", err)
	}
	if report.Action != "unchanged" || !bytes.Equal(readKimiCodeTestFile(t, configPath), installed) {
		t.Fatalf("second install report = %#v", report)
	}

	uninstallReport, err := Uninstall(KindKimiCode, "ignored")
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if uninstallReport.Action != "updated" || uninstallReport.RemovedEntries != 16 {
		t.Fatalf("uninstall report = %#v", uninstallReport)
	}
	if current := readKimiCodeTestFile(t, configPath); !bytes.Equal(current, original) {
		t.Fatalf("uninstall changed unrelated bytes:\n got %q\nwant %q", current, original)
	}
}

func TestKimiCodeInstallPreservesConfigWithoutFinalNewline(t *testing.T) {
	enableKimiCodeCLIForTest(t)
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	configPath := filepath.Join(home, "config.toml")
	original := []byte(`default_model = "kimi-code"`)
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindKimiCode, ".", false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := Uninstall(KindKimiCode, "."); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if current := readKimiCodeTestFile(t, configPath); !bytes.Equal(current, original) {
		t.Fatalf("uninstall changed EOF bytes: got %q, want %q", current, original)
	}
}

func TestKimiCodeManagedBlockRoundTripPreservesMarkerLikeTOML(t *testing.T) {
	artifact, err := generateKimiCode()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body string
	}{
		{name: "basic string", body: `note = "# >>> reconc kimi-code hooks / # <<< reconc kimi-code hooks"` + "\n"},
		{name: "array strings", body: `markers = ["# >>> reconc kimi-code hooks", "# <<< reconc kimi-code hooks"]` + "\n"},
		{name: "inline table strings", body: `marker = { start = "# >>> reconc kimi-code hooks", end = "# <<< reconc kimi-code hooks" }` + "\n"},
		{name: "ordinary comment", body: "# marker-like text: " + KimiCodeManagedBlockStart + " and " + KimiCodeManagedBlockEnd + "\nkey = 1\n"},
		{name: "indented comments", body: "  " + KimiCodeManagedBlockStart + "\n\t" + KimiCodeManagedBlockEnd + "\nkey = 1\n"},
		{name: "trailing comments", body: "key = 1 " + KimiCodeManagedBlockStart + "\nother = 2 " + KimiCodeManagedBlockEnd + "\n"},
		{name: "array comments", body: "values = [\n" + KimiCodeManagedBlockStart + "\n1,\n" + KimiCodeManagedBlockEnd + "\n]\n"},
		{name: "multiline basic string", body: "note = \"\"\"\n" + KimiCodeManagedBlockStart + "\n" + KimiCodeManagedBlockEnd + "\n\"\"\"\n"},
		{name: "multiline literal managed block", body: "note = '''" + artifact.Content + "'''\n"},
		{name: "CRLF content", body: "key = 1\r\n# unrelated\r\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			enableKimiCodeCLIForTest(t)
			original := []byte(test.body)
			if err := validateKimiCodeTOML(original); err != nil {
				t.Fatalf("invalid test TOML: %v\n%s", err, original)
			}
			if _, present, err := currentKimiCodeBlock(original); err != nil || present {
				t.Fatalf("marker-like TOML detected as managed block: present=%t err=%v", present, err)
			}
			home := t.TempDir()
			t.Setenv("KIMI_CODE_HOME", home)
			configPath := filepath.Join(home, "config.toml")
			if err := os.WriteFile(configPath, original, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(KindKimiCode, ".", false); err != nil {
				t.Fatalf("Install: %v", err)
			}
			if _, err := Uninstall(KindKimiCode, "."); err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
			if current := readKimiCodeTestFile(t, configPath); !bytes.Equal(current, original) {
				t.Fatalf("round trip changed unrelated TOML:\n got %q\nwant %q", current, original)
			}
		})
	}
}

func TestKimiCodeInstallCreatesOnlyTheIsolatedPrivateHome(t *testing.T) {
	enableKimiCodeCLIForTest(t)
	home := filepath.Join(t.TempDir(), "new-kimi-home")
	t.Setenv("KIMI_CODE_HOME", home)

	report, err := Install(KindKimiCode, ".", false)
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(home))
	if err != nil {
		t.Fatal(err)
	}
	resolvedHome := filepath.Join(resolvedParent, filepath.Base(home))
	if report.TargetPath != filepath.Join(resolvedHome, "config.toml") {
		t.Fatalf("target path = %q", report.TargetPath)
	}
	info, err := os.Stat(resolvedHome)
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() || runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("created Kimi home mode = %s", info.Mode())
	}
	if hooks := strings.Count(string(readKimiCodeTestFile(t, report.TargetPath)), "[[hooks]]"); hooks != 16 {
		t.Fatalf("installed hook count = %d", hooks)
	}
}

func TestKimiCodeDriftRequiresForceAndCreatesExactBackup(t *testing.T) {
	enableKimiCodeCLIForTest(t)
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	configPath := filepath.Join(home, "config.toml")
	if _, err := Install(KindKimiCode, ".", false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	drifted := bytes.Replace(
		readKimiCodeTestFile(t, configPath),
		[]byte("reconc hook kimi-runtime receipt-v1 kimi-pre-tool-use"),
		[]byte("custom-pre-tool-command"),
		1,
	)
	if err := os.WriteFile(configPath, drifted, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(KindKimiCode, ".", false); err == nil || !strings.Contains(err.Error(), "--force") {
		t.Fatalf("non-force drift error = %v", err)
	}
	if current := readKimiCodeTestFile(t, configPath); !bytes.Equal(current, drifted) {
		t.Fatal("failed non-force install mutated drifted config")
	}

	report, err := Install(KindKimiCode, ".", true)
	if err != nil {
		t.Fatalf("force Install: %v", err)
	}
	if report.Action != "updated" || report.BackupPath == "" {
		t.Fatalf("force report = %#v", report)
	}
	if backup := readKimiCodeTestFile(t, report.BackupPath); !bytes.Equal(backup, drifted) {
		t.Fatal("force backup does not preserve exact pre-change config")
	}
}

func TestKimiCodeInstallRefusesInvalidOrMalformedConfigWithoutMutation(t *testing.T) {
	enableKimiCodeCLIForTest(t)
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid TOML", body: `[[hooks]`},
		{name: "unpaired marker", body: KimiCodeManagedBlockStart + "\n"},
		{name: "duplicate marker", body: KimiCodeManagedBlockStart + "\n" + KimiCodeManagedBlockEnd + "\n" + KimiCodeManagedBlockEnd + "\n"},
		{name: "nested markers", body: KimiCodeManagedBlockStart + "\n" + KimiCodeManagedBlockStart + "\n" + KimiCodeManagedBlockEnd + "\n" + KimiCodeManagedBlockEnd + "\n"},
		{name: "reversed markers", body: KimiCodeManagedBlockEnd + "\n" + KimiCodeManagedBlockStart + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("KIMI_CODE_HOME", home)
			configPath := filepath.Join(home, "config.toml")
			original := []byte(test.body)
			if err := os.WriteFile(configPath, original, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Install(KindKimiCode, ".", true); err == nil {
				t.Fatal("Install succeeded for unsafe config")
			}
			if current := readKimiCodeTestFile(t, configPath); !bytes.Equal(current, original) {
				t.Fatal("failed install mutated unsafe config")
			}
		})
	}
}

func TestKimiCodeGeneratedContractMatchesStrictHostSchema(t *testing.T) {
	artifact, err := Generate(KindKimiCode)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	var config struct {
		Hooks []struct {
			Event   string  `toml:"event"`
			Matcher *string `toml:"matcher"`
			Command string  `toml:"command"`
			Timeout int     `toml:"timeout"`
		} `toml:"hooks"`
	}
	if err := toml.Unmarshal([]byte(artifact.Content), &config); err != nil {
		t.Fatalf("decode generated TOML: %v", err)
	}
	if len(config.Hooks) != 16 {
		t.Fatalf("hook count = %d, want 16", len(config.Hooks))
	}
	seen := map[string]bool{}
	for _, hook := range config.Hooks {
		if hook.Event == "" || hook.Command == "" || hook.Timeout < 1 || hook.Timeout > 600 {
			t.Fatalf("invalid generated hook: %#v", hook)
		}
		if hook.Matcher != nil {
			t.Fatalf("global policy route unexpectedly narrows matcher: %#v", hook)
		}
		if seen[hook.Event] {
			t.Fatalf("duplicate generated event %q", hook.Event)
		}
		seen[hook.Event] = true
	}
}

func TestKimiCodeStatusSeparatesConfigurationFromLiveExecution(t *testing.T) {
	enableKimiCodeCLIForTest(t)
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	if _, err := Install(KindKimiCode, ".", false); err != nil {
		t.Fatalf("Install: %v", err)
	}
	platform, ok := PlatformForKind(KindKimiCode)
	if !ok {
		t.Fatal("Kimi Code platform missing")
	}
	status := inspectKimiCodePlatform(platform)
	if status.State != StateConfigured || !status.Generated || !status.Installed ||
		!status.Executable || !status.Configured || status.Live {
		t.Fatalf("status = %#v", status)
	}
}

func TestKimiCodeStatusRejectsReplacementAndAcceptsReceiptUpgrade(t *testing.T) {
	cliInstall := enableKimiCodeCLIForTest(t)
	home := t.TempDir()
	t.Setenv("KIMI_CODE_HOME", home)
	if _, err := Install(KindKimiCode, ".", false); err != nil {
		t.Fatal(err)
	}
	platform, ok := PlatformForKind(KindKimiCode)
	if !ok {
		t.Fatal("Kimi Code platform missing")
	}
	if err := os.WriteFile(cliInstall.Receipt.BinaryPath, []byte("replacement"), 0o755); err != nil {
		t.Fatal(err)
	}
	drifted := inspectKimiCodePlatform(platform)
	if drifted.State != StateDegraded || !strings.Contains(drifted.Detail, "checksum changed") {
		t.Fatalf("replacement status = %#v", drifted)
	}
	upgraded, err := usercli.InstallCurrentWithReceipt(
		filepath.Dir(cliInstall.Receipt.BinaryPath),
		usercli.InstallOptions{Version: "test-upgrade"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.Receipt == nil || upgraded.Receipt.ReceiptDigest == cliInstall.Receipt.ReceiptDigest {
		t.Fatalf("upgrade report = %+v", upgraded)
	}
	healthy := inspectKimiCodePlatform(platform)
	if healthy.State != StateConfigured || !healthy.Configured || !healthy.Executable {
		t.Fatalf("upgraded status = %#v", healthy)
	}
}

func TestKimiCodeStatusDistinguishesUnsafeConfigurationAndCLIStates(t *testing.T) {
	platform, ok := PlatformForKind(KindKimiCode)
	if !ok {
		t.Fatal("Kimi Code platform missing")
	}
	t.Run("absent", func(t *testing.T) {
		t.Setenv("KIMI_CODE_HOME", t.TempDir())
		status := inspectKimiCodePlatform(platform)
		if status.State != StateAbsent || status.Installed {
			t.Fatalf("absent status = %#v", status)
		}
	})
	t.Run("invalid TOML", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("KIMI_CODE_HOME", home)
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[[hooks]"), 0o600); err != nil {
			t.Fatal(err)
		}
		status := inspectKimiCodePlatform(platform)
		if status.State != StateDegraded || !strings.Contains(status.Detail, "invalid TOML") {
			t.Fatalf("invalid status = %#v", status)
		}
	})
	t.Run("managed drift", func(t *testing.T) {
		enableKimiCodeCLIForTest(t)
		home := t.TempDir()
		t.Setenv("KIMI_CODE_HOME", home)
		if _, err := Install(KindKimiCode, ".", false); err != nil {
			t.Fatal(err)
		}
		configPath := filepath.Join(home, "config.toml")
		body := bytes.Replace(
			readKimiCodeTestFile(t, configPath),
			[]byte("reconc hook kimi-runtime receipt-v1 kimi-stop"),
			[]byte("reconc hook kimi-runtime receipt-v1 custom-stop"),
			1,
		)
		if err := os.WriteFile(configPath, body, 0o600); err != nil {
			t.Fatal(err)
		}
		status := inspectKimiCodePlatform(platform)
		if status.State != StateDegraded || !strings.Contains(status.Detail, "differs") {
			t.Fatalf("drift status = %#v", status)
		}
	})
	t.Run("missing bare CLI", func(t *testing.T) {
		enableKimiCodeCLIForTest(t)
		home := t.TempDir()
		t.Setenv("KIMI_CODE_HOME", home)
		if _, err := Install(KindKimiCode, ".", false); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", t.TempDir())
		status := inspectKimiCodePlatform(platform)
		if status.State != StateDegraded || status.Executable || !strings.Contains(status.Detail, "not visible") {
			t.Fatalf("missing CLI status = %#v", status)
		}
	})
	t.Run("different bare CLI", func(t *testing.T) {
		enableKimiCodeCLIForTest(t)
		home := t.TempDir()
		t.Setenv("KIMI_CODE_HOME", home)
		if _, err := Install(KindKimiCode, ".", false); err != nil {
			t.Fatal(err)
		}
		binDir := t.TempDir()
		name := "reconc"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("different"), 0o700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", binDir)
		status := inspectKimiCodePlatform(platform)
		if status.State != StateDegraded || status.Executable || !strings.Contains(status.Detail, "not the receipt-owned binary") {
			t.Fatalf("different CLI status = %#v", status)
		}
	})
}

func TestKimiCodeInstallRejectsMissingOrDifferentBareCLIWithoutMutation(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T)
		want  string
	}{
		{
			name: "missing PATH entry",
			setup: func(t *testing.T) {
				t.Setenv("PATH", t.TempDir())
			},
			want: "not visible on PATH",
		},
		{
			name: "missing receipt",
			setup: func(t *testing.T) {
				t.Setenv("RECONC_HOME", t.TempDir())
			},
			want: "load installation receipt",
		},
		{
			name: "different bytes",
			setup: func(t *testing.T) {
				binDir := t.TempDir()
				name := "reconc"
				if runtime.GOOS == "windows" {
					name += ".exe"
				}
				if err := os.WriteFile(filepath.Join(binDir, name), []byte("not reconc"), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", binDir)
			},
			want: "not the receipt-owned binary",
		},
		{
			name: "invalid PATH",
			setup: func(t *testing.T) {
				entry := t.TempDir() + string(os.PathListSeparator)
				t.Setenv("PATH", strings.Repeat(entry, 1100))
			},
			want: "PATH contains more than",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			enableKimiCodeCLIForTest(t)
			home := t.TempDir()
			t.Setenv("KIMI_CODE_HOME", home)
			test.setup(t)
			if _, err := Install(KindKimiCode, ".", false); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Install error = %v, want %q", err, test.want)
			}
			if _, err := os.Stat(filepath.Join(home, "config.toml")); !os.IsNotExist(err) {
				t.Fatalf("failed preflight mutated Kimi Code config: %v", err)
			}
		})
	}
}

func TestKimiCodeUninstallRefusesUnsafeStateAndTreatsAbsenceAsNoOp(t *testing.T) {
	t.Run("missing home", func(t *testing.T) {
		t.Setenv("KIMI_CODE_HOME", filepath.Join(t.TempDir(), "missing"))
		report, err := Uninstall(KindKimiCode, ".")
		if err != nil {
			t.Fatalf("Uninstall: %v", err)
		}
		if report.Action != "absent" {
			t.Fatalf("report = %#v", report)
		}
	})
	t.Run("missing config", func(t *testing.T) {
		t.Setenv("KIMI_CODE_HOME", t.TempDir())
		report, err := Uninstall(KindKimiCode, ".")
		if err != nil {
			t.Fatalf("Uninstall: %v", err)
		}
		if report.Action != "absent" {
			t.Fatalf("report = %#v", report)
		}
	})
	tests := []struct {
		name string
		body func(*testing.T) []byte
		want string
	}{
		{
			name: "invalid TOML",
			body: func(*testing.T) []byte { return []byte("[[hooks]") },
			want: "invalid TOML",
		},
		{
			name: "modified managed block",
			body: func(t *testing.T) []byte {
				t.Helper()
				artifact, err := generateKimiCode()
				if err != nil {
					t.Fatal(err)
				}
				return bytes.Replace([]byte(artifact.Content), []byte("kimi-stop"), []byte("custom-stop"), 1)
			},
			want: "refusing to remove modified content",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("KIMI_CODE_HOME", home)
			configPath := filepath.Join(home, "config.toml")
			original := test.body(t)
			if err := os.WriteFile(configPath, original, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Uninstall(KindKimiCode, "."); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Uninstall error = %v, want %q", err, test.want)
			}
			if current := readKimiCodeTestFile(t, configPath); !bytes.Equal(current, original) {
				t.Fatal("failed uninstall mutated unsafe config")
			}
		})
	}
}

func TestKimiCodeManagedBlockHelpersRejectAmbiguousMarkers(t *testing.T) {
	artifact, err := generateKimiCode()
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("default_model = \"kimi-code\"\n")
	updated, removed, err := removeKimiCodeBlock(plain, artifact.Content)
	if err != nil || removed || !bytes.Equal(updated, plain) {
		t.Fatalf("remove absent block = %q, %t, %v", updated, removed, err)
	}
	if _, err := replaceKimiCodeBlock(plain, kimiCodeManagedBlock{start: -1, end: 1}, "replacement"); err == nil {
		t.Fatal("replace accepted an invalid structural boundary")
	}
	unsafe := []string{
		KimiCodeManagedBlockStart + "\n",
		KimiCodeManagedBlockEnd + "\n",
		KimiCodeManagedBlockStart + "\n" + KimiCodeManagedBlockStart + "\n" + KimiCodeManagedBlockEnd + "\n",
		KimiCodeManagedBlockStart + "\n" + KimiCodeManagedBlockEnd + "\n" + KimiCodeManagedBlockEnd + "\n",
		KimiCodeManagedBlockEnd + "\n" + KimiCodeManagedBlockStart + "\n",
	}
	for _, body := range unsafe {
		if _, _, err := currentKimiCodeBlock([]byte(body)); err == nil {
			t.Fatalf("ambiguous markers accepted:\n%s", body)
		}
	}
}

func TestKimiCodeStatusRejectsUnsafeHomeAndMarkers(t *testing.T) {
	platform, ok := PlatformForKind(KindKimiCode)
	if !ok {
		t.Fatal("Kimi Code platform missing")
	}
	t.Run("home is file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "kimi-home")
		if err := os.WriteFile(path, []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("KIMI_CODE_HOME", path)
		status := inspectKimiCodePlatform(platform)
		if status.State != StateDegraded || !strings.Contains(status.Detail, "not a real directory") {
			t.Fatalf("status = %#v", status)
		}
	})
	t.Run("ambiguous markers", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("KIMI_CODE_HOME", home)
		body := KimiCodeManagedBlockStart + "\n" + KimiCodeManagedBlockStart + "\n" + KimiCodeManagedBlockEnd + "\n"
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		status := inspectKimiCodePlatform(platform)
		if status.State != StateDegraded || !strings.Contains(status.Detail, "malformed or duplicate") {
			t.Fatalf("status = %#v", status)
		}
	})
	t.Run("config path is directory", func(t *testing.T) {
		home := t.TempDir()
		t.Setenv("KIMI_CODE_HOME", home)
		if err := os.Mkdir(filepath.Join(home, "config.toml"), 0o700); err != nil {
			t.Fatal(err)
		}
		status := inspectKimiCodePlatform(platform)
		if status.State != StateDegraded || !strings.Contains(status.Detail, "not a regular file") {
			t.Fatalf("status = %#v", status)
		}
	})
	t.Run("PATH inspection fails", func(t *testing.T) {
		enableKimiCodeCLIForTest(t)
		home := t.TempDir()
		t.Setenv("KIMI_CODE_HOME", home)
		artifact, err := generateKimiCode()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(artifact.Content), 0o600); err != nil {
			t.Fatal(err)
		}
		entry := t.TempDir() + string(os.PathListSeparator)
		t.Setenv("PATH", strings.Repeat(entry, 1100))
		status := inspectKimiCodePlatform(platform)
		if status.State != StateDegraded || !strings.Contains(status.Detail, "not bound to a valid installation receipt") {
			t.Fatalf("status = %#v", status)
		}
	})
}

func TestKimiCodePathAndLockSafetyEdges(t *testing.T) {
	t.Setenv("KIMI_CODE_HOME", "")
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	configPath, lockHome, err := kimiCodeConfigPath(false)
	if err != nil {
		t.Fatalf("kimiCodeConfigPath: %v", err)
	}
	resolvedHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	if configPath != filepath.Join(resolvedHome, ".kimi-code", "config.toml") || lockHome != "" {
		t.Fatalf("config path = %q, lock home = %q", configPath, lockHome)
	}
	if _, err := os.Stat(filepath.Dir(configPath)); !os.IsNotExist(err) {
		t.Fatalf("read-only path lookup created Kimi home: %v", err)
	}
	if err := withKimiCodeLock("", func() error { return nil }); err == nil {
		t.Fatal("empty Kimi Code lock home succeeded")
	}
	lockHome = t.TempDir()
	sentinel := os.ErrPermission
	if err := withKimiCodeLock(lockHome, func() error { return sentinel }); !os.IsPermission(err) {
		t.Fatalf("lock callback error = %v", err)
	}
}

func enableKimiCodeCLIForTest(t *testing.T) *usercli.InstallReport {
	t.Helper()
	t.Setenv("RECONC_HOME", t.TempDir())
	binDir := t.TempDir()
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	report, err := usercli.InstallCurrentWithReceipt(binDir, usercli.InstallOptions{Version: "test"})
	if err != nil {
		t.Fatalf("install receipt-bound test CLI: %v", err)
	}
	if report.Receipt == nil {
		t.Fatalf("test CLI installation did not publish a receipt: %+v", report)
	}
	return report
}

func readKimiCodeTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func kimiCodeTestFileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}
