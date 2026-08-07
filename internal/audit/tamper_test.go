package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func chainRepo(t *testing.T, records int) string {
	t.Helper()
	repo := t.TempDir()
	for index := 0; index < records; index++ {
		if err := Append(repo, Entry{Event: "check", Decision: "pass", OK: true}, DefaultMaxSizeBytes); err != nil {
			t.Fatalf("append %d: %v", index, err)
		}
	}
	report, err := Verify(repo)
	if err != nil || !report.Valid || report.Entries != records {
		t.Fatalf("clean chain did not verify: %+v %v", report, err)
	}
	return repo
}

func auditLines(t *testing.T, repo string) []string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repo, AuditFileRelative))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(body), "\n"), "\n")
}

func writeAuditLines(t *testing.T, repo string, lines []string) {
	t.Helper()
	body := ""
	if len(lines) > 0 {
		body = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(filepath.Join(repo, AuditFileRelative), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestVerifyDetectsEveryTamperShape is the property the audit log exists for.
// A decision record that can be edited, dropped, reordered, replayed, or
// removed without detection is not evidence, so each of those shapes must make
// verification fail rather than report a healthy chain.
func TestVerifyDetectsEveryTamperShape(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, repo string)
	}{
		{
			name: "edited decision",
			mutate: func(t *testing.T, repo string) {
				lines := auditLines(t, repo)
				lines[2] = strings.Replace(lines[2], `"decision":"pass"`, `"decision":"block"`, 1)
				writeAuditLines(t, repo, lines)
			},
		},
		{
			name: "dropped tail record",
			mutate: func(t *testing.T, repo string) {
				lines := auditLines(t, repo)
				writeAuditLines(t, repo, lines[:len(lines)-1])
			},
		},
		{
			name: "dropped middle record",
			mutate: func(t *testing.T, repo string) {
				lines := auditLines(t, repo)
				writeAuditLines(t, repo, append(lines[:2:2], lines[3:]...))
			},
		},
		{
			name: "replayed record",
			mutate: func(t *testing.T, repo string) {
				lines := auditLines(t, repo)
				writeAuditLines(t, repo, append(lines, lines[len(lines)-1]))
			},
		},
		{
			name: "reordered records",
			mutate: func(t *testing.T, repo string) {
				lines := auditLines(t, repo)
				lines[1], lines[2] = lines[2], lines[1]
				writeAuditLines(t, repo, lines)
			},
		},
		{
			name: "truncated log",
			mutate: func(t *testing.T, repo string) {
				writeAuditLines(t, repo, nil)
			},
		},
		{
			name: "removed log",
			mutate: func(t *testing.T, repo string) {
				if err := os.Remove(filepath.Join(repo, AuditFileRelative)); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := chainRepo(t, 5)
			tc.mutate(t, repo)
			report, err := Verify(repo)
			if err == nil && report.Valid {
				t.Fatalf("tampered chain verified as healthy: %+v", report)
			}
		})
	}
}

// TestVerifyAcceptsAnUntouchedChain keeps the tamper suite from passing by
// rejecting everything.
func TestVerifyAcceptsAnUntouchedChain(t *testing.T) {
	repo := chainRepo(t, 12)
	report, err := Verify(repo)
	if err != nil || !report.Valid {
		t.Fatalf("untouched chain rejected: %+v %v", report, err)
	}
	if report.FirstSequence != 1 || report.LastSequence != 12 {
		t.Fatalf("sequence range = %d..%d, want 1..12", report.FirstSequence, report.LastSequence)
	}
}
