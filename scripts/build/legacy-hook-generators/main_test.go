package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"reconc.dev/reconc/internal/hooks"
)

type archiveEntry struct {
	name string
	kind byte
	body string
}

func testArchive(t *testing.T, entries ...archiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range entries {
		if err := writer.WriteHeader(&tar.Header{Name: entry.name, Typeflag: entry.kind, Size: int64(len(entry.body)), Mode: 0o755}); err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestExtractRejectsEscapingPaths(t *testing.T) {
	for _, name := range []string{"../escaped", "nested/../../escaped", filepath.Join(t.TempDir(), "absolute")} {
		t.Run(name, func(t *testing.T) {
			archive := testArchive(t, archiveEntry{name: name, kind: tar.TypeReg, body: "outside"})
			if err := extract(bytes.NewReader(archive), t.TempDir()); err == nil {
				t.Fatalf("accepted escaping archive entry %q", name)
			}
		})
	}
}

func TestExtractRefusesSymlinkedParents(t *testing.T) {
	for _, test := range []struct {
		name string
		kind byte
		body string
	}{
		{name: "file", kind: tar.TypeReg, body: "outside"},
		{name: "directory", kind: tar.TypeDir},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory, outside := t.TempDir(), t.TempDir()
			if err := os.Symlink(outside, filepath.Join(directory, "link")); err != nil {
				t.Fatal(err)
			}
			archive := testArchive(t, archiveEntry{name: "link/escaped", kind: test.kind, body: test.body})
			if err := extract(bytes.NewReader(archive), directory); err == nil {
				t.Error("extracted through a parent symlink outside the destination")
			}
			if _, err := os.Lstat(filepath.Join(outside, "escaped")); !os.IsNotExist(err) {
				t.Fatalf("archive touched a path outside the destination: %v", err)
			}
		})
	}
}

func TestExtractPreservesRegularFilesAndRejectsOverwrite(t *testing.T) {
	directory := t.TempDir()
	archive := testArchive(t,
		archiveEntry{name: "nested", kind: tar.TypeDir},
		archiveEntry{name: "nested/file.go", kind: tar.TypeReg, body: "package fixture\n"},
		archiveEntry{name: "empty", kind: tar.TypeReg},
	)
	if err := extract(bytes.NewReader(archive), directory); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, body string }{{"nested/file.go", "package fixture\n"}, {"empty", ""}} {
		body, err := os.ReadFile(filepath.Join(directory, test.name))
		if err != nil || string(body) != test.body {
			t.Fatalf("extracted %s = %q, %v; want %q", test.name, body, err, test.body)
		}
	}
	if err := extract(bytes.NewReader(archive), directory); err == nil {
		t.Fatal("extraction replaced an existing regular file")
	}
	body, err := os.ReadFile(filepath.Join(directory, "nested/file.go"))
	if err != nil || string(body) != "package fixture\n" {
		t.Fatalf("existing source changed after refused extraction: %q, %v", body, err)
	}
}

func encodedDocument(t *testing.T, releases ...map[string]string) Document {
	t.Helper()
	document := newDocument()
	for _, artifacts := range releases {
		addArtifacts(&document, artifacts)
	}
	body, err := encodeDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	decoded := Document{}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	return decoded
}

func TestAddArtifactsIndexesEveryOwnedValue(t *testing.T) {
	document := encodedDocument(t, map[string]string{
		"claude-code": `{"hooks":{"Stop":[{"hooks":[{"command":"sh"}]}],"PreToolUse":[{"matcher":"Bash"},{"matcher":"mcp__.*"}]}}`,
		"zcode":       `{"hooks":{"enabled":true,"events":{"Stop":[{"command":"sh"}]}}}`,
		"antigravity": `{"reconc":{"Stop":[{"command":"x"}]}}`,
		"opencode":    "// plugin source\n",
	})
	for kind, keys := range map[string][]string{
		"claude-code": {"", "Stop", "PreToolUse"},
		"zcode":       {"", "Stop"},
		"antigravity": {"", "reconc"},
	} {
		for _, key := range keys {
			if len(document.Values[kind][key]) == 0 {
				t.Errorf("%s value %q was not indexed: %v", kind, key, document.Values[kind])
			}
		}
	}
	if got := len(document.Values["claude-code"]["PreToolUse"]); got != 2 {
		t.Errorf("PreToolUse entries indexed = %d, want 2", got)
	}
	if _, ok := document.Values["opencode"]; ok || len(document.Artifacts["opencode"]) != 1 {
		t.Errorf("a non-JSON artifact must have only an artifact digest: %+v", document)
	}
	entry, err := json.Marshal(map[string]interface{}{"matcher": "Bash"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(document.Values["claude-code"]["PreToolUse"], digest(entry)) {
		t.Error("entry digests are not canonical JSON digests")
	}
}

func TestEncodedDigestsAreCanonicalAndDeduplicated(t *testing.T) {
	first := `{"hooks":{"Stop":[{"command":"sh"}],"PreToolUse":[{"matcher":"Bash"}]}}`
	reordered := `{"hooks":{"PreToolUse":[{"matcher":"Bash"}],"Stop":[{"command":"sh"}]}}`
	once := encodedDocument(t, map[string]string{"claude-code": first})
	twice := encodedDocument(t, map[string]string{"claude-code": first}, map[string]string{"claude-code": reordered})
	for _, key := range []string{"", "Stop", "PreToolUse"} {
		if !slices.Equal(once.Values["claude-code"][key], twice.Values["claude-code"][key]) {
			t.Errorf("value %q digests depend on source key order or repeat: %v", key, twice.Values["claude-code"][key])
		}
	}
	if len(twice.Artifacts["claude-code"]) != 2 {
		t.Errorf("artifact digests must identify the exact bytes: %v", twice.Artifacts["claude-code"])
	}
	if !slices.IsSorted(twice.Artifacts["claude-code"]) {
		t.Errorf("artifact digests are not sorted: %v", twice.Artifacts["claude-code"])
	}
}

func TestCurrentKindsMapsRenamedHostsAndRejectsUnknownOnes(t *testing.T) {
	current, err := currentKinds(map[string]string{"copilot": "old", "codex": "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if current[hooks.KindGitHubCopilot] != "old" || current["codex"] != "codex" || len(current) != 2 {
		t.Fatalf("current kinds = %v", current)
	}
	if _, err := currentKinds(map[string]string{"retired-host": "x"}); err == nil {
		t.Fatal("an unknown release host kind must fail")
	}
	if _, err := currentKinds(map[string]string{"copilot": "old", hooks.KindGitHubCopilot: "new"}); err == nil {
		t.Fatal("two release kinds for one current host must fail")
	}
}

func TestRecordedReleaseTagsFollowHeadHistoryFromTheFirstRecordedRelease(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", append([]string{"-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
		}
	}
	commit := func(name string, tags ...string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		git("add", name)
		git("commit", "-q", "-m", name)
		for _, tag := range tags {
			git("tag", tag)
		}
	}
	git("init", "-q", "-b", "main")
	commit("old", "reconc-v0.6.0")
	if _, err := recordedReleaseTags(context.Background(), root); err == nil {
		t.Fatal("a history without the first recorded release must fail")
	}
	commit("first", firstRecordedRelease)
	commit("candidate", "reconc-v0.10.0-rc1")
	commit("later", "reconc-v0.10.0")
	commit("fix", "reconc-v0.9.9")
	git("checkout", "-q", "-b", "side", "HEAD~1")
	commit("unmerged", "reconc-v0.11.0")
	git("checkout", "-q", "main")
	commit("release", "reconc-v0.12.0")
	tags, err := recordedReleaseTags(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{firstRecordedRelease, "reconc-v0.9.9", "reconc-v0.10.0"}; !slices.Equal(tags, want) {
		t.Fatalf("recorded release tags = %v, want %v", tags, want)
	}
}
