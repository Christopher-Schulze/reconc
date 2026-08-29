package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/compiler"
	productruntime "reconc.dev/reconc/internal/runtime"
	"reconc.dev/reconc/internal/runtime/agentsession"
)

func BenchmarkGatewayRepositoryEffectEvaluatorReuse(b *testing.B) {
	b.Setenv("RECONC_HOME", b.TempDir())
	b.Setenv(agentsession.StateRootEnv, b.TempDir())
	repository := b.TempDir()
	if err := os.WriteFile(filepath.Join(repository, "AGENTS.md"), []byte("# fixture\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repository, ".reconc.yml"), []byte("rules: []\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	if _, err := compiler.CompileRepoPolicy(repository, "gateway-evaluator-benchmark"); err != nil {
		b.Fatal(err)
	}
	runtimeEvaluator := productruntime.NewEvaluator()
	snapshot, err := (gatewayPolicyLoader{evaluator: runtimeEvaluator}).Load(
		context.Background(), repository,
	)
	if err != nil {
		b.Fatal(err)
	}
	arguments, err := action.ParseObjectJSON([]byte(`{"path":"src/main.go"}`))
	if err != nil {
		b.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		effect action.EffectKind
		share  bool
	}{
		{name: "read-shared-plan", effect: action.EffectRepositoryRead, share: true},
		{name: "write-shared-plan", effect: action.EffectRepositoryWrite, share: true},
		{name: "read-new-evaluator", effect: action.EffectRepositoryRead},
		{name: "write-new-evaluator", effect: action.EffectRepositoryWrite},
	} {
		test := test
		b.Run(test.name, func(b *testing.B) {
			tool := action.Tool{Effect: action.Effect{
				Kind: test.effect, PathFields: []string{"/path"},
			}}
			request := action.Request{Arguments: &arguments}
			sharedLoader := gatewayPolicyLoader{evaluator: runtimeEvaluator}
			planRebuilds := 0
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				current := snapshot
				provider := gatewayEvidenceProvider{evaluator: runtimeEvaluator}
				if !test.share {
					freshEvaluator := productruntime.NewEvaluator()
					var loadErr error
					current, loadErr = (gatewayPolicyLoader{evaluator: freshEvaluator}).Load(
						context.Background(), repository,
					)
					if loadErr != nil {
						b.Fatal(loadErr)
					}
					current.RepositoryEffectCheck = nil
					provider.evaluator = productruntime.NewEvaluator()
				} else {
					var loadErr error
					current, loadErr = sharedLoader.Load(context.Background(), repository)
					if loadErr != nil {
						b.Fatal(loadErr)
					}
				}
				if current.Plan != snapshot.Plan {
					planRebuilds++
				}
				evidence, observeErr := provider.Observe(context.Background(), current, request, tool)
				if observeErr != nil || evidence.RepositoryEffect == nil {
					b.Fatalf("repository-effect evidence = %#v, %v", evidence, observeErr)
				}
			}
			b.ReportMetric(float64(planRebuilds)/float64(b.N), "plan-rebuilds/op")
		})
	}
}
