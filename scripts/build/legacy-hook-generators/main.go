// Command legacy-hook-generators records what the hook generators of
// published releases wrote, so hook installation and removal can recognize
// those artifacts as Reconc output after an upgrade. For each stable release
// tag in HEAD's history from firstRecordedRelease on it exports the tagged
// source, runs that release's generators, and stores only SHA-256 digests: of
// every artifact and, for JSON hosts, of every generated hook entry per event
// and of the owned top-level `reconc` value. With --check it fails when the
// registry differs from what those generators produce.
//
// Usage: go run ./scripts/build/legacy-hook-generators [--output FILE] [--check]
package main

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/gitexec"
	"reconc.dev/reconc/internal/hooks"
)

// dumpProgram runs inside an exported release tree and prints every
// generated artifact keyed by host kind.
const dumpProgram = `package main

import (
	"encoding/json"
	"os"

	"reconc.dev/reconc/internal/hooks"
)

func main() {
	artifacts := map[string]string{}
	for _, platform := range hooks.Platforms() {
		artifact, err := hooks.Generate(platform.Kind)
		if err != nil {
			continue
		}
		artifacts[platform.Kind] = artifact.Content
	}
	if err := json.NewEncoder(os.Stdout).Encode(artifacts); err != nil {
		os.Exit(1)
	}
}
`

const (
	maxArchiveBytes         = 256 << 20
	maxGeneratorOutputBytes = 64 << 20
	maxGitOutputBytes       = 1 << 20
	maxRegistryBytes        = 16 << 20
)

// firstRecordedRelease is the earliest release whose generators the dump
// program can run: `hooks.Platforms` and `hooks.Generate` exist from it on.
const firstRecordedRelease = "reconc-v0.7.0"

// renamedKinds maps a host kind an earlier release used to its current name;
// both name the same target file.
var renamedKinds = map[string]string{"copilot": hooks.KindGitHubCopilot}

var stableReleaseTag = regexp.MustCompile(`^reconc-v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

// Document is the committed registry. Digests are merged per host kind
// because recognition does not depend on which release wrote a value.
type Document struct {
	Description string                         `json:"description"`
	Releases    []string                       `json:"releases"`
	Artifacts   map[string][]string            `json:"artifacts"`
	Values      map[string]map[string][]string `json:"values"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "legacy-hook-generators:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("legacy-hook-generators", flag.ContinueOnError)
	output := flags.String("output", "internal/hooks/legacy_generators.json", "registry to write or check")
	check := flags.Bool("check", false, "fail when the registry differs from the release generators")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: legacy-hook-generators [--output FILE] [--check]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	tags, err := recordedReleaseTags(ctx, ".")
	if err != nil {
		return err
	}
	document := newDocument()
	for _, tag := range tags {
		artifacts, err := releaseArtifacts(ctx, tag)
		if err == nil {
			artifacts, err = currentKinds(artifacts)
		}
		if err != nil {
			return fmt.Errorf("%s: %w", tag, err)
		}
		document.Releases = append(document.Releases, strings.TrimPrefix(tag, "reconc-v"))
		addArtifacts(&document, artifacts)
	}
	body, err := encodeDocument(document)
	if err != nil {
		return err
	}
	if !*check {
		_, err = atomicfile.WriteIfChanged(*output, body, 0o644)
		return err
	}
	current, err := boundedio.ReadRegularFile(*output, maxRegistryBytes)
	if err != nil {
		return err
	}
	if !bytes.Equal(current, body) {
		return fmt.Errorf("%s differs from the generators of %s; regenerate it with go run ./scripts/build/legacy-hook-generators", *output, strings.Join(document.Releases, ", "))
	}
	return nil
}

// recordedReleaseTags lists the stable release tags in HEAD's history from
// firstRecordedRelease on, oldest first. A tag on HEAD itself names the
// release being built, whose generators are the current ones.
func recordedReleaseTags(ctx context.Context, root string) ([]string, error) {
	merged, err := gitLines(ctx, root, "tag", "--merged", "HEAD", "--sort=v:refname", "--list", "reconc-v*")
	if err != nil {
		return nil, err
	}
	atHead, err := gitLines(ctx, root, "tag", "--points-at", "HEAD", "--list", "reconc-v*")
	if err != nil {
		return nil, err
	}
	tags, started := []string{}, false
	for _, tag := range merged {
		started = started || tag == firstRecordedRelease
		if started && stableReleaseTag.MatchString(tag) && !slices.Contains(atHead, tag) {
			tags = append(tags, tag)
		}
	}
	if !started {
		return nil, fmt.Errorf("release tag %s is not in HEAD's history; fetch the release tags", firstRecordedRelease)
	}
	return tags, nil
}

func gitLines(ctx context.Context, root string, args ...string) ([]string, error) {
	output, err := boundedexec.Output(gitexec.CommandContext(ctx, root, nil, args...), maxGitOutputBytes)
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, exitDetail(err))
	}
	return strings.Fields(string(output)), nil
}

func exitDetail(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return strings.TrimSpace(string(exitErr.Stderr))
	}
	return ""
}

func releaseArtifacts(ctx context.Context, tag string) (map[string]string, error) {
	directory, err := os.MkdirTemp("", "reconc-legacy-generators-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	archive, err := boundedexec.Output(gitexec.CommandContext(ctx, ".", nil, "archive", "--format=tar", tag), maxArchiveBytes)
	if err != nil {
		return nil, fmt.Errorf("export source: %w: %s", err, exitDetail(err))
	}
	if err := extract(bytes.NewReader(archive), directory); err != nil {
		return nil, err
	}
	program := filepath.Join(directory, "internal", "zzlegacydump")
	if err := os.MkdirAll(program, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(program, "main.go"), []byte(dumpProgram), 0o644); err != nil {
		return nil, err
	}
	run := exec.CommandContext(ctx, "go", "run", "./internal/zzlegacydump")
	run.Dir = directory
	output, err := boundedexec.Output(run, maxGeneratorOutputBytes)
	if err != nil {
		return nil, fmt.Errorf("run release generators: %w: %s", err, exitDetail(err))
	}
	artifacts := map[string]string{}
	if err := json.Unmarshal(output, &artifacts); err != nil {
		return nil, fmt.Errorf("decode release generators: %w", err)
	}
	return artifacts, nil
}

func extract(archive io.Reader, directory string) error {
	reader := tar.NewReader(archive)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := filepath.Clean(filepath.FromSlash(header.Name))
		if filepath.IsAbs(name) || name == ".." || strings.HasPrefix(name, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive entry escapes the export: %s", header.Name)
		}
		target := filepath.Join(directory, name)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(file, reader)
			if err := errors.Join(copyErr, file.Close()); err != nil {
				return err
			}
		}
	}
}

// currentKinds files each artifact under the host kind that installs that
// target today. A kind the current generators do not know fails, so a future
// rename is mapped instead of silently never matching.
func currentKinds(artifacts map[string]string) (map[string]string, error) {
	known := map[string]bool{}
	for _, platform := range hooks.Platforms() {
		known[platform.Kind] = true
	}
	current := make(map[string]string, len(artifacts))
	for kind, content := range artifacts {
		if renamed, ok := renamedKinds[kind]; ok {
			kind = renamed
		}
		if !known[kind] {
			return nil, fmt.Errorf("release host kind %q is unknown to the current generators; map it in renamedKinds", kind)
		}
		if _, duplicate := current[kind]; duplicate {
			return nil, fmt.Errorf("release generated host kind %q twice", kind)
		}
		current[kind] = content
	}
	return current, nil
}

func newDocument() Document {
	return Document{
		Description: "SHA-256 digests of hook artifacts written by published Reconc releases; regenerate with scripts/build/legacy-hook-generators.",
		Releases:    []string{},
		Artifacts:   map[string][]string{},
		Values:      map[string]map[string][]string{},
	}
}

// addArtifacts records the digests of one release's generator output.
func addArtifacts(document *Document, artifacts map[string]string) {
	for kind, content := range artifacts {
		document.Artifacts[kind] = append(document.Artifacts[kind], digest([]byte(content)))
		var generated map[string]interface{}
		if json.Unmarshal([]byte(content), &generated) != nil {
			continue
		}
		if document.Values[kind] == nil {
			document.Values[kind] = map[string][]string{}
		}
		values := document.Values[kind]
		values[""] = append(values[""], valueDigest(generated))
		if owned, ok := generated["reconc"]; ok {
			values["reconc"] = append(values["reconc"], valueDigest(owned))
		}
		hooks, _ := generated["hooks"].(map[string]interface{})
		if events, nested := hooks["events"].(map[string]interface{}); nested {
			hooks = events
		}
		for event, raw := range hooks {
			entries, _ := raw.([]interface{})
			for _, entry := range entries {
				values[event] = append(values[event], valueDigest(entry))
			}
		}
	}
}

// encodeDocument sorts and deduplicates every digest list so equal
// generator output always produces the same bytes.
func encodeDocument(document Document) ([]byte, error) {
	for kind, digests := range document.Artifacts {
		document.Artifacts[kind] = sortedUnique(digests)
	}
	for _, values := range document.Values {
		for key, digests := range values {
			values[key] = sortedUnique(digests)
		}
	}
	body, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}

func sortedUnique(values []string) []string {
	sorted := slices.Clone(values)
	sort.Strings(sorted)
	return slices.Compact(sorted)
}

func valueDigest(value interface{}) string {
	canonical, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return digest(canonical)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
