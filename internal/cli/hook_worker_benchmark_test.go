package cli

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"reconc.dev/reconc/internal/runtime/agentsession"
)

var hookWorkerRootBenchmarkSink agentsession.ResolvedRepoRoot

func BenchmarkHookWorkerRootCache(b *testing.B) {
	b.Run("single-repository-hit", func(b *testing.B) {
		repo := b.TempDir()
		cache := hookWorkerRootCache{}
		if _, err := cache.resolve(repo); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			root, err := cache.resolve(repo)
			if err != nil {
				b.Fatal(err)
			}
			hookWorkerRootBenchmarkSink = root
		}
	})

	b.Run("hostile-cardinality", func(b *testing.B) {
		paths := make([]string, hookWorkerRootCacheLimit+1)
		for index := range paths {
			paths[index] = b.TempDir()
		}
		cache := hookWorkerRootCache{}
		index := 0
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			root, err := cache.resolve(paths[index%len(paths)])
			if err != nil {
				b.Fatal(err)
			}
			hookWorkerRootBenchmarkSink = root
			index++
		}
		b.StopTimer()
		if len(cache.roots) > hookWorkerRootCacheLimit {
			b.Fatalf("root cache retained %d entries", len(cache.roots))
		}
	})
}

func BenchmarkHookRuntimeTransport(b *testing.B) {
	if testing.Short() {
		b.Skip("process benchmark disabled in short mode")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		b.Fatal("resolve benchmark source")
	}
	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	binary := filepath.Join(b.TempDir(), "reconc")
	build := exec.Command("go", "build", "-o", binary, "./cmd/reconc")
	build.Dir = moduleRoot
	if output, err := build.CombinedOutput(); err != nil {
		b.Fatalf("build benchmark binary: %v: %s", err, output)
	}
	repo := b.TempDir()
	b.Setenv("RECONC_HOME", b.TempDir())
	b.Setenv("RECONC_CLAUDE_STATE_DIR", b.TempDir())
	payload := []byte(`{"session_id":"transport-benchmark"}`)
	initialize := exec.Command(binary, "hook", "runtime", "claude-session-start", repo)
	initialize.Stdin = bytes.NewReader(payload)
	if output, err := initialize.CombinedOutput(); err != nil {
		b.Fatalf("initialize benchmark session: %v: %s", err, output)
	}

	b.Run("one-shot", func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			command := exec.Command(binary, "hook", "runtime", "claude-user-prompt-submit", repo)
			command.Stdin = bytes.NewReader(payload)
			if output, err := command.CombinedOutput(); err != nil {
				b.Fatalf("one-shot request: %v: %s", err, output)
			}
		}
	})

	b.Run("stdio-worker", func(b *testing.B) {
		command := exec.Command(binary, "hook", "worker")
		stdin, err := command.StdinPipe()
		if err != nil {
			b.Fatal(err)
		}
		stdout, err := command.StdoutPipe()
		if err != nil {
			b.Fatal(err)
		}
		var stderr bytes.Buffer
		command.Stderr = &stderr
		if err := command.Start(); err != nil {
			b.Fatal(err)
		}
		encoder := json.NewEncoder(stdin)
		decoder := json.NewDecoder(stdout)
		ping := hookWorkerRequest{FormatVersion: 1, Type: "ping", ID: "benchmark-ping"}
		if err := encoder.Encode(ping); err != nil {
			b.Fatal(err)
		}
		var response hookWorkerResponse
		if err := decoder.Decode(&response); err != nil || response.Code != 0 {
			b.Fatalf("worker handshake: response=%+v err=%v stderr=%s", response, err, stderr.String())
		}
		b.ResetTimer()
		for index := 0; index < b.N; index++ {
			request := hookWorkerRequest{
				FormatVersion: 1,
				Type:          "request",
				ID:            "benchmark-request",
				Event:         "claude-user-prompt-submit",
				Repo:          repo,
				Payload:       json.RawMessage(payload),
			}
			if err := encoder.Encode(request); err != nil {
				b.Fatal(err)
			}
			response = hookWorkerResponse{}
			if err := decoder.Decode(&response); err != nil || response.Code != 0 {
				b.Fatalf("worker request: response=%+v err=%v stderr=%s", response, err, stderr.String())
			}
		}
		b.StopTimer()
		_ = encoder.Encode(hookWorkerRequest{FormatVersion: 1, Type: "shutdown", ID: "benchmark-shutdown"})
		_ = decoder.Decode(&response)
		_ = stdin.Close()
		if err := command.Wait(); err != nil {
			b.Fatalf("worker shutdown: %v: %s", err, stderr.String())
		}
	})
}
