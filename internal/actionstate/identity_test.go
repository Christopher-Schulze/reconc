package actionstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIdentityConstructionIsFramedKeyedAndDomainSeparated(t *testing.T) {
	t.Parallel()
	first := testIdentityKey(t, "a")
	second := testIdentityKey(t, "b")
	identities := []string{
		first.Identity(DomainServer, []byte("ab"), []byte("c")),
		first.Identity(DomainServer, []byte("a"), []byte("bc")),
		first.Identity(DomainRepository, []byte("ab"), []byte("c")),
		second.Identity(DomainServer, []byte("ab"), []byte("c")),
	}
	seen := map[string]bool{}
	for _, identity := range identities {
		if seen[identity] || !strings.HasPrefix(identity, "hmac-sha256:v1:") {
			t.Fatalf("identity collision or invalid format: %#v", identities)
		}
		seen[identity] = true
	}
	if first.Identity("unknown", []byte("value")) != "" {
		t.Fatal("unknown HMAC domain was accepted")
	}
}

func TestObserveServerRetainsNoEnvironmentOrArgvValues(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	executable := filepath.Join(root, "worker")
	if err := os.WriteFile(executable, []byte("worker-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	observed, err := ObserveServer(
		testIdentityKey(t, "c"), executable, []string{"--profile", "private-profile"}, root,
		[]EnvironmentBinding{{Name: "SERVICE_TOKEN", Value: "secret-value"}, {Name: "REGION", Value: "eu"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if observed.ExecutableDigest == "" || observed.ServerIdentity == "" ||
		observed.Environment[0].Name != "REGION" || observed.Environment[1].Name != "SERVICE_TOKEN" {
		t.Fatalf("observed server = %#v", observed)
	}
	body, err := json.Marshal(observed)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"private-profile", "secret-value", `"eu"`} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("observed identity exposed %q: %s", forbidden, body)
		}
	}
	if strings.Contains(string(body), root) || strings.Contains(string(body), executable) {
		t.Fatalf("observed identity serialized raw filesystem paths: %s", body)
	}
}

func TestObservedServerValidationRejectsEveryComponentMutation(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "worker")
	if err := os.WriteFile(executable, []byte("worker-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	key := testIdentityKey(t, "h")
	observed, err := ObserveServer(
		key, executable, []string{"--profile", "private-profile"}, root,
		[]EnvironmentBinding{{Name: "SECOND", Value: "two"}, {Name: "FIRST", Value: "one"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := observed.Validate(key); err != nil {
		t.Fatalf("fresh observation failed validation: %v", err)
	}

	otherKey := testIdentityKey(t, "i")
	tests := []struct {
		name   string
		mutate func(*ObservedServer)
	}{
		{name: "executable digest", mutate: func(server *ObservedServer) {
			server.ExecutableDigest = "sha256:" + strings.Repeat("1", 64)
		}},
		{name: "argv identity", mutate: func(server *ObservedServer) {
			server.ArgvIdentity = otherKey.Identity(DomainArgv, []byte("argv"))
		}},
		{name: "working directory identity", mutate: func(server *ObservedServer) {
			server.WorkingDirIdentity = otherKey.Identity(DomainWorkingDirectory, []byte("cwd"))
		}},
		{name: "nil environment names", mutate: func(server *ObservedServer) {
			server.EnvironmentNames = nil
		}},
		{name: "nil environment", mutate: func(server *ObservedServer) {
			server.Environment = nil
		}},
		{name: "environment name mismatch", mutate: func(server *ObservedServer) {
			server.EnvironmentNames[0] = "OTHER"
		}},
		{name: "environment order", mutate: func(server *ObservedServer) {
			server.Environment[0], server.Environment[1] = server.Environment[1], server.Environment[0]
			server.EnvironmentNames[0], server.EnvironmentNames[1] = server.EnvironmentNames[1], server.EnvironmentNames[0]
		}},
		{name: "environment entry identity", mutate: func(server *ObservedServer) {
			server.Environment[0].Identity = otherKey.Identity(DomainEnvironment, []byte("FIRST"), []byte("one"))
		}},
		{name: "environment identity", mutate: func(server *ObservedServer) {
			server.EnvironmentIdentity = otherKey.Identity(DomainEnvironmentName, []byte("FIRST"), []byte("SECOND"))
		}},
		{name: "server identity", mutate: func(server *ObservedServer) {
			server.ServerIdentity = otherKey.Identity(DomainServer, []byte("server"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutated := observed
			mutated.EnvironmentNames = append([]string(nil), observed.EnvironmentNames...)
			mutated.Environment = append([]ObservedEnvironment(nil), observed.Environment...)
			test.mutate(&mutated)
			if err := mutated.Validate(key); err == nil {
				t.Fatal("mutated downstream server observation was accepted")
			}
		})
	}
}

func TestObserveRepositoryCanonicalizesAliases(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	alias := filepath.Join(filepath.Dir(root), "repo-alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(alias) })
	key := testIdentityKey(t, "d")
	resolved, identity, err := ObserveRepository(key, root)
	if err != nil {
		t.Fatal(err)
	}
	aliasResolved, aliasIdentity, err := ObserveRepository(key, alias)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != aliasResolved || identity != aliasIdentity {
		t.Fatalf("alias split repository identity: %q/%q != %q/%q", resolved, identity, aliasResolved, aliasIdentity)
	}
}

func TestRandomCallIDMatchesCanonicalContract(t *testing.T) {
	t.Parallel()
	id, err := randomID("act_", strings.NewReader(strings.Repeat("\x00", 26)))
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 30 || id != "act_"+strings.Repeat("a", 26) {
		t.Fatalf("call ID = %q", id)
	}
}

func TestNewRandomCallIDMatchesCanonicalContract(t *testing.T) {
	t.Parallel()
	id, err := NewRandomCallID()
	if err != nil {
		t.Fatal(err)
	}
	if !validCallID(id) {
		t.Fatalf("random call ID = %q", id)
	}
}

func TestObserveServerRejectsEveryBoundedIdentityViolation(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "worker")
	if err := os.WriteFile(executable, []byte("worker"), 0o700); err != nil {
		t.Fatal(err)
	}
	key := testIdentityKey(t, "e")
	tests := []struct {
		name        string
		argv        []string
		environment []EnvironmentBinding
	}{
		{name: "argv NUL", argv: []string{"bad\x00value"}},
		{name: "argv bytes", argv: []string{strings.Repeat("a", MaxServerArgvBytes+1)}},
		{name: "argv values", argv: make([]string, MaxServerArgvValues)},
		{name: "environment control", environment: []EnvironmentBinding{{Name: "BAD\nNAME", Value: "x"}}},
		{name: "environment duplicate", environment: []EnvironmentBinding{{Name: "A", Value: "x"}, {Name: "A", Value: "y"}}},
		{name: "environment value NUL", environment: []EnvironmentBinding{{Name: "A", Value: "x\x00y"}}},
		{name: "environment entries", environment: make([]EnvironmentBinding, MaxEnvironmentBindings+1)},
		{name: "environment bytes", environment: []EnvironmentBinding{{Name: "A", Value: strings.Repeat("x", MaxEnvironmentBindingBytes)}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ObserveServer(key, executable, test.argv, root, test.environment); err == nil {
				t.Fatal("invalid server identity input was accepted")
			}
		})
	}
}

func TestObserveServerRejectsOversizedExecutableWithoutReadingIt(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "oversized")
	file, err := os.OpenFile(executable, os.O_CREATE|os.O_WRONLY, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(MaxExecutableBytes + 1); err != nil {
		closeErr := file.Close()
		t.Fatalf("truncate executable: %v; close: %v", err, closeErr)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ObserveServer(testIdentityKey(t, "f"), executable, nil, root, nil); err == nil {
		t.Fatal("oversized executable was accepted")
	}
}

func TestCredentialIdentityIsBounded(t *testing.T) {
	t.Parallel()
	key := testIdentityKey(t, "g")
	tests := []struct {
		name  string
		label string
		value []byte
		valid bool
	}{
		{name: "valid", label: "database", value: []byte("secret"), valid: true},
		{name: "empty", label: "database"},
		{name: "invalid label", label: "Database", value: []byte("secret")},
		{name: "oversized", label: "database", value: make([]byte, MaxCredentialIdentityBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identity, err := CredentialIdentity(key, test.label, test.value)
			if (err == nil) != test.valid || test.valid && !identityUsesKey(identity, key.ID()) {
				t.Fatalf("CredentialIdentity = %q, %v, valid=%t", identity, err, test.valid)
			}
		})
	}
}
