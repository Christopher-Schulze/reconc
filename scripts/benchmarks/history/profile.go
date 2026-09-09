package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/atomicfile"
	"reconc.dev/reconc/internal/boundedexec"
	"reconc.dev/reconc/internal/boundedio"
)

const profileManifestName = "manifest.json"

const maxProfileDirectoryEntries = 256

type profileOptions struct {
	directory string
	groups    map[string]bool
}

type profileKind struct {
	name      string
	flag      string
	extension string
}

var profileKinds = []profileKind{
	{name: "cpu", flag: "cpuprofile", extension: ".cpu.pprof"},
	{name: "heap", flag: "memprofile", extension: ".heap.pprof"},
	{name: "blocking", flag: "blockprofile", extension: ".block.pprof"},
	{name: "mutex", flag: "mutexprofile", extension: ".mutex.pprof"},
	{name: "trace", flag: "trace", extension: ".trace"},
}

func profileOptionsFromFlags(root, directory, groups string) (*profileOptions, error) {
	if directory == "" && groups == "" {
		return nil, nil
	}
	if directory == "" {
		return nil, fmt.Errorf("--profile-groups requires --profile-dir")
	}
	if groups == "" {
		return nil, fmt.Errorf("--profile-dir requires --profile-groups to bound profiling")
	}
	selected, err := parseProfileGroups(groups)
	if err != nil {
		return nil, err
	}
	if !filepath.IsAbs(directory) {
		directory = filepath.Join(root, directory)
	}
	return &profileOptions{directory: filepath.Clean(directory), groups: selected}, nil
}

func parseProfileGroups(value string) (map[string]bool, error) {
	selected := make(map[string]bool)
	known := make(map[string]bool, len(benchmarkSuite))
	for _, spec := range benchmarkSuite {
		known[spec.Name] = true
	}
	for _, raw := range strings.Split(value, ",") {
		name := strings.TrimSpace(raw)
		if name == "" || !known[name] {
			return nil, fmt.Errorf("unknown benchmark profile group %q; choose one of %s", name, strings.Join(profileGroupNames(), ", "))
		}
		if selected[name] {
			return nil, fmt.Errorf("benchmark profile group %q was selected more than once", name)
		}
		selected[name] = true
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("at least one benchmark profile group is required")
	}
	return selected, nil
}

func profileGroupNames() []string {
	names := make([]string, 0, len(benchmarkSuite))
	for _, spec := range benchmarkSuite {
		names = append(names, spec.Name)
	}
	sort.Strings(names)
	return names
}

func runProfiles(root, goBinary string, parameters Parameters, environment Environment, options profileOptions) error {
	if err := prepareProfileDirectory(options.directory); err != nil {
		return err
	}
	manifest := ProfileManifest{
		FormatVersion: profileFormat,
		Environment:   environment,
		Parameters:    parameters,
		Workloads:     make([]ProfileWorkload, 0, len(options.groups)),
	}
	for _, spec := range benchmarkSuite {
		if !options.groups[spec.Name] {
			continue
		}
		names := append([]string{spec.Calibration}, spec.Targets...)
		patterns := benchmarkPatterns(names)
		for patternIndex, pattern := range patterns {
			artifacts, err := runProfileSample(root, goBinary, spec, pattern, patternIndex, parameters, options.directory)
			if err != nil {
				return err
			}
			manifest.Workloads = append(manifest.Workloads, ProfileWorkload{
				Group: spec.Name, Package: spec.Package, Pattern: pattern, Profiles: artifacts,
			})
		}
	}
	if err := validateProfileManifest(manifest); err != nil {
		return err
	}
	body, err := encodeContract(manifest)
	if err != nil {
		return err
	}
	manifestPath := filepath.Join(options.directory, profileManifestName)
	if _, err := atomicfile.WriteIfChanged(manifestPath, body, 0o600); err != nil {
		return fmt.Errorf("publish benchmark profile manifest: %w", err)
	}
	return nil
}

func prepareProfileDirectory(directory string) error {
	info, err := os.Stat(directory)
	if os.IsNotExist(err) {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create benchmark profile directory: %w", err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect benchmark profile directory: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("benchmark profile path %s is not a directory", directory)
	}
	entries, err := boundedio.ReadDirNoSymlink(directory, maxProfileDirectoryEntries)
	if err != nil {
		return fmt.Errorf("read benchmark profile directory: %w", err)
	}
	if len(entries) != 0 {
		return fmt.Errorf("benchmark profile directory %s must be empty", directory)
	}
	return nil
}

func runProfileSample(root, goBinary string, spec groupSpec, pattern string, patternIndex int, parameters Parameters, directory string) ([]ProfileArtifact, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	stem := fmt.Sprintf("%02d-%s", patternIndex+1, spec.Name)
	args := []string{"test", "-json", "-run", "^$", "-bench", pattern, "-benchmem", "-count", "1", "-benchtime", parameters.Benchtime, "-cpu", fmt.Sprint(parameters.CPU), "-timeout", "5m", spec.Package}
	paths := make([]string, 0, len(profileKinds))
	for _, kind := range profileKinds {
		profilePath := filepath.Join(directory, stem+kind.extension)
		paths = append(paths, profilePath)
		args = append(args, "-"+kind.flag+"="+profilePath)
	}
	args = append(args, "-blockprofilerate=1", "-mutexprofilefraction=1")
	command := exec.CommandContext(ctx, goBinary, args...)
	command.Dir = root
	output, err := boundedexec.Output(command, maxBenchmarkOutput)
	if ctx.Err() != nil {
		return nil, fmt.Errorf("profile workload %s timed out: %w", spec.Name, ctx.Err())
	}
	if err != nil {
		return nil, fmt.Errorf("profile workload %s failed: %w", spec.Name, err)
	}
	outputPath := filepath.Join(directory, stem+".bench.json")
	if err := os.WriteFile(outputPath, output, 0o600); err != nil {
		return nil, fmt.Errorf("write profile benchmark output: %w", err)
	}
	artifacts := make([]ProfileArtifact, 0, len(paths)+1)
	outputArtifact, err := profileArtifact(directory, outputPath, "benchmark-output")
	if err != nil {
		return nil, err
	}
	artifacts = append(artifacts, outputArtifact)
	for index, profilePath := range paths {
		artifact, err := profileArtifact(directory, profilePath, profileKinds[index].name)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, artifact)
	}
	return artifacts, nil
}

func profileArtifact(directory, filePath, kind string) (ProfileArtifact, error) {
	body, info, err := boundedio.ReadRegularFileSnapshot(filePath, maxProfileBytes)
	if err != nil {
		return ProfileArtifact{}, fmt.Errorf("read benchmark profile %s: %w", filePath, err)
	}
	if info.Size() <= 0 {
		return ProfileArtifact{}, fmt.Errorf("benchmark profile %s is empty", filePath)
	}
	digest := sha256.Sum256(body)
	relative, err := filepath.Rel(directory, filePath)
	if err != nil {
		return ProfileArtifact{}, fmt.Errorf("resolve benchmark profile path: %w", err)
	}
	return ProfileArtifact{
		Kind: kind, Path: filepath.ToSlash(relative), Bytes: info.Size(), SHA256: hex.EncodeToString(digest[:]),
	}, nil
}
