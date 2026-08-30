package bootstrap

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncGitOutputReturnsExactCommandResult(t *testing.T) {
	repo := t.TempDir()
	command := exec.Command("git", "init", "--quiet", repo)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}
	output, err := syncGitOutput(repo, nil, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		t.Fatal(err)
	}
	if output != "true" {
		t.Fatalf("sync git output = %q", output)
	}
}

func TestRepositorySyncResolutionRejectsInvalidRequestsWithoutMutation(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
	managed := receipt.ManagedFiles[firstHarnessManagedFile(t, receipt)]
	target := filepath.Join(repo, filepath.FromSlash(managed.Path))
	if err := os.WriteFile(target, []byte("user drift\n"), os.FileMode(managed.Mode)); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	unchangedPath := ""
	for _, action := range plan.Actions {
		if action.State == SyncUnchanged {
			unchangedPath = action.Path
			break
		}
	}
	if unchangedPath == "" {
		t.Fatal("resolution fixture has no unchanged action")
	}
	binaryInputs := &BinarySelection{}
	tests := []struct {
		name    string
		request SyncResolutionRequest
		version string
		want    string
	}{
		{
			name: "invalid plan",
			request: SyncResolutionRequest{
				Path: managed.Path, Strategy: SyncUseTarget,
			},
			version: syncTestVersion,
			want:    "unsupported repository sync plan",
		},
		{
			name: "digest mismatch",
			request: SyncResolutionRequest{
				Plan: plan, ExactDigest: strings.Repeat("0", 64),
				Path: managed.Path, Strategy: SyncUseTarget,
			},
			version: syncTestVersion,
			want:    "digest mismatch",
		},
		{
			name: "running version mismatch",
			request: SyncResolutionRequest{
				Plan: plan, ExactDigest: plan.PlanDigest,
				Path: managed.Path, Strategy: SyncUseTarget,
			},
			version: "9.9.9",
			want:    "not the running",
		},
		{
			name: "unknown action",
			request: SyncResolutionRequest{
				Plan: plan, ExactDigest: plan.PlanDigest,
				Path: "missing", Strategy: SyncUseTarget,
			},
			version: syncTestVersion,
			want:    "has no action",
		},
		{
			name: "unchanged action",
			request: SyncResolutionRequest{
				Plan: plan, ExactDigest: plan.PlanDigest,
				Path: unchangedPath, Strategy: SyncUseTarget,
			},
			version: syncTestVersion,
			want:    "does not require explicit resolution",
		},
		{
			name: "unknown strategy",
			request: SyncResolutionRequest{
				Plan: plan, ExactDigest: plan.PlanDigest,
				Path: managed.Path, Strategy: SyncResolutionStrategy("unknown"),
			},
			version: syncTestVersion,
			want:    "unsupported repository sync resolution strategy",
		},
		{
			name: "keep current with binary inputs",
			request: SyncResolutionRequest{
				Plan: plan, ExactDigest: plan.PlanDigest,
				Path: managed.Path, Strategy: SyncKeepCurrent, Binary: binaryInputs,
			},
			version: syncTestVersion,
			want:    "--binary inputs are valid only",
		},
		{
			name: "use target with binary inputs",
			request: SyncResolutionRequest{
				Plan: plan, ExactDigest: plan.PlanDigest,
				Path: managed.Path, Strategy: SyncUseTarget, Binary: binaryInputs,
			},
			version: syncTestVersion,
			want:    "--binary inputs are valid only",
		},
		{
			name: "use binary without binary action",
			request: SyncResolutionRequest{
				Plan: plan, ExactDigest: plan.PlanDigest,
				Path: managed.Path, Strategy: SyncUseBinary,
			},
			version: syncTestVersion,
			want:    "requires a binary action",
		},
	}
	before := snapshotRegularFiles(t, repo)
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			report, resolveErr := ResolveRepositorySync(test.request, test.version)
			if resolveErr == nil || !strings.Contains(resolveErr.Error(), test.want) {
				t.Fatalf("resolution error = %v, want substring %q", resolveErr, test.want)
			}
			if report.Status != SyncRefused || !strings.Contains(report.NextAction, test.want) {
				t.Fatalf("resolution report = %+v", report)
			}
			if after := snapshotRegularFiles(t, repo); !equalStringMaps(before, after) {
				t.Fatalf("refused resolution mutated repository\nbefore=%v\nafter=%v", before, after)
			}
		})
	}
}

func TestRepositorySyncCannotKeepInvalidGeneratedPolicy(t *testing.T) {
	repo, _ := initializeSyncFixture(t, ProfileGoverned)
	policyPath := ".reconc/policy.lock.json"
	target := filepath.Join(repo, filepath.FromSlash(policyPath))
	invalid := []byte("{}\n")
	if err := os.WriteFile(target, invalid, 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	action := syncActionForPath(t, plan, policyPath)
	if action.State != SyncUserDrift {
		t.Fatalf("invalid policy action = %+v", action)
	}
	report, err := ResolveRepositorySync(SyncResolutionRequest{
		Plan: plan, ExactDigest: plan.PlanDigest, Path: policyPath,
		Strategy: SyncKeepCurrent,
	}, syncTestVersion)
	if err == nil || !strings.Contains(err.Error(), "cannot keep an invalid generated policy lock") {
		t.Fatalf("invalid policy resolution = %+v err=%v", report, err)
	}
	after, readErr := os.ReadFile(target)
	if readErr != nil || string(after) != string(invalid) {
		t.Fatalf("invalid policy changed = %q err=%v", after, readErr)
	}
}

func TestRepositorySyncResolutionRollsBackPublicationFailure(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
	managed := receipt.ManagedFiles[firstHarnessManagedFile(t, receipt)]
	target := filepath.Join(repo, filepath.FromSlash(managed.Path))
	drift := []byte("preserve this drift after rollback\n")
	if err := os.WriteFile(target, drift, os.FileMode(managed.Mode)); err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSyncPlan(repo, syncTestVersion)
	if err != nil {
		t.Fatal(err)
	}
	before := snapshotRegularFiles(t, repo)
	report, err := resolveRepositorySync(SyncResolutionRequest{
		Plan: plan, ExactDigest: plan.PlanDigest, Path: managed.Path,
		Strategy: SyncUseTarget,
	}, syncTestVersion, syncResolutionOptions{failAfter: 1})
	if err == nil || !strings.Contains(err.Error(), "injected repository sync failure") {
		t.Fatalf("injected resolution failure = %+v err=%v", report, err)
	}
	if report.Status != SyncRolledBack || !containsString(report.RolledBack, managed.Path) {
		t.Fatalf("resolution rollback report = %+v", report)
	}
	if after := snapshotRegularFiles(t, repo); !equalStringMaps(before, after) {
		t.Fatalf("resolution rollback changed repository\nbefore=%v\nafter=%v", before, after)
	}
	journal := filepath.Join(repo, filepath.FromSlash(repositorySyncTransactionRelativePath))
	if _, statErr := os.Lstat(journal); !os.IsNotExist(statErr) {
		t.Fatalf("resolution rollback left transaction journal: %v", statErr)
	}
}

func TestRepositorySyncResolutionMutationRejectsUnsafeTargets(t *testing.T) {
	root := t.TempDir()
	created, err := resolutionMutation(root, "nested/new.txt", 0o644, []byte("new\n"))
	if err != nil || !created.Created {
		t.Fatalf("missing target mutation = %+v err=%v", created, err)
	}
	regularPath := filepath.Join(root, "regular.txt")
	if err := os.WriteFile(regularPath, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	replacement, err := resolutionMutation(root, "regular.txt", 0o644, []byte("new\n"))
	if err != nil || replacement.Created {
		t.Fatalf("regular target mutation = %+v err=%v", replacement, err)
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := resolutionMutation(root, "directory", 0o644, []byte("new\n")); err == nil ||
		!strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("directory mutation error = %v", err)
	}
	if _, err := resolutionMutation(root, "../escape", 0o644, []byte("new\n")); err == nil {
		t.Fatal("resolution mutation accepted traversal")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(regularPath, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := resolutionMutation(root, "link", 0o644, []byte("new\n")); err == nil ||
		!strings.Contains(err.Error(), "non-regular") {
		t.Fatalf("symlink mutation error = %v", err)
	}
}

func TestRepositorySyncOwnershipTransitionsAreComponentExact(t *testing.T) {
	t.Run("release hook pair", func(t *testing.T) {
		receipt := &RepositoryReceipt{
			Hooks: []string{"claude", "cursor"},
			ManagedFiles: []ManagedFile{
				{Path: ".claude/hooks/reconc.sh", Component: "hook:claude"},
				{Path: ".cursor/hooks/reconc.sh", Component: "hook:cursor"},
			},
			ManagedBlocks: []ManagedBlock{
				{Path: ".claude/settings.json", Component: "hook-activation:claude"},
				{Path: ".cursor/settings.json", Component: "hook-activation:cursor"},
			},
			GeneratedArtifacts: []GeneratedArtifact{
				{Path: ".claude/hooks/reconc.sh"},
				{Path: ".reconc/policy.lock.json"},
			},
			UserOwnedPaths: []string{},
		}
		releaseRepositoryOwnership(receipt, SyncAction{
			Path: ".claude/hooks/reconc.sh", Component: "hook:claude",
		})
		if len(receipt.ManagedFiles) != 1 || receipt.ManagedFiles[0].Component != "hook:cursor" ||
			len(receipt.ManagedBlocks) != 1 || receipt.ManagedBlocks[0].Component != "hook-activation:cursor" ||
			len(receipt.GeneratedArtifacts) != 1 ||
			containsString(receipt.Hooks, "claude") || !containsString(receipt.Hooks, "cursor") ||
			!containsString(receipt.UserOwnedPaths, ".claude/hooks/reconc.sh") ||
			!containsString(receipt.UserOwnedPaths, ".claude/settings.json") {
			t.Fatalf("released hook ownership = %+v", receipt)
		}
	})

	t.Run("release one harness pack", func(t *testing.T) {
		receipt := &RepositoryReceipt{HarnessPacks: []HarnessPackIdentity{
			{Name: "advanced"}, {Name: "other"},
		}}
		releaseRepositoryOwnership(receipt, SyncAction{
			Path:      "tools/reconc/harness/advanced/main.go",
			Component: "harness-pack:advanced@1.0.0",
		})
		if len(receipt.HarnessPacks) != 1 || receipt.HarnessPacks[0].Name != "other" {
			t.Fatalf("released harness packs = %+v", receipt.HarnessPacks)
		}
	})

	t.Run("assign generated policy and binary", func(t *testing.T) {
		receipt := &RepositoryReceipt{
			UserOwnedPaths: []string{".reconc/policy.lock.json", "tools/reconc/dist/reconc-linux-amd64"},
		}
		policy := []byte("{\"format_version\":\"1\"}\n")
		if err := assignRepositoryOwnership(receipt, SyncAction{
			Path: ".reconc/policy.lock.json", Component: "policy-lock", Mode: 0o644,
		}, policy, syncTestVersion, false); err != nil {
			t.Fatal(err)
		}
		binary := []byte("binary\n")
		if err := assignRepositoryOwnership(receipt, SyncAction{
			Path: "tools/reconc/dist/reconc-linux-amd64", Component: "binary", Mode: 0o755,
		}, binary, syncTestVersion, true); err != nil {
			t.Fatal(err)
		}
		if len(receipt.GeneratedArtifacts) != 1 ||
			receipt.GeneratedArtifacts[0].SHA256 != bytesSHA256(policy) ||
			len(receipt.ManagedFiles) != 1 ||
			receipt.ManagedFiles[0].Component != "binary@"+syncTestVersion ||
			len(receipt.UserOwnedPaths) != 0 {
			t.Fatalf("assigned policy and binary ownership = %+v", receipt)
		}
	})

	t.Run("assign managed block", func(t *testing.T) {
		const start = "<!-- reconc:start -->"
		const end = "<!-- reconc:end -->"
		body := []byte("before\n" + start + "\nmanaged\n" + end + "\nafter\n")
		receipt := &RepositoryReceipt{
			ManagedBlocks: []ManagedBlock{{
				Path: "AGENTS.md", BlockStart: start, BlockEnd: end,
				Component: "hook-activation:claude",
			}},
			UserOwnedPaths: []string{"AGENTS.md"},
		}
		if err := assignRepositoryOwnership(receipt, SyncAction{
			Path: "AGENTS.md", Component: "hook-activation:claude", Mode: 0o644,
		}, body, syncTestVersion, false); err != nil {
			t.Fatal(err)
		}
		managed, err := extractManagedBlock(body, start, end)
		if err != nil {
			t.Fatal(err)
		}
		if len(receipt.ManagedBlocks) != 1 ||
			receipt.ManagedBlocks[0].ManagedSHA256 != bytesSHA256(managed) ||
			receipt.ManagedBlocks[0].WholeFileSHA256 != bytesSHA256(body) ||
			len(receipt.UserOwnedPaths) != 0 {
			t.Fatalf("assigned managed block ownership = %+v", receipt)
		}
	})
}

func TestRepositorySyncCrossPlatformBinaryPlanningIsFailClosed(t *testing.T) {
	root := t.TempDir()
	name, err := StableBinaryName("linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	relative := filepath.ToSlash(filepath.Join("tools", "reconc", "dist", name))
	target := filepath.Join(root, filepath.FromSlash(relative))
	body := []byte("approved cross-platform binary\n")
	owned := ManagedFile{
		Path: relative, Mode: 0o755, SHA256: bytesSHA256(body),
		Component: "binary@" + syncTestVersion, Ownership: "file",
	}

	approved, err := planApprovedCrossPlatformBinary(root, owned, syncTestVersion)
	if err != nil || approved.State != SyncIncompatible ||
		!strings.Contains(approved.Reason, "is missing") || approved.DesiredSHA256 != "" {
		t.Fatalf("missing approved binary action = %+v err=%v", approved, err)
	}
	unapproved, err := planCrossPlatformBinary(root, owned, "linux", "amd64")
	if err != nil || unapproved.State != SyncIncompatible ||
		!strings.Contains(unapproved.Reason, "is missing") {
		t.Fatalf("missing unapproved binary action = %+v err=%v", unapproved, err)
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	approved, err = planApprovedCrossPlatformBinary(root, owned, syncTestVersion)
	if err != nil || !strings.Contains(approved.Reason, "not a real regular file") {
		t.Fatalf("non-regular approved binary action = %+v err=%v", approved, err)
	}
	unapproved, err = planCrossPlatformBinary(root, owned, "linux", "amd64")
	if err != nil || !strings.Contains(unapproved.Reason, "not a real regular file") {
		t.Fatalf("non-regular unapproved binary action = %+v err=%v", unapproved, err)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(target, []byte("drift\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	approved, err = planApprovedCrossPlatformBinary(root, owned, syncTestVersion)
	if err != nil || approved.State != SyncIncompatible ||
		!strings.Contains(approved.Reason, "has drifted") || approved.DesiredSHA256 != "" {
		t.Fatalf("drifted approved binary action = %+v err=%v", approved, err)
	}
	unapproved, err = planCrossPlatformBinary(root, owned, "linux", "amd64")
	if err != nil || !strings.Contains(unapproved.Reason, "has drifted") ||
		unapproved.CurrentSHA256 != bytesSHA256([]byte("drift\n")) {
		t.Fatalf("drifted unapproved binary action = %+v err=%v", unapproved, err)
	}

	if err := os.WriteFile(target, body, 0o755); err != nil {
		t.Fatal(err)
	}
	approved, err = planApprovedCrossPlatformBinary(root, owned, syncTestVersion)
	if err != nil || approved.State != SyncUnchanged ||
		approved.DesiredSHA256 != owned.SHA256 || approved.CurrentSHA256 != owned.SHA256 {
		t.Fatalf("exact approved binary action = %+v err=%v", approved, err)
	}
	unapproved, err = planCrossPlatformBinary(root, owned, "linux", "amd64")
	if err != nil || unapproved.State != SyncIncompatible ||
		strings.Contains(unapproved.Reason, "has drifted") ||
		unapproved.CurrentSHA256 != owned.SHA256 {
		t.Fatalf("exact unapproved binary action = %+v err=%v", unapproved, err)
	}
}

func TestRepositorySyncTargetMaterializationIsDigestBound(t *testing.T) {
	repo, receipt := initializeSyncFixture(t, ProfileAdvanced)
	managed := receipt.ManagedFiles[firstHarnessManagedFile(t, receipt)]

	if _, err := resolvePlannedTargetBytes(repo, receipt, SyncAction{
		Path: managed.Path,
	}, syncTestVersion); err == nil || !strings.Contains(err.Error(), "no materializable target") {
		t.Fatalf("missing target digest error = %v", err)
	}
	if _, err := resolvePlannedTargetBytes(repo, receipt, SyncAction{
		Path: "missing", DesiredSHA256: strings.Repeat("0", 64),
	}, syncTestVersion); err == nil || !strings.Contains(err.Error(), "target artifact no longer resolves") {
		t.Fatalf("missing target artifact error = %v", err)
	}
	if _, err := resolvePlannedTargetBytes(repo, receipt, SyncAction{
		Path: managed.Path, DesiredSHA256: strings.Repeat("0", 64),
	}, syncTestVersion); err == nil || !strings.Contains(err.Error(), "target bytes drifted") {
		t.Fatalf("target digest drift error = %v", err)
	}

	const start = "<!-- reconc:start -->"
	const end = "<!-- reconc:end -->"
	blockReceipt := &RepositoryReceipt{
		ManagedBlocks: []ManagedBlock{{
			Path: "AGENTS.md", BlockStart: start, BlockEnd: end,
			Component: "hook-activation:claude",
		}},
		UserOwnedPaths: []string{"AGENTS.md"},
	}
	if err := assignRepositoryOwnership(blockReceipt, SyncAction{
		Path: "AGENTS.md", Component: "hook-activation:claude", Mode: 0o644,
	}, []byte("markers missing\n"), syncTestVersion, false); err == nil ||
		!strings.Contains(err.Error(), "capture resolved managed block") {
		t.Fatalf("invalid managed block assignment error = %v", err)
	}
}

func TestRepositorySyncTransactionBuilderRejectsUnsafeMutations(t *testing.T) {
	root := t.TempDir()
	digest := strings.Repeat("a", 64)
	existing := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(existing, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	validCreate := syncMutation{
		Path: "new.txt", Mode: 0o644, After: []byte("after\n"), Created: true,
	}
	tests := []struct {
		name       string
		version    string
		planDigest string
		mutations  []syncMutation
		want       string
	}{
		{
			name: "missing identity", version: "", planDigest: digest,
			mutations: []syncMutation{validCreate}, want: "identity is invalid",
		},
		{
			name: "duplicate path", version: syncTestVersion, planDigest: digest,
			mutations: []syncMutation{validCreate, validCreate}, want: "repeats path",
		},
		{
			name: "traversal", version: syncTestVersion, planDigest: digest,
			mutations: []syncMutation{{
				Path: "../escape", Mode: 0o644, After: []byte("after\n"), Created: true,
			}}, want: "mutation is invalid",
		},
		{
			name: "invalid mode", version: syncTestVersion, planDigest: digest,
			mutations: []syncMutation{{
				Path: "new.txt", Mode: 0o600, After: []byte("after\n"), Created: true,
			}}, want: "mutation is invalid",
		},
		{
			name: "empty after image", version: syncTestVersion, planDigest: digest,
			mutations: []syncMutation{{
				Path: "new.txt", Mode: 0o644, Created: true,
			}}, want: "mutation is invalid",
		},
		{
			name: "create target exists", version: syncTestVersion, planDigest: digest,
			mutations: []syncMutation{{
				Path: "existing.txt", Mode: 0o644, After: []byte("after\n"), Created: true,
			}}, want: "appeared before journaling",
		},
		{
			name: "replacement target missing", version: syncTestVersion, planDigest: digest,
			mutations: []syncMutation{{
				Path: "missing.txt", Mode: 0o644, After: []byte("after\n"),
			}}, want: "inspect repository sync source",
		},
		{
			name: "replacement target non-regular", version: syncTestVersion, planDigest: digest,
			mutations: []syncMutation{{
				Path: "directory", Mode: 0o644, After: []byte("after\n"),
			}}, want: "not a real regular file",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transaction, err := buildRepositorySyncTransaction(
				root, test.version, test.planDigest, test.mutations, false,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("transaction = %+v err=%v, want substring %q", transaction, err, test.want)
			}
		})
	}
}

func TestRepositorySyncTransactionValidationRejectsTampering(t *testing.T) {
	root := t.TempDir()
	base, err := buildRepositorySyncTransaction(
		root,
		syncTestVersion,
		strings.Repeat("a", 64),
		[]syncMutation{{
			Path: "new.txt", Mode: 0o644, After: []byte("after\n"), Created: true,
		}},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		mutate func(*repositorySyncTransaction)
		want   string
	}{
		{
			name: "empty files",
			mutate: func(transaction *repositorySyncTransaction) {
				transaction.Files = []repositorySyncJournalFile{}
			},
			want: "identity is invalid",
		},
		{
			name: "duplicate file",
			mutate: func(transaction *repositorySyncTransaction) {
				transaction.Files = append(transaction.Files, transaction.Files[0])
			},
			want: "journal file is invalid",
		},
		{
			name: "invalid before mode",
			mutate: func(transaction *repositorySyncTransaction) {
				transaction.Files[0].BeforeMode = 0
			},
			want: "journal file is invalid",
		},
		{
			name: "before checksum",
			mutate: func(transaction *repositorySyncTransaction) {
				transaction.Files[0].Before = []byte("tampered\n")
			},
			want: "before image checksum mismatch",
		},
		{
			name: "created before image",
			mutate: func(transaction *repositorySyncTransaction) {
				transaction.Files[0].Before = []byte("impossible\n")
				transaction.Files[0].BeforeSHA256 = bytesSHA256(transaction.Files[0].Before)
			},
			want: "created file has a before image",
		},
		{
			name: "unsorted created directories",
			mutate: func(transaction *repositorySyncTransaction) {
				transaction.CreatedDirectories = []string{"z", "a"}
			},
			want: "must be uniquely sorted",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			transaction := *base
			transaction.Files = append([]repositorySyncJournalFile{}, base.Files...)
			transaction.CreatedDirectories = append([]string{}, base.CreatedDirectories...)
			test.mutate(&transaction)
			transaction.JournalDigest = ""
			digest, digestErr := computeRepositorySyncTransactionDigest(&transaction)
			if digestErr != nil {
				t.Fatal(digestErr)
			}
			transaction.JournalDigest = digest
			if err := validateRepositorySyncTransaction(&transaction); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("tampered transaction error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestSyncMigrationOrderingUsesEveryIdentityField(t *testing.T) {
	tests := []struct {
		name  string
		left  SyncMigration
		right SyncMigration
	}{
		{
			name:  "path",
			left:  SyncMigration{Path: "a", Kind: "z", From: "z", To: "z"},
			right: SyncMigration{Path: "b", Kind: "a", From: "a", To: "a"},
		},
		{
			name:  "kind",
			left:  SyncMigration{Path: "a", Kind: "a", From: "z", To: "z"},
			right: SyncMigration{Path: "a", Kind: "b", From: "a", To: "a"},
		},
		{
			name:  "from",
			left:  SyncMigration{Path: "a", Kind: "a", From: "a", To: "z"},
			right: SyncMigration{Path: "a", Kind: "a", From: "b", To: "a"},
		},
		{
			name:  "to",
			left:  SyncMigration{Path: "a", Kind: "a", From: "a", To: "a"},
			right: SyncMigration{Path: "a", Kind: "a", From: "a", To: "b"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if !syncMigrationLess(test.left, test.right) || syncMigrationLess(test.right, test.left) {
				t.Fatalf("migration order left=%+v right=%+v", test.left, test.right)
			}
		})
	}
}
