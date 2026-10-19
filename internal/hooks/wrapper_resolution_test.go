package hooks

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"reconc.dev/reconc/buildprovenance"
)

func TestWrapperStatusIdentifiesDevelopmentBinaryPrecedenceWithoutExecution(t *testing.T) {
	repo := t.TempDir()
	if _, err := Install(KindClaudeCode, repo, false); err != nil {
		t.Fatal(err)
	}
	stable, supported := stableWrapperTargetName(runtime.GOOS, runtime.GOARCH)
	if !supported {
		t.Skip("host has no stable wrapper target")
	}
	writeWrapperStatusCandidate(t, filepath.Join(repo, "tools/reconc/dist", stable), "dev+stable")
	development := filepath.Join(repo, "tools/reconc/.build/bin/reconc")
	writeWrapperStatusCandidate(t, development, "dev+older")
	inspection := inspectStatusWrapperWithBinary(repo)
	if inspection.binary == nil || inspection.binary.Path != development || inspection.binary.Source != "development" ||
		inspection.binary.Version != "dev+older" || inspection.binary.Error != "" {
		t.Fatalf("selected binary hidden: %+v", inspection.binary)
	}
	if _, err := os.Stat(development + ".executed"); !os.IsNotExist(err) {
		t.Fatalf("status executed its candidate: %v", err)
	}
}

func writeWrapperStatusCandidate(t *testing.T, path, version string) {
	t.Helper()
	marker, err := buildprovenance.FormatMarker(buildprovenance.Provenance{
		Version: version, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, SourceDigest: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\n# " + marker + "\ntouch \"$0.executed\"\n"
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestWrapperStatusStopsOnAmbiguousRepositoryBinaries(t *testing.T) {
	repo := t.TempDir()
	stable, supported := stableWrapperTargetName(runtime.GOOS, runtime.GOARCH)
	if !supported {
		t.Skip("host has no stable wrapper target")
	}
	for _, revision := range []string{"first", "second"} {
		name := strings.Replace(stable, "reconc-", "reconc-"+revision+"-", 1)
		writeWrapperStatusCandidate(t, filepath.Join(repo, "tools/reconc/dist", name), "dev+"+revision)
	}
	status := inspectWrapperBinary(repo)
	if status.Path != "" || !strings.Contains(status.Error, "ambiguous") {
		t.Fatalf("ambiguous binaries silently selected a fallback: %+v", status)
	}
}

func TestWrapperStatusMatchesResolverForLinkedDistributionDirectory(t *testing.T) {
	repo := t.TempDir()
	stable, supported := stableWrapperTargetName(runtime.GOOS, runtime.GOARCH)
	if !supported {
		t.Skip("host has no stable wrapper target")
	}
	distribution := t.TempDir()
	name := strings.Replace(stable, "reconc-", "reconc-selected-", 1)
	writeWrapperStatusCandidate(t, filepath.Join(distribution, name), "dev+selected")
	if err := os.Symlink(distribution, filepath.Join(repo, "dist")); err != nil {
		t.Fatal(err)
	}
	status := inspectWrapperBinary(repo)
	if status.Path != filepath.Join(repo, "dist", name) || status.Error != "" {
		t.Fatalf("status diverged from the shell resolver: %+v", status)
	}
}

func TestStatusDegradesWhenTheWrapperCannotRunAReconcBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	repo := t.TempDir()
	if _, err := Install(KindClaudeCode, repo, false); err != nil {
		t.Fatal(err)
	}
	stable, supported := stableWrapperTargetName(runtime.GOOS, runtime.GOARCH)
	if !supported {
		t.Skip("host has no stable wrapper target")
	}
	state := func(t *testing.T, all bool) PlatformStatus {
		t.Helper()
		if !all {
			report, err := InspectPlatform(repo, KindClaudeCode)
			if err != nil {
				t.Fatal(err)
			}
			return report
		}
		reports, err := InspectPlatforms(repo)
		if err != nil {
			t.Fatal(err)
		}
		for _, report := range reports {
			if report.Kind == KindClaudeCode {
				return report
			}
		}
		t.Fatal("claude-code status missing")
		return PlatformStatus{}
	}
	for _, all := range []bool{true, false} {
		if report := state(t, all); report.State != StateDegraded || report.Configured || !strings.Contains(report.Detail, "no runnable Reconc binary") || report.Remediation == "" {
			t.Fatalf("all=%v: status without a binary = %s configured=%v detail=%q remediation=%q", all, report.State, report.Configured, report.Detail, report.Remediation)
		}
	}
	foreignOS := "linux"
	if runtime.GOOS == "linux" {
		foreignOS = "darwin"
	}
	marker, err := buildprovenance.FormatMarker(buildprovenance.Provenance{
		Version: "dev+foreign", GOOS: foreignOS, GOARCH: runtime.GOARCH, SourceDigest: strings.Repeat("b", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, "tools/reconc/dist", stable)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n# "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if report := state(t, true); report.State != StateDegraded || !strings.Contains(report.Detail, "targets "+foreignOS) {
		t.Fatalf("status with a foreign-platform binary = %s detail=%q", report.State, report.Detail)
	}
	writeWrapperStatusCandidate(t, path, "dev+host")
	for _, all := range []bool{true, false} {
		if report := state(t, all); report.State != StateConfigured {
			t.Fatalf("all=%v: status with a runnable binary = %s detail=%q", all, report.State, report.Detail)
		}
	}
}
