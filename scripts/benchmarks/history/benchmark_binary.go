package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedexec"
)

const maxBenchmarkBinaryBytes = 512 << 20

type benchmarkBinary struct {
	path      string
	directory string
	sha256    string
	goRoot    string
}

func buildBenchmarkBinary(ctx context.Context, root, goBinary, packageName, output string) (benchmarkBinary, error) {
	command := exec.CommandContext(ctx, goBinary, "env", "GOROOT")
	command.Dir = root
	command.WaitDelay = time.Second
	goRoot, err := boundedexec.Output(command, 8<<10)
	if err != nil {
		return benchmarkBinary{}, fmt.Errorf("inspect benchmark toolchain: %w", errors.Join(err, ctx.Err()))
	}
	args := []string{"test", "-c", "-o", output}
	// The historical CLI transport workload resolves its source via runtime.Caller.
	// Preserve that workload unchanged; every other package can omit source paths.
	if packageName != "./internal/cli" {
		args = append(args, "-trimpath")
	} else {
		args = append(args, "-trimpath=false")
	}
	command = exec.CommandContext(ctx, goBinary, append(args, packageName)...)
	command.Dir = root
	command.WaitDelay = time.Second
	if output, err := boundedexec.CombinedOutput(command, maxBenchmarkOutput); err != nil {
		return benchmarkBinary{}, fmt.Errorf("build benchmark package %s: %w: %s", packageName, errors.Join(err, ctx.Err()), output)
	}
	digest, err := benchmarkBinarySHA256(output)
	if err != nil {
		return benchmarkBinary{}, err
	}
	return benchmarkBinary{path: output, directory: filepath.Join(root, packageName), sha256: digest, goRoot: strings.TrimSpace(string(goRoot))}, nil
}

func buildCPUSentinel(ctx context.Context, goBinary, directory string) (benchmarkBinary, error) {
	sourceRoot := filepath.Join(directory, "sentinel")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		return benchmarkBinary{}, err
	}
	for name, body := range map[string]string{
		"go.mod": "module reconc.benchmark/sentinel\n\ngo 1.26\n", "sentinel_test.go": cpuSentinelSource,
	} {
		if err := os.WriteFile(filepath.Join(sourceRoot, name), []byte(body), 0o600); err != nil {
			return benchmarkBinary{}, fmt.Errorf("write CPU sentinel source: %w", err)
		}
	}
	return buildBenchmarkBinary(ctx, sourceRoot, goBinary, ".", filepath.Join(directory, "sentinel.test.exe"))
}

func benchmarkBinarySHA256(path string) (digest string, err error) {
	before, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !before.Mode().IsRegular() || before.Size() <= 0 || before.Size() > maxBenchmarkBinaryBytes {
		return "", errors.New("benchmark binary must be a nonempty bounded regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || !os.SameFile(before, info) || info.Size() != before.Size() {
		return "", errors.New("benchmark binary changed while opening")
	}
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, maxBenchmarkBinaryBytes+1))
	if err != nil || count != info.Size() {
		return "", fmt.Errorf("hash benchmark binary: %w", errors.Join(err, errors.New("binary size changed while hashing")))
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func (binary benchmarkBinary) command(ctx context.Context, args ...string) *exec.Cmd {
	command := exec.CommandContext(ctx, binary.path, args...)
	command.Dir = binary.directory
	command.WaitDelay = time.Second
	command.Env = append(os.Environ(), "PATH="+filepath.Join(binary.goRoot, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	return command
}
