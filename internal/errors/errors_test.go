package errors

import (
	stderrors "errors"
	"testing"
)

func TestPolicySourceErrorFormat(t *testing.T) {
	err := &PolicySourceError{Message: "bad yaml"}
	if got, want := err.Error(), "policy source: bad yaml"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPolicySourceErrorWithCause(t *testing.T) {
	cause := stderrors.New("underlying")
	err := &PolicySourceError{Message: "read failed", Cause: cause}
	if got, want := err.Error(), "policy source: read failed: underlying"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	if !stderrors.Is(err, cause) {
		t.Errorf("errors.Is should unwrap to cause")
	}
}

func TestRuleValidationErrorUnwrap(t *testing.T) {
	cause := stderrors.New("parse error")
	err := &RuleValidationError{Message: "bad rule", Cause: cause}
	if !stderrors.Is(err, cause) {
		t.Errorf("errors.Is should unwrap to cause")
	}
}

func TestLockfileErrorNoCause(t *testing.T) {
	err := &LockfileError{Message: "stale"}
	if got, want := err.Error(), "lockfile: stale"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEvidenceErrorFormat(t *testing.T) {
	err := &EvidenceError{Message: "missing path"}
	if got, want := err.Error(), "evidence: missing path"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestReportErrorFormat(t *testing.T) {
	err := &ReportError{Message: "schema drift"}
	if got, want := err.Error(), "report: schema drift"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPresetErrorFormat(t *testing.T) {
	err := &PresetError{Message: "malformed"}
	if got, want := err.Error(), "preset: malformed"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPresetNotFoundError(t *testing.T) {
	err := &PresetNotFoundError{Name: "mypreset"}
	if got, want := err.Error(), `preset not found: "mypreset"`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestPresetNotFoundCanBeTypeAsserted(t *testing.T) {
	var err error = &PresetNotFoundError{Name: "x"}
	var pnf *PresetNotFoundError
	if !stderrors.As(err, &pnf) {
		t.Error("expected errors.As to match *PresetNotFoundError")
	}
	if pnf.Name != "x" {
		t.Errorf("got Name=%q, want %q", pnf.Name, "x")
	}
}

func TestRepoBoundaryError(t *testing.T) {
	err := &RepoBoundaryError{Path: "/outside", RepoRoot: "/repo"}
	want := `path "/outside" escapes repo root "/repo"`
	if got := err.Error(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestGitErrorWithCause(t *testing.T) {
	cause := stderrors.New("exit 128")
	err := &GitError{Message: "diff failed", Cause: cause}
	if got, want := err.Error(), "git: diff failed: exit 128"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTypedErrorsExposeNoCauseBranchesAndUnwrap(t *testing.T) {
	cause := stderrors.New("root")
	cases := []struct {
		name          string
		err           error
		wantNoCause   string
		wantWithCause string
		wrap          func(error) error
	}{
		{
			name:          "rule-validation",
			err:           &RuleValidationError{Message: "bad rule"},
			wantNoCause:   "rule validation: bad rule",
			wantWithCause: "rule validation: bad rule: root",
			wrap:          func(c error) error { return &RuleValidationError{Message: "bad rule", Cause: c} },
		},
		{
			name:          "lockfile",
			err:           &LockfileError{Message: "stale"},
			wantNoCause:   "lockfile: stale",
			wantWithCause: "lockfile: stale: root",
			wrap:          func(c error) error { return &LockfileError{Message: "stale", Cause: c} },
		},
		{
			name:          "evidence",
			err:           &EvidenceError{Message: "missing path"},
			wantNoCause:   "evidence: missing path",
			wantWithCause: "evidence: missing path: root",
			wrap:          func(c error) error { return &EvidenceError{Message: "missing path", Cause: c} },
		},
		{
			name:          "report",
			err:           &ReportError{Message: "schema drift"},
			wantNoCause:   "report: schema drift",
			wantWithCause: "report: schema drift: root",
			wrap:          func(c error) error { return &ReportError{Message: "schema drift", Cause: c} },
		},
		{
			name:          "preset",
			err:           &PresetError{Message: "malformed"},
			wantNoCause:   "preset: malformed",
			wantWithCause: "preset: malformed: root",
			wrap:          func(c error) error { return &PresetError{Message: "malformed", Cause: c} },
		},
		{
			name:          "git",
			err:           &GitError{Message: "diff failed"},
			wantNoCause:   "git: diff failed",
			wantWithCause: "git: diff failed: root",
			wrap:          func(c error) error { return &GitError{Message: "diff failed", Cause: c} },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.wantNoCause {
				t.Fatalf("no-cause error mismatch: got %q want %q", got, tc.wantNoCause)
			}
			wrapped := tc.wrap(cause)
			if got := wrapped.Error(); got != tc.wantWithCause {
				t.Fatalf("with-cause error mismatch: got %q want %q", got, tc.wantWithCause)
			}
			if !stderrors.Is(wrapped, cause) {
				t.Fatalf("expected %T to unwrap to root cause", wrapped)
			}
		})
	}
}
