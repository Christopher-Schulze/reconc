package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/usercli"
)

const (
	manifestName     = "release-manifest.json"
	checksumsName    = "SHA256SUMS"
	manifestFormat   = "reconc.release/v1"
	repository       = "Christopher-Schulze/reconc"
	maxAssets        = 128
	maxAssetBytes    = 256 << 20
	maxManifestBytes = 1 << 20
)

var versionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "release-manifest:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("release-manifest", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var directory string
	var version string
	var verify bool
	flags.StringVar(&directory, "output-dir", "dist", "release output directory")
	flags.StringVar(&version, "version", "", "release version")
	flags.BoolVar(&verify, "verify", false, "verify the existing manifest without mutation")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || !versionPattern.MatchString(version) {
		return errors.New("--version must be supported semantic versioning")
	}
	manifest, err := buildManifest(directory, version)
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	path := filepath.Join(directory, manifestName)
	if verify {
		current, err := boundedio.ReadRegularFile(path, maxManifestBytes)
		if err != nil {
			return fmt.Errorf("read release manifest: %w", err)
		}
		if !bytes.Equal(current, body) {
			return errors.New("release manifest does not match the current release inventory")
		}
		fmt.Fprintf(stdout, "verified: %s\n", path)
		return nil
	}
	changed, err := atomicfile.WriteIfChanged(path, body, 0o644)
	if err != nil {
		return err
	}
	action := "unchanged"
	if changed {
		action = "written"
	}
	fmt.Fprintf(stdout, "%s: %s\n", action, path)
	return nil
}

func buildManifest(directory string, version string) (usercli.ReleaseManifest, error) {
	info, err := os.Lstat(directory)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return usercli.ReleaseManifest{}, fmt.Errorf("output directory must be a real directory: %s", directory)
	}
	entries, err := boundedio.ReadDirNoSymlink(directory, maxAssets+2)
	if err != nil {
		return usercli.ReleaseManifest{}, err
	}
	assets := make([]usercli.ReleaseAsset, 0, len(entries))
	for _, entry := range entries {
		if entry.Name() == manifestName || entry.Name() == checksumsName {
			continue
		}
		if entry.Name() == "" || filepath.Base(entry.Name()) != entry.Name() ||
			strings.HasPrefix(entry.Name(), ".") || !entry.Type().IsRegular() {
			return usercli.ReleaseManifest{}, fmt.Errorf("release entry must be a public regular flat file: %s", entry.Name())
		}
		path := filepath.Join(directory, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return usercli.ReleaseManifest{}, err
		}
		if info.Size() <= 0 || info.Size() > maxAssetBytes {
			return usercli.ReleaseManifest{}, fmt.Errorf("release asset size is outside 1..%d bytes: %s", maxAssetBytes, entry.Name())
		}
		digest, size, err := hashReleaseAsset(path, info)
		if err != nil {
			return usercli.ReleaseManifest{}, err
		}
		assets = append(assets, usercli.ReleaseAsset{
			Name: entry.Name(), SHA256: digest, Size: size,
		})
	}
	if len(assets) == 0 || len(assets) > maxAssets {
		return usercli.ReleaseManifest{}, fmt.Errorf("release inventory must contain 1 to %d assets", maxAssets)
	}
	sort.Slice(assets, func(left, right int) bool {
		return assets[left].Name < assets[right].Name
	})
	return usercli.ReleaseManifest{
		FormatVersion: manifestFormat, Repository: repository,
		Tag: "reconc-v" + version, Version: version,
		Prerelease: strings.Contains(version, "-"), Assets: assets,
	}, nil
}

func hashReleaseAsset(path string, before os.FileInfo) (string, int64, error) {
	hash := sha256.New()
	var written int64
	err := boundedio.WithRegularFileSnapshot(path, maxAssetBytes, func(file *os.File, opened os.FileInfo) error {
		if !os.SameFile(before, opened) || before.Mode() != opened.Mode() ||
			before.Size() != opened.Size() || !before.ModTime().Equal(opened.ModTime()) {
			return fmt.Errorf("release asset changed before hashing: %s", path)
		}
		var copyErr error
		written, copyErr = io.Copy(hash, io.LimitReader(file, maxAssetBytes+1))
		if copyErr != nil {
			return copyErr
		}
		if written <= 0 || written > maxAssetBytes || written != opened.Size() {
			return fmt.Errorf("release asset size changed while hashing: %s", path)
		}
		return nil
	})
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}
