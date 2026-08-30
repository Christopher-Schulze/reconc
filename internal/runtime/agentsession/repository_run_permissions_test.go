package agentsession

import (
	"os"
	"path/filepath"
	"testing"

	"reconc.dev/reconc/internal/privatefs"
)

func TestRepositoryRunArtifactsUsePrivateAccessContract(t *testing.T) {
	repo := t.TempDir()
	if err := saveRepositoryRunState(repo, repositoryRunState{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := appendRunDecision(repo, RunDecision{Event: "test", Branch: "private"}); err != nil {
		t.Fatal(err)
	}
	runDirectory := filepath.Join(repo, ".reconc", "run")
	if err := privatefs.ValidateDirectory(runDirectory); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		repositoryRunStatePathResolved(repo),
		runDecisionLogPathResolved(repo),
		runDecisionLogPathResolved(repo) + ".lock",
	} {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		info, statErr := file.Stat()
		validateErr := privatefs.ValidateFile(file, info)
		closeErr := file.Close()
		if statErr != nil || validateErr != nil || closeErr != nil {
			t.Fatalf("private repository-run artifact %s: stat=%v validate=%v close=%v", path, statErr, validateErr, closeErr)
		}
	}
}
