package actionstate

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actionapproval"
	"reconc.dev/reconc/internal/pathidentity"
)

func TestLoadApprovalAuthorityRegistryAcceptsOnlyPrivateExternalCanonicalFile(t *testing.T) {
	repository := t.TempDir()
	operatorRoot := privateTestHome(t)
	body := approvalRegistryBody(t)
	path := filepath.Join(operatorRoot, "approval-authorities.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadApprovalAuthorityRegistry(path, repository)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := pathidentity.ResolveExisting(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Path() != resolved || loaded.compiled() == nil || loaded.Identity() != loaded.compiled().Identity() ||
		!action.ValidSHA256Identity(loaded.Identity()) {
		t.Fatalf("loaded approval registry = %#v", loaded)
	}

	inside := filepath.Join(repository, "approval-authorities.json")
	if err := os.WriteFile(inside, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadApprovalAuthorityRegistry(inside, repository); err == nil {
		t.Fatal("repository-owned approval authority registry was accepted")
	}

	whitespace := filepath.Join(operatorRoot, "non-canonical.json")
	if err := os.WriteFile(whitespace, append([]byte(" "), body...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadApprovalAuthorityRegistry(whitespace, repository); err == nil {
		t.Fatal("non-canonical approval authority registry was accepted")
	}
}

func TestLoadApprovalAuthorityRegistryRejectsFilesystemSubstitutionAndBounds(t *testing.T) {
	repository := t.TempDir()
	operatorRoot := privateTestHome(t)
	body := approvalRegistryBody(t)
	target := filepath.Join(operatorRoot, "target.json")
	if err := os.WriteFile(target, body, 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(operatorRoot, "link.json")
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Logf("symlink privilege unavailable: %v", err)
		} else {
			t.Fatal(err)
		}
	} else if _, err := LoadApprovalAuthorityRegistry(link, repository); err == nil {
		t.Fatal("symlink approval authority registry was accepted")
	}

	oversized := filepath.Join(operatorRoot, "oversized.json")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte{'x'}, actionapproval.MaxAuthorityRegistryBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadApprovalAuthorityRegistry(oversized, repository); err == nil {
		t.Fatal("oversized approval authority registry was accepted")
	}

	if runtime.GOOS != "windows" {
		public := filepath.Join(operatorRoot, "public.json")
		if err := os.WriteFile(public, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadApprovalAuthorityRegistry(public, repository); err == nil {
			t.Fatal("publicly readable approval authority registry was accepted")
		}
	}
}

func approvalRegistryBody(t testing.TB) []byte {
	t.Helper()
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x51}, ed25519.SeedSize))
	registry := actionapproval.Registry{
		Schema: actionapproval.RegistrySchema, FormatVersion: actionapproval.FormatVersion,
		Authorities: []actionapproval.Authority{{
			ID:         "security-primary",
			PublicKey:  base64.RawURLEncoding.EncodeToString(privateKey.Public().(ed25519.PublicKey)),
			ActiveFrom: now.Format(time.RFC3339Nano),
		}},
		AuthorityPolicies: []actionapproval.AuthorityPolicy{{
			ID: "production-writes", AuthorityKeyIDs: []string{"security-primary"},
		}},
	}
	raw, err := json.Marshal(registry)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := action.ParseObjectJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	body, err := parsed.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), " ") {
		t.Fatal("approval registry helper produced non-canonical whitespace")
	}
	return body
}
