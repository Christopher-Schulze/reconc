//go:build !windows

package runtime

import (
	"os/exec"
	"strings"
	"testing"
)

func TestMigrationRecipeRunsRealIsolatedSQLiteWorkflow(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("SQLite CLI is required for the real migration integration fixture")
	}
	for _, test := range []struct {
		name, forward, rollback string
		want                    Decision
	}{
		{"round trip", "ALTER TABLE records ADD COLUMN label TEXT;", "ALTER TABLE records DROP COLUMN label;", DecisionPass},
		{"invalid forward", "ALTER TABLE missing ADD COLUMN label TEXT;", "ALTER TABLE records DROP COLUMN label;", DecisionBlock},
		{"rollback loses data", "ALTER TABLE records ADD COLUMN label TEXT;", "DELETE FROM records;", DecisionBlock},
	} {
		t.Run(test.name, func(t *testing.T) {
			withRECONCHome(t)
			repo := t.TempDir()
			writeFile(t, repo, "AGENTS.md", "# migration fixture\n")
			writeFile(t, repo, "migrations/forward.sql", test.forward)
			writeFile(t, repo, "migrations/rollback.sql", test.rollback)
			writeScript(t, repo, "scripts/check-migrations.sh", migrationRecipeSQLiteScript)
			writeFile(t, repo, "policies/rules.yml", `rules:
  - id: migration
    template: schema-migration-safety
    script: scripts/check-migrations.sh
    when_paths: ['migrations/**']
    args: ['--engine', 'sqlite', '--database', 'fixture.db', '--forward', '--rollback', '--isolated']
    cache_inputs: ['migrations/forward.sql', 'migrations/rollback.sql', 'scripts/check-migrations.sh']
`)
			if _, err := compileTestHelper(repo); err != nil {
				t.Fatal(err)
			}
			report, err := CheckRepoPolicy(repo, ExecutionInputs{WritePaths: []string{"migrations/forward.sql"}})
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != test.want {
				t.Fatalf("migration decision = %s, want %s: %+v", report.Decision, test.want, report)
			}
			for _, violation := range report.Violations {
				if strings.Contains(violation.Explanation, "script scripts/check-migrations.sh error:") {
					t.Fatalf("domain failure misclassified as script error: %s", violation.Explanation)
				}
			}
		})
	}
}

const migrationRecipeSQLiteScript = `#!/bin/sh
set -eu
[ "$#" -eq 7 ] && [ "$1" = --engine ] && [ "$2" = sqlite ] && [ "$3" = --database ] && [ "$4" = fixture.db ] && [ "$5" = --forward ] && [ "$6" = --rollback ] && [ "$7" = --isolated ] || exit 1
input=$(cat)
printf '%s' "$input" | grep -F '"migrations/forward.sql"' >/dev/null || exit 1
database=$4
[ ! -e "$database" ] || exit 1
sqlite3 -batch -bail "$database" 'CREATE TABLE records(id INTEGER PRIMARY KEY); INSERT INTO records VALUES(7);'
before=$(sqlite3 -batch "$database" '.dump')
forward=false
rollback=false
if sqlite3 -batch -bail "$database" < migrations/forward.sql; then
  forward=true
  if sqlite3 -batch -bail "$database" < migrations/rollback.sql; then
    after=$(sqlite3 -batch "$database" '.dump')
    if [ "$before" = "$after" ]; then rollback=true; fi
  fi
fi
result=block
code=2
if [ "$forward" = true ] && [ "$rollback" = true ]; then result=pass; code=0; fi
printf '{"contract":"schema-migration-safety","result":"%s","engine":"sqlite","database":"fixture.db","forward":%s,"rollback":%s,"isolated":true,"evidence":"sqlite-schema-and-data-roundtrip"}\n' "$result" "$forward" "$rollback"
exit "$code"
`
