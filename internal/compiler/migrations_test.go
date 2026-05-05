package compiler

import (
	"strings"
	"testing"
)

func TestMigrateLockfileCurrentVersionIsNoOp(t *testing.T) {
	payload := map[string]interface{}{"format_version": LockfileFormatVersion, "foo": "bar"}
	out, applied, err := MigrateLockfile(payload)
	if err != nil {
		t.Fatalf("MigrateLockfile: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("expected no migrations applied, got %d", len(applied))
	}
	if out["foo"] != "bar" {
		t.Errorf("payload should be unchanged for current version; got %v", out)
	}
}

func TestMigrateLockfileMissingVersionErrors(t *testing.T) {
	payload := map[string]interface{}{"foo": "bar"}
	_, _, err := MigrateLockfile(payload)
	if err == nil {
		t.Fatal("expected error for missing format_version")
	}
	if !strings.Contains(err.Error(), "format_version") {
		t.Errorf("error should mention format_version; got %v", err)
	}
}

func TestMigrateLockfileUnknownVersionErrors(t *testing.T) {
	payload := map[string]interface{}{"format_version": "99"}
	_, _, err := MigrateLockfile(payload)
	if err == nil {
		t.Fatal("expected error for unknown version")
	}
	if !strings.Contains(err.Error(), "no migration registered") {
		t.Errorf("error should mention missing migration; got %v", err)
	}
}

func TestMigrateLockfileAppliesChain(t *testing.T) {
	// Inject a synthetic chain 0 -> 0.5 -> current. We reset via
	// defer to keep the registry clean for other tests.
	orig := Migrations
	defer func() { Migrations = orig }()
	Migrations = []Migration{
		{FromVersion: "0", ToVersion: "0.5", Apply: func(p map[string]interface{}) (map[string]interface{}, error) {
			p["step1"] = true
			return p, nil
		}},
		{FromVersion: "0.5", ToVersion: LockfileFormatVersion, Apply: func(p map[string]interface{}) (map[string]interface{}, error) {
			p["step2"] = true
			return p, nil
		}},
	}

	payload := map[string]interface{}{"format_version": "0"}
	out, applied, err := MigrateLockfile(payload)
	if err != nil {
		t.Fatalf("MigrateLockfile: %v", err)
	}
	if len(applied) != 2 {
		t.Errorf("expected 2 migrations applied, got %d", len(applied))
	}
	if out["step1"] != true || out["step2"] != true {
		t.Errorf("both migrations should have run; got %v", out)
	}
	if out["format_version"] != LockfileFormatVersion {
		t.Errorf("final version should be current; got %v", out["format_version"])
	}
}

func TestMigrateLockfilePropagatesApplyError(t *testing.T) {
	orig := Migrations
	defer func() { Migrations = orig }()
	Migrations = []Migration{
		{FromVersion: "0", ToVersion: LockfileFormatVersion, Apply: func(p map[string]interface{}) (map[string]interface{}, error) {
			return nil, &testErr{msg: "boom"}
		}},
	}
	payload := map[string]interface{}{"format_version": "0"}
	_, _, err := MigrateLockfile(payload)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected apply error to propagate; got %v", err)
	}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
