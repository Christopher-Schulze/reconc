package assurance

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/policy"
)

func TestGoConcurrencyBoundaryFindsChangedProductionGoStatements(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "worker"), 0o755); err != nil {
		t.Fatal(err)
	}
	production := "package worker\n\nfunc Start() { go work() }\nfunc work() {}\n"
	if err := os.WriteFile(filepath.Join(root, "worker", "worker.go"), []byte(production), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "worker", "worker_test.go"), []byte(production), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := policy.AssuranceGate{
		ID: "go-concurrency", Type: policy.AssuranceGoConcurrency,
		ScanPaths: []string{"**/*.go"}, ExcludePaths: []string{"**/*_test.go"},
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{
		ChangedPaths: []string{"worker/worker.go", "worker/worker_test.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || len(findings[0].Paths) != 1 || findings[0].Paths[0] != "worker/worker.go" {
		t.Fatalf("unexpected Go concurrency findings: %+v", findings)
	}
}

func TestGoConcurrencyBoundaryFailsClosedOnInvalidGo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package broken\nfunc"), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := policy.AssuranceGate{ID: "go-concurrency", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"**/*.go"}}
	if _, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"broken.go"}}); err == nil {
		t.Fatal("invalid changed Go source must fail closed")
	}
}

func TestGoConcurrencyBoundaryRecognizesOnlyCompleteWaitGroupOwnership(t *testing.T) {
	tests := []struct {
		name       string
		body       string
		wantIssues int
	}{
		{
			name: "owned",
			body: `package worker
import "sync"
func Start() {
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			work()
		}()
	}
	wg.Wait()
}
`,
		},
		{
			name: "missing wait",
			body: `package worker
import "sync"
func Start() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done() }()
}
`,
			wantIssues: 1,
		},
		{
			name: "non-deferred done",
			body: `package worker
import "sync"
func Start() {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { wg.Done() }()
	wg.Wait()
}
`,
			wantIssues: 1,
		},
		{
			name: "add after launch",
			body: `package worker
import "sync"
func Start() {
	var wg sync.WaitGroup
	go func() { defer wg.Done() }()
	wg.Add(1)
	wg.Wait()
}
`,
			wantIssues: 1,
		},
		{
			name: "lookalike lifecycle type",
			body: `package worker
type group struct{}
func (group) Add(int) {}
func (group) Done() {}
func (group) Wait() {}
func Start() {
	var wg group
	wg.Add(1)
	go func() { defer wg.Done() }()
	wg.Wait()
}
`,
			wantIssues: 1,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "worker.go"), []byte(test.body), 0o644); err != nil {
				t.Fatal(err)
			}
			gate := policy.AssuranceGate{ID: "go-concurrency", Type: policy.AssuranceGoConcurrency, ScanPaths: []string{"**/*.go"}}
			findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"worker.go"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != test.wantIssues {
				t.Fatalf("findings = %d, want %d: %+v", len(findings), test.wantIssues, findings)
			}
		})
	}
}

func TestGoConcurrencyBoundaryAcceptsStructFieldWaitGroup(t *testing.T) {
	root := t.TempDir()
	production := `package worker

import "sync"

type Server struct{ wg sync.WaitGroup }

func (s *Server) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		work()
	}()
	s.wg.Wait()
}

func work() {}
`
	if err := os.WriteFile(filepath.Join(root, "worker.go"), []byte(production), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := policy.AssuranceGate{
		ID: "go-concurrency", Type: policy.AssuranceGoConcurrency,
		ScanPaths: []string{"**/*.go"},
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"worker.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("struct-field WaitGroup ownership must not be flagged: %+v", findings)
	}
}

func TestGoConcurrencyBoundaryAcceptsNamedWorkerWithWaitGroupPointer(t *testing.T) {
	root := t.TempDir()
	production := `package worker

import "sync"

func Start() {
	var wg sync.WaitGroup
	wg.Add(1)
	go work(&wg)
	wg.Wait()
}

func work(wg *sync.WaitGroup) { defer wg.Done() }
`
	if err := os.WriteFile(filepath.Join(root, "worker.go"), []byte(production), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := policy.AssuranceGate{
		ID: "go-concurrency", Type: policy.AssuranceGoConcurrency,
		ScanPaths: []string{"**/*.go"},
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"worker.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("go worker(&wg) delegation must not be flagged: %+v", findings)
	}
}

func TestGoConcurrencyBoundaryAcceptsNamedWorkerAliasesAndMethods(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "type and receiver aliases",
			body: `package worker

import "sync"

type group = sync.WaitGroup

func Start() {
	var wg group
	worker := work
	wg.Add(1)
	go worker(&wg)
	wg.Wait()
}

func work(wg *group) {
	local := wg
	defer local.Done()
}
`,
		},
		{
			name: "local method",
			body: `package worker

import "sync"

type worker struct{}

func (worker) run(wg *sync.WaitGroup) { defer wg.Done() }

func Start() {
	var wg sync.WaitGroup
	var item worker
	wg.Add(1)
	go item.run(&wg)
	wg.Wait()
}
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "worker.go"), []byte(test.body), 0o644); err != nil {
				t.Fatal(err)
			}
			gate := policy.AssuranceGate{
				ID: "go-concurrency", Type: policy.AssuranceGoConcurrency,
				ScanPaths: []string{"**/*.go"},
			}
			findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"worker.go"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 0 {
				t.Fatalf("complete named worker ownership must not be flagged: %+v", findings)
			}
		})
	}
}

func TestGoConcurrencyBoundaryRejectsIncompleteNamedWorkers(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "missing done",
			body: `package worker

import "sync"

func Start() {
	var wg sync.WaitGroup
	wg.Add(1)
	go work(&wg)
	wg.Wait()
}

func work(wg *sync.WaitGroup) {}
`,
		},
		{
			name: "wrong parameter",
			body: `package worker

import (
	"context"
	"sync"
)

func Start() {
	var wg sync.WaitGroup
	wg.Add(1)
	go work(&wg, context.Background())
	wg.Wait()
}

func work(wg *sync.WaitGroup, ctx context.Context) { defer ctx.Done() }
`,
		},
		{
			name: "non deferred done",
			body: `package worker

import "sync"

func Start() {
	var wg sync.WaitGroup
	wg.Add(1)
	go work(&wg)
	wg.Wait()
}

func work(wg *sync.WaitGroup) { wg.Done() }
`,
		},
		{
			name: "unresolved worker",
			body: `package worker

import "sync"

func Start() {
	var wg sync.WaitGroup
	wg.Add(1)
	go missing(&wg)
	wg.Wait()
}
`,
		},
		{
			name: "external worker",
			body: `package worker

import (
	"sync"
		"example.com/external"
)

func Start() {
	var wg sync.WaitGroup
	wg.Add(1)
	go external.Work(&wg)
	wg.Wait()
}
`,
		},
		{
			name: "ambiguous worker",
			body: `package worker

import "sync"

func Start() {
	var wg sync.WaitGroup
	wg.Add(1)
	go work(&wg)
	wg.Wait()
}

func work(wg *sync.WaitGroup) { defer wg.Done() }
func work(wg *sync.WaitGroup) { defer wg.Done() }
`,
		},
		{
			name: "alias declared after launch",
			body: `package worker

import "sync"

func Start() {
	var wg sync.WaitGroup
	wg.Add(1)
	go worker(&wg)
	worker := complete
	wg.Wait()
}

func worker(wg *sync.WaitGroup) {}
func complete(wg *sync.WaitGroup) { defer wg.Done() }
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "worker.go"), []byte(test.body), 0o644); err != nil {
				t.Fatal(err)
			}
			gate := policy.AssuranceGate{
				ID: "go-concurrency", Type: policy.AssuranceGoConcurrency,
				ScanPaths: []string{"**/*.go"},
			}
			findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"worker.go"}})
			if err != nil {
				t.Fatal(err)
			}
			if len(findings) != 1 {
				t.Fatalf("incomplete named worker must be flagged: %+v", findings)
			}
		})
	}
}

func TestGoConcurrencyBoundaryStillFlagsWaitlessLaunch(t *testing.T) {
	root := t.TempDir()
	// A named call without a WaitGroup pointer argument stays a finding.
	production := `package worker

func Start() {
	go work()
}

func work() {}
`
	if err := os.WriteFile(filepath.Join(root, "worker.go"), []byte(production), 0o644); err != nil {
		t.Fatal(err)
	}
	gate := policy.AssuranceGate{
		ID: "go-concurrency", Type: policy.AssuranceGoConcurrency,
		ScanPaths: []string{"**/*.go"},
	}
	findings, err := Evaluate(root, []policy.AssuranceGate{gate}, Inputs{ChangedPaths: []string{"worker.go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("unowned named launch must stay flagged: %+v", findings)
	}
}
