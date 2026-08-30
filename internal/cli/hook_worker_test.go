package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	policyruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

var hookWorkerFrameBenchmarkSink []byte

func TestHookWorkerProcessesOrderedRequestsAndShutdown(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	frames := []hookWorkerRequest{
		{FormatVersion: 1, Type: "ping", ID: "ping-1"},
		{
			FormatVersion: 1,
			Type:          "request",
			ID:            "request-1",
			Event:         "claude-session-start",
			Repo:          repo,
			Payload:       json.RawMessage(`{"session_id":"worker-session"}`),
		},
		{
			FormatVersion: 1,
			Type:          "request",
			ID:            "request-2",
			Event:         "claude-session-end",
			Repo:          repo,
			Payload:       json.RawMessage(`{"session_id":"worker-session"}`),
		},
		{FormatVersion: 1, Type: "shutdown", ID: "shutdown-1"},
	}
	var input bytes.Buffer
	encoder := json.NewEncoder(&input)
	for _, frame := range frames {
		if err := encoder.Encode(frame); err != nil {
			t.Fatal(err)
		}
	}
	var output bytes.Buffer
	if err := runHookWorker(nil, &input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	for index, wantID := range []string{"ping-1", "request-1", "request-2", "shutdown-1"} {
		var response hookWorkerResponse
		if err := decoder.Decode(&response); err != nil {
			t.Fatalf("decode response %d: %v", index, err)
		}
		if response.ID != wantID || response.Code != 0 || response.Error != "" {
			t.Fatalf("response %d = %+v, want id=%q code=0", index, response, wantID)
		}
		if index == len(frames)-1 && response.Type != "shutdown" {
			t.Fatalf("shutdown response type=%q", response.Type)
		}
	}
}

func TestHookWorkerProtocolErrorIsFramedAndWorkerContinues(t *testing.T) {
	input := strings.NewReader(
		`{"format_version":2,"type":"ping","id":"old"}` + "\n" +
			`{"format_version":1,"type":"ping","id":"current"}` + "\n",
	)
	var output bytes.Buffer
	if err := runHookWorker(nil, input, &output); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var first, second hookWorkerResponse
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&second); err != nil {
		t.Fatal(err)
	}
	if first.Type != "error" || first.ID != "old" || !strings.Contains(first.Error, "format_version") {
		t.Fatalf("protocol error response=%+v", first)
	}
	if second.Type != "response" || second.ID != "current" || second.Code != 0 {
		t.Fatalf("worker did not continue after request error: %+v", second)
	}
}

func TestHookWorkerOversizedFrameIsDrainedAndWorkerContinues(t *testing.T) {
	oversized := strings.Repeat("x", 128)
	input := strings.NewReader(oversized + "\n" +
		`{"format_version":1,"type":"ping","id":"valid"}` + "\n" +
		`{"format_version":1,"type":"shutdown","id":"bye"}` + "\n")
	var output bytes.Buffer
	if err := runHookWorkerWithFrameLimit(nil, input, &output, 64); err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(&output)
	var oversizedResponse, validResponse, shutdownResponse hookWorkerResponse
	if err := decoder.Decode(&oversizedResponse); err != nil {
		t.Fatal(err)
	}
	if oversizedResponse.Type != "error" || oversizedResponse.ID != "" || oversizedResponse.Error != errHookWorkerFrameTooLarge.Error() {
		t.Fatalf("oversized response = %+v", oversizedResponse)
	}
	if err := decoder.Decode(&validResponse); err != nil {
		t.Fatal(err)
	}
	if validResponse.Type != "response" || validResponse.ID != "valid" || validResponse.Code != 0 {
		t.Fatalf("worker did not recover after oversized frame: %+v", validResponse)
	}
	if err := decoder.Decode(&shutdownResponse); err != nil {
		t.Fatal(err)
	}
	if shutdownResponse.Type != "shutdown" || shutdownResponse.ID != "bye" {
		t.Fatalf("shutdown response after oversized frame = %+v", shutdownResponse)
	}
}

func TestHookWorkerOversizedFrameWithoutTerminatorIsTerminal(t *testing.T) {
	oversized := strings.Repeat("x", 128)
	if err := runHookWorkerWithFrameLimit(nil, strings.NewReader(oversized), &bytes.Buffer{}, 64); err == nil || !strings.Contains(err.Error(), "truncated hook worker frame") {
		t.Fatalf("unterminated oversized frame error = %v", err)
	}
}

func TestDrainHookWorkerFrameBoundsDiscardWork(t *testing.T) {
	if err := drainHookWorkerFrame(bufio.NewReader(strings.NewReader("12345\n")), 4); err == nil || !errors.Is(err, errHookWorkerFrameDrainFailed) {
		t.Fatalf("drain over budget error = %v", err)
	}
	if err := drainHookWorkerFrame(bufio.NewReader(strings.NewReader("1234\nnext\n")), 5); err != nil {
		t.Fatalf("bounded drain error = %v", err)
	}
}

func TestReadHookWorkerFrameRejectsBoundAndEncodingFailures(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
		limit int
		want  string
	}{
		{name: "oversized", input: []byte("12345\n"), limit: 4, want: "bounded protocol limit"},
		{name: "invalid UTF-8", input: []byte{0xff, '\n'}, limit: 4, want: "valid UTF-8"},
		{name: "truncated", input: []byte("{}"), limit: 4, want: "truncated"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := readHookWorkerFrameLimit(bufio.NewReader(bytes.NewReader(test.input)), test.limit)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
		})
	}
	_, err := readHookWorkerFrameLimit(bufio.NewReader(strings.NewReader("")), 4)
	if err != io.EOF {
		t.Fatalf("empty stream error=%v, want EOF", err)
	}
}

func TestReadHookWorkerFrameBoundsGrowthAllocations(t *testing.T) {
	frame := append(bytes.Repeat([]byte{'x'}, 200<<10), '\n')
	allocations := testing.AllocsPerRun(25, func() {
		reader := bufio.NewReaderSize(bytes.NewReader(frame), hookWorkerReadBuffer)
		body, err := readHookWorkerFrameLimit(reader, len(frame))
		if err != nil {
			t.Fatal(err)
		}
		hookWorkerFrameBenchmarkSink = body
	})
	if allocations > 4 {
		t.Fatalf("200 KiB worker frame allocations = %.1f, want <= 4", allocations)
	}
}

func BenchmarkReadHookWorkerFrame(b *testing.B) {
	for _, benchmark := range []struct {
		name string
		size int
	}{
		{name: "4KiB", size: 4 << 10},
		{name: "64KiB", size: 64 << 10},
		{name: "256KiB", size: 256 << 10},
		{name: "1MiB", size: 1 << 20},
	} {
		b.Run(benchmark.name, func(b *testing.B) {
			benchmarkReadHookWorkerFrame(b, benchmark.size)
		})
	}
}

func BenchmarkHookWorkerFrameRepresentativeCalibrated(b *testing.B) {
	benchmarkReadHookWorkerFrame(b, 64<<10)
}

func BenchmarkHookWorkerFrameLarge(b *testing.B) {
	benchmarkReadHookWorkerFrame(b, 1<<20)
}

func benchmarkReadHookWorkerFrame(b *testing.B, size int) {
	frame := append(bytes.Repeat([]byte{'x'}, size), '\n')
	b.ReportAllocs()
	b.SetBytes(int64(size))
	for b.Loop() {
		reader := bufio.NewReaderSize(bytes.NewReader(frame), hookWorkerReadBuffer)
		body, err := readHookWorkerFrameLimit(reader, len(frame))
		if err != nil {
			b.Fatal(err)
		}
		hookWorkerFrameBenchmarkSink = body
	}
}

func TestDecodeHookWorkerRequestRejectsUnknownFieldsAndInvalidTokens(t *testing.T) {
	for _, frame := range []string{
		`{"format_version":1,"type":"ping","id":"ok","extra":true}`,
		`{"format_version":1,"type":"ping","id":"bad id"}`,
		`{"format_version":1,"type":"request","id":"ok","event":"x","repo":".","payload":null}`,
	} {
		if _, err := decodeHookWorkerRequest([]byte(frame)); err == nil {
			t.Fatalf("invalid frame accepted: %s", frame)
		}
	}
}

func FuzzDecodeHookWorkerRequest(f *testing.F) {
	for _, seed := range [][]byte{
		[]byte(`{"format_version":1,"type":"ping","id":"seed"}`),
		[]byte(`{"format_version":2,"type":"request","id":"old","event":"x","repo":".","payload":{}}`),
		{0xff, 0x00, '{', '}'},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, frame []byte) {
		_, _ = decodeHookWorkerRequest(frame)
	})
}

func TestHookWorkerRootCacheRefreshesReplacedRepositoryIdentity(t *testing.T) {
	parent := t.TempDir()
	repo := filepath.Join(parent, "repo")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	cache := hookWorkerRootCache{roots: make(map[string]agentsession.ResolvedRepoRoot)}
	first, err := cache.resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	oldRepo := filepath.Join(parent, "replaced-repo")
	if err := os.Rename(repo, oldRepo); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := first.Revalidate(); err == nil {
		t.Fatal("replaced repository retained the old resolved identity")
	}
	second, err := cache.resolve(repo)
	if err != nil {
		t.Fatal(err)
	}
	if second.Path() != first.Path() {
		t.Fatalf("canonical path drifted across same-path replacement: first=%q second=%q", first.Path(), second.Path())
	}
	if err := second.Revalidate(); err != nil {
		t.Fatalf("refreshed identity did not revalidate: %v", err)
	}
}

func TestHookWorkerRootCacheRevalidatesReboundAlias(t *testing.T) {
	parent := t.TempDir()
	first := filepath.Join(parent, "first")
	second := filepath.Join(parent, "second")
	alias := filepath.Join(parent, "alias")
	for _, directory := range []string{first, second} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	createHookWorkerDirectoryAliasForTest(t, first, alias)
	cache := hookWorkerRootCache{roots: make(map[string]agentsession.ResolvedRepoRoot)}
	resolvedFirst, err := cache.resolve(alias)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	createHookWorkerDirectoryAliasForTest(t, second, alias)
	resolvedSecond, err := cache.resolve(alias)
	if err != nil {
		t.Fatal(err)
	}
	wantSecond, err := agentsession.ResolveRepoRootRef(second)
	if err != nil {
		t.Fatal(err)
	}
	if resolvedFirst.Path() == resolvedSecond.Path() || resolvedSecond.Path() != wantSecond.Path() {
		t.Fatalf("rebound alias resolved first=%q second=%q want=%q", resolvedFirst.Path(), resolvedSecond.Path(), wantSecond.Path())
	}
}

func TestHookWorkerRootCacheBoundsHostileCardinalityAndEviction(t *testing.T) {
	parent := t.TempDir()
	cache := hookWorkerRootCache{}
	paths := make([]string, hookWorkerRootCacheLimit*3+1)
	for index := range paths {
		paths[index] = filepath.Join(parent, fmt.Sprintf("repo-%02d", index))
		if err := os.Mkdir(paths[index], 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := cache.resolve(paths[index]); err != nil {
			t.Fatal(err)
		}
		if len(cache.roots) > hookWorkerRootCacheLimit {
			t.Fatalf("root cache retained %d entries after %d distinct roots", len(cache.roots), index+1)
		}
	}
	if len(cache.roots) != 1 {
		t.Fatalf("clear-on-overflow retained %d entries, want 1", len(cache.roots))
	}
	if _, retained := cache.roots[paths[0]]; retained {
		t.Fatal("evicted root remained cached")
	}
	replaced := filepath.Join(parent, "repo-00-replaced")
	if err := os.Rename(paths[0], replaced); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(paths[0], 0o755); err != nil {
		t.Fatal(err)
	}
	refreshed, err := cache.resolve(paths[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := refreshed.Revalidate(); err != nil {
		t.Fatalf("evicted root resolved stale identity: %v", err)
	}
	missing := filepath.Join(parent, "missing")
	if _, err := cache.resolve(missing); err == nil {
		t.Fatal("malformed missing root was accepted")
	}
	if _, retained := cache.roots[missing]; retained {
		t.Fatal("malformed root was retained")
	}
}

func TestHookWorkerRootCacheConcurrentResolutionStaysBounded(t *testing.T) {
	parent := t.TempDir()
	paths := make([]string, hookWorkerRootCacheLimit*2)
	for index := range paths {
		paths[index] = filepath.Join(parent, fmt.Sprintf("repo-%02d", index))
		if err := os.Mkdir(paths[index], 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cache := hookWorkerRootCache{}
	start := make(chan struct{})
	errorsByWorker := make(chan error, len(paths)*2)
	var workers sync.WaitGroup
	for worker := 0; worker < len(paths)*2; worker++ {
		worker := worker
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			for request := 0; request < len(paths); request++ {
				if _, err := cache.resolve(paths[(worker+request)%len(paths)]); err != nil {
					errorsByWorker <- err
					return
				}
			}
		}()
	}
	close(start)
	workers.Wait()
	close(errorsByWorker)
	for err := range errorsByWorker {
		t.Fatal(err)
	}
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if len(cache.roots) > hookWorkerRootCacheLimit {
		t.Fatalf("concurrent root cache retained %d entries", len(cache.roots))
	}
	for key, root := range cache.roots {
		if err := root.Revalidate(); err != nil {
			t.Errorf("cached root %q failed revalidation: %v", key, err)
		}
	}
}

func TestHookWorkerRootCacheAllocationBounds(t *testing.T) {
	singleRepo := t.TempDir()
	singleCache := hookWorkerRootCache{}
	if _, err := singleCache.resolve(singleRepo); err != nil {
		t.Fatal(err)
	}
	var allocationErr error
	hitAllocations := testing.AllocsPerRun(100, func() {
		_, allocationErr = singleCache.resolve(singleRepo)
	})
	if allocationErr != nil {
		t.Fatal(allocationErr)
	}
	if hitAllocations > 128 {
		t.Fatalf("single-repository cache hit allocations = %.1f, want <= 128", hitAllocations)
	}

	paths := make([]string, hookWorkerRootCacheLimit+1)
	for index := range paths {
		paths[index] = t.TempDir()
	}
	hostileCache := hookWorkerRootCache{}
	hostileAllocations := testing.AllocsPerRun(25, func() {
		for _, path := range paths {
			_, allocationErr = hostileCache.resolve(path)
			if allocationErr != nil {
				return
			}
		}
	})
	if allocationErr != nil {
		t.Fatal(allocationErr)
	}
	hostileAllocationLimit := float64(128 * len(paths))
	if hostileAllocations > hostileAllocationLimit {
		t.Fatalf("high-cardinality cache cycle allocations = %.1f, want <= %.0f", hostileAllocations, hostileAllocationLimit)
	}
	if len(hostileCache.roots) > hookWorkerRootCacheLimit {
		t.Fatalf("allocation run retained %d roots", len(hostileCache.roots))
	}
}

func TestHookWorkerEvaluatorReusesAndInvalidatesRepositoryPlan(t *testing.T) {
	repo := bootstrapE2ERepo(t)
	cache := hookWorkerRootCache{
		roots:     make(map[string]agentsession.ResolvedRepoRoot),
		evaluator: policyruntime.NewEvaluator(),
	}
	request := hookWorkerRequest{
		FormatVersion: hookWorkerFormatVersion,
		Type:          "request",
		ID:            "first",
		Event:         "claude-pre-tool-use",
		Repo:          repo,
		Payload:       json.RawMessage(`{"session_id":"worker-plan","tool_name":"Bash","tool_use_id":"one","tool_input":{"command":"go test ./..."}}`),
	}
	first, stop := executeHookWorkerRequest(request, cache.resolve, cache.evaluator, cache.stopCache)
	if stop || first.Code != 0 {
		t.Fatalf("first worker policy request = %+v stop=%t", first, stop)
	}
	policyPath := filepath.Join(repo, "policies", "rules.yml")
	policyBytes, err := os.ReadFile(policyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policyPath, append(policyBytes, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	request.ID = "drifted"
	request.Payload = json.RawMessage(`{"session_id":"worker-plan","tool_name":"Bash","tool_use_id":"two","tool_input":{"command":"go test ./..."}}`)
	drifted, stop := executeHookWorkerRequest(request, cache.resolve, cache.evaluator, cache.stopCache)
	if stop || drifted.Code != 2 || !strings.Contains(drifted.Stderr, "source_digest") {
		t.Fatalf("drifted worker policy request = %+v stop=%t", drifted, stop)
	}
}
