package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing/fstest"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/harnesspack"
)

const (
	advancedPackName    = "advanced"
	advancedPackVersion = "1.0.0"
	advancedPackPrefix  = "tools/reconc/harness/template"
)

var advancedPackCapabilities = []string{
	"advanced-audits",
	"architecture-scaffolds",
	"hook-scaffolds",
	"task-utilities",
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "harness-pack:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("harness-pack", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	sourcePath := flags.String("source", "harness/template", "canonical advanced harness source")
	manifestPath := flags.String("manifest", "harness/advanced-pack-manifest.json", "committed pack manifest")
	archivePath := flags.String("archive", "harness/advanced-pack.zip", "committed deterministic release archive")
	write := flags.Bool("write", false, "atomically publish the generated manifest")
	check := flags.Bool("check", false, "require the committed manifest to match canonical sources")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || (*write && *check) || (!*write && !*check) {
		return errors.New("usage: harness-pack (--write|--check) [--source DIR] [--manifest FILE] [--archive FILE]")
	}
	source, err := canonicalSource(*sourcePath)
	if err != nil {
		return err
	}
	manifest, err := harnesspack.Build(source, harnesspack.BuildOptions{
		Name: advancedPackName, Version: advancedPackVersion,
		ProductCompatibility: harnesspack.Compatibility{
			Minimum: "0.9.0", MaximumExclusive: "1.0.0",
		},
		Capabilities: advancedPackCapabilities,
		TargetPrefix: advancedPackPrefix,
		ExcludedPaths: map[string]bool{
			"coverage.html": true,
			"coverage.out":  true,
		},
	})
	if err != nil {
		return err
	}
	body, err := harnesspack.Encode(manifest)
	if err != nil {
		return err
	}
	archive, err := buildArchive(body, source, manifest)
	if err != nil {
		return err
	}
	switch {
	case *write:
		if _, err := atomicfile.WriteIfChanged(*manifestPath, body, 0o644); err != nil {
			return fmt.Errorf("write harness pack manifest: %w", err)
		}
		if _, err := atomicfile.WriteIfChanged(*archivePath, archive, 0o644); err != nil {
			return fmt.Errorf("write harness pack archive: %w", err)
		}
	case *check:
		current, err := boundedio.ReadRegularFile(*manifestPath, harnesspack.MaxManifestBytes)
		if err != nil {
			return fmt.Errorf("read harness pack manifest: %w", err)
		}
		if !bytes.Equal(current, body) {
			return errors.New("harness pack manifest is stale; run `go run ./scripts/build/harness-pack --write`")
		}
		currentArchive, err := boundedio.ReadRegularFile(*archivePath, harnesspack.MaxArchiveBytes)
		if err != nil {
			return fmt.Errorf("read harness pack archive: %w", err)
		}
		if !bytes.Equal(currentArchive, archive) {
			return errors.New("harness pack archive is stale; run `go run ./scripts/build/harness-pack --write`")
		}
	}
	fmt.Fprintf(stdout, "harness-pack: %s@%s files=%d bytes=%d digest=%s\n",
		manifest.Name, manifest.Version, len(manifest.Files), manifest.TotalBytes, manifest.Digest)
	return nil
}

func canonicalSource(sourcePath string) (fs.FS, error) {
	absoluteSource, err := filepath.Abs(sourcePath)
	if err != nil {
		return nil, fmt.Errorf("resolve harness pack source: %w", err)
	}
	absoluteSource, err = filepath.EvalSymlinks(absoluteSource)
	if err != nil {
		return nil, fmt.Errorf("resolve harness pack source identity: %w", err)
	}
	root, tracked, err := trackedSourceInventory(absoluteSource)
	if err != nil {
		return nil, err
	}
	if !tracked {
		return os.DirFS(absoluteSource), nil
	}
	source := fstest.MapFS{}
	sourcePrefix, err := filepath.Rel(root, absoluteSource)
	if err != nil {
		return nil, fmt.Errorf("resolve tracked harness source: %w", err)
	}
	sourcePrefix = filepath.ToSlash(sourcePrefix)
	output, err := gitOutput(root, "ls-files", "--stage", "-z", "--", sourcePrefix)
	if err != nil {
		return nil, err
	}
	prefix := strings.TrimSuffix(sourcePrefix, "/") + "/"
	for _, record := range bytes.Split(output, []byte{0}) {
		if len(record) == 0 {
			continue
		}
		tab := bytes.IndexByte(record, '\t')
		if tab < 0 {
			return nil, errors.New("tracked harness source inventory is malformed")
		}
		fields := strings.Fields(string(record[:tab]))
		if len(fields) != 3 || fields[2] != "0" {
			return nil, errors.New("tracked harness source inventory is malformed")
		}
		mode := fs.FileMode(0)
		switch fields[0] {
		case "100644":
			mode = 0o644
		case "100755":
			mode = 0o755
		default:
			return nil, fmt.Errorf("tracked harness source has unsupported Git mode %s", fields[0])
		}
		repositoryPath := string(record[tab+1:])
		if !strings.HasPrefix(repositoryPath, prefix) {
			return nil, fmt.Errorf("tracked harness source path escaped its prefix: %s", repositoryPath)
		}
		relative := strings.TrimPrefix(repositoryPath, prefix)
		info, err := os.Lstat(filepath.Join(root, filepath.FromSlash(repositoryPath)))
		if err != nil {
			return nil, fmt.Errorf("inspect tracked harness source %s: %w", repositoryPath, err)
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
			info.Size() > harnesspack.MaxFileBytes {
			return nil, fmt.Errorf("tracked harness source is not a bounded regular file: %s", repositoryPath)
		}
		body, err := boundedio.ReadRegularFile(filepath.Join(root, filepath.FromSlash(repositoryPath)), harnesspack.MaxFileBytes)
		if err != nil {
			return nil, fmt.Errorf("read tracked harness source %s: %w", repositoryPath, err)
		}
		source[relative] = &fstest.MapFile{Data: body, Mode: mode}
	}
	return source, nil
}

func trackedSourceInventory(source string) (string, bool, error) {
	tracked, err := hasGitAncestor(source)
	if err != nil || !tracked {
		return "", tracked, err
	}
	output, err := gitOutput(source, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false, err
	}
	root := strings.TrimSpace(string(output))
	if root == "" {
		return "", false, errors.New("git returned an empty repository root for the harness source")
	}
	relative, err := filepath.Rel(root, source)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", false, fmt.Errorf("harness source is outside its git repository: %s", source)
	}
	return root, true, nil
}

func hasGitAncestor(source string) (bool, error) {
	current := source
	for {
		if _, err := os.Lstat(filepath.Join(current, ".git")); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, fmt.Errorf("inspect git repository marker: %w", err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
}

func gitOutput(directory string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, args...)...)
	output, err := boundedexec.Output(command, harnesspack.MaxManifestBytes)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("git command timed out: %w", ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("run git command: %w", err)
	}
	if len(output) > harnesspack.MaxManifestBytes {
		return nil, fmt.Errorf("git source inventory exceeds %d bytes", harnesspack.MaxManifestBytes)
	}
	return output, nil
}

func buildArchive(manifestBody []byte, source fs.FS, manifest *harnesspack.Manifest) ([]byte, error) {
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	if err := writeArchiveEntry(writer, "manifest.json", 0o644, manifestBody); err != nil {
		return nil, abortArchive(writer, err)
	}
	for _, file := range manifest.Files {
		relative := file.Path[len(advancedPackPrefix)+1:]
		content, err := fs.ReadFile(source, relative)
		if err != nil {
			return nil, abortArchive(writer, fmt.Errorf("read archive source %s: %w", relative, err))
		}
		if err := harnesspack.VerifyFile(file, content); err != nil {
			return nil, abortArchive(writer, err)
		}
		if err := writeArchiveEntry(writer, file.Path, fs.FileMode(file.Mode), content); err != nil {
			return nil, abortArchive(writer, err)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("close harness pack archive: %w", err)
	}
	return body.Bytes(), nil
}

func writeArchiveEntry(writer *zip.Writer, name string, mode fs.FileMode, body []byte) error {
	header := &zip.FileHeader{Name: filepath.ToSlash(name), Method: zip.Deflate}
	header.Modified = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
	header.SetMode(mode)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("create harness pack archive entry %s: %w", name, err)
	}
	if _, err := entry.Write(body); err != nil {
		return fmt.Errorf("write harness pack archive entry %s: %w", name, err)
	}
	return nil
}

func abortArchive(writer *zip.Writer, cause error) error {
	if closeErr := writer.Close(); closeErr != nil {
		return errors.Join(cause, fmt.Errorf("close harness pack archive: %w", closeErr))
	}
	return cause
}
