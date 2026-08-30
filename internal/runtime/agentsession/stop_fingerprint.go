package agentsession

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/runtime"
)

func stopPolicyFingerprint(repoRoot string, state SessionState) string {
	return hashStopPolicyFingerprintInput(stopPolicyFingerprintInputFor(repoRoot, state))
}

func stopPolicyFingerprintInputFor(repoRoot string, state SessionState) stopPolicyFingerprintInput {
	root, err := ResolveRepoRoot(repoRoot)
	if err != nil {
		root = repoRoot
	}
	return stopPolicyFingerprintInputForSnapshot(root, state, stopPolicyGitSnapshotFor(root))
}

func stopPolicyFingerprintInputForSnapshot(root string, state SessionState, gitSnapshot stopPolicyGitSnapshot) stopPolicyFingerprintInput {
	taskSnapshot, err := captureStopTaskSnapshot(root)
	if err != nil {
		return stopPolicyFingerprintInput{
			Version: stopPolicyFingerprintVersion, RepoRoot: root, SessionID: state.SessionID,
			PolicyLockHash:     fileContentHash(filepath.Join(root, policyLockfilePath)),
			PolicySourceDigest: "error:" + err.Error(), TaskStateHash: "error:" + err.Error(),
			GitHead: gitSnapshot.Head, GitStatus: gitSnapshot.Status,
			GitStatusMode: gitSnapshot.StatusMode, GitStatusOK: gitSnapshot.StatusOK,
		}
	}
	return stopPolicyFingerprintInputForSnapshotWithGeneration(root, state, gitSnapshot, taskSnapshot, stopGenerationCapture{})
}

func stopPolicyFingerprintInputForSnapshotWithGeneration(
	root string,
	state SessionState,
	gitSnapshot stopPolicyGitSnapshot,
	taskSnapshot stopTaskSnapshot,
	generation stopGenerationCapture,
) stopPolicyFingerprintInput {
	return stopPolicyFingerprintInputForSnapshotWithScan(root, state, gitSnapshot, taskSnapshot, generation, nil)
}

func stopPolicyFingerprintInputForSnapshotWithScan(
	root string,
	state SessionState,
	gitSnapshot stopPolicyGitSnapshot,
	taskSnapshot stopTaskSnapshot,
	generation stopGenerationCapture,
	scanCache *stopPolicyScanCache,
) stopPolicyFingerprintInput {
	policyDigest := generation.PolicySourceDigest
	policyCount := generation.PolicySourceCount
	if policyDigest == "" {
		var err error
		policyDigest, policyCount, err = stopPolicySourceIdentity(root)
		if err != nil {
			policyDigest = "error:" + err.Error()
		}
	}
	taskHash := generation.TaskStateHash
	if taskHash == "" {
		taskHash = stopTaskSnapshotHash(taskSnapshot)
	}
	dirtyFiles := []gitDirtyFile{}
	if gitSnapshot.StatusOK {
		dirtyFiles = gitDirtyFilesWithScan(root, gitSnapshot.Status, scanCache)
	}
	policyScan := scanCache.get(root, state.WritePaths)
	policyLockHash := policyScan.LockHash
	if policyLockHash == "" {
		policyLockHash = fileContentHash(filepath.Join(root, policyLockfilePath))
	}
	return stopPolicyFingerprintInput{
		Version:            stopPolicyFingerprintVersion,
		RepoRoot:           root,
		SessionID:          state.SessionID,
		PolicyLockHash:     policyLockHash,
		PolicySourceDigest: policyDigest,
		PolicySourceCount:  policyCount,
		TaskStateHash:      taskHash,
		ReportFormat:       runtime.CheckReportFormatVersion,
		SchemaBase:         os.Getenv("RECONC_SCHEMA_BASE_URL"),
		ReadPaths:          sortedUniqueExact(state.ReadPaths),
		WritePaths:         sortedUniqueExact(state.WritePaths),
		WriteEpochs:        cloneWriteEpochs(state.WriteEpochs),
		Commands:           sortedUnique(state.Commands),
		Claims:             sortedUnique(state.Claims),
		CommandResults:     append([]CommandResult{}, state.CommandResults...),
		GitHead:            gitSnapshot.Head,
		GitStatusMode:      gitSnapshot.StatusMode,
		GitStatusOK:        gitSnapshot.StatusOK,
		GitStatus:          gitSnapshot.Status,
		GitDirtyFiles:      dirtyFiles,
		ReconcAuditNoCache: os.Getenv("RECONC_AUDIT_NO_CACHE"),
		PolicyInputs:       stopPolicyInputIdentitiesWithScan(root, policyScan.Paths, scanCache),
	}
}

func hashStopPolicyFingerprintInput(input stopPolicyFingerprintInput) string {
	fingerprint, err := hashStopPolicyFingerprintInputWithError(input)
	if err != nil {
		return "error:" + err.Error()
	}
	return fingerprint
}

func hashStopPolicyFingerprintInputWithError(input stopPolicyFingerprintInput) (string, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal Stop policy fingerprint: %w", err)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

// stopPolicyWritePaths is the exact set require_script rules match their
// when_paths against, so a rule this Stop cannot trigger cannot affect the
// report it would reuse.
func stopPolicyWritePaths(input stopPolicyFingerprintInput) []string {
	return input.WritePaths
}

func stopPolicyFingerprintCacheable(input stopPolicyFingerprintInput) bool {
	return stopPolicyFingerprintCacheableWithScan(input, nil)
}

func stopPolicyFingerprintCacheableWithScan(input stopPolicyFingerprintInput, scanCache *stopPolicyScanCache) bool {
	if !(input.GitStatusOK &&
		!strings.HasPrefix(input.GitHead, "error:") &&
		!strings.HasPrefix(input.PolicyLockHash, "error:") &&
		!strings.HasPrefix(input.PolicySourceDigest, "error:") &&
		!strings.HasPrefix(input.TaskStateHash, "error:") &&
		completionDirtyFilesTrusted(input.GitDirtyFiles)) {
		return false
	}
	for _, policyInput := range input.PolicyInputs {
		if !policyInput.Trusted {
			return false
		}
	}
	// A policy path the compiler cannot name statically cannot be bound into
	// the fingerprint, so the report it produced cannot be revalidated.
	return scanCache.get(input.RepoRoot, stopPolicyWritePaths(input)).Cacheable
}

// stopPolicyLockScan is the bounded static view of the compiled policy that
// Stop caching needs: which repository paths the rules read, and which of them
// can turn a clean report stale from wall-clock time alone.
//
// It binds what the policy names. A require_script body is opaque, so it must
// declare the files it reads through `cache_inputs`; a script that declares
// none keeps its plan off the warm path entirely.
type stopPolicyLockScan struct {
	// LockHash is the exact SHA-256 of the bounded lockfile bytes used for
	// this scan. It is the cache key's content identity and is rechecked at
	// the end of an attempt before any result can be reused or stored.
	LockHash string
	// Cacheable is false when the lock cannot be read or decoded, when a policy
	// path is template-generated, or when an applicable gate has a dynamic
	// authority surface. Completion still binds every concrete path that can be
	// resolved from the exact candidate inputs.
	Cacheable bool
	// Paths are the repository-relative paths the policy names, sorted and
	// deduplicated.
	Paths []string
	// FreshFiles are the age-bounded requirements, used to give a stored
	// report the expiry its inputs imply.
	FreshFiles []stopPolicyFreshFile
	// Assurance contains only native gates on rules reachable from this
	// evaluation's write paths. Completion snapshots evaluate these read-only
	// gates to bind their dynamic authority surfaces and time windows.
	Assurance []policy.AssuranceGate
}

// stopPolicyScanCache is owned by one Stop attempt. It deliberately retains
// only the bounded structural scan and its exact lockfile hash, never the raw
// lockfile bytes or process-global policy data.
type stopPolicyScanCache struct {
	root             string
	writeKey         string
	scan             stopPolicyLockScan
	loaded           bool
	mutated          bool
	dirtyIdentities  map[string]stopCachedDirtyIdentity
	policyIdentities map[string]stopCachedPolicyIdentity
	metrics          stopPolicyAttemptMetrics
}

type stopCachedDirtyIdentity struct {
	generation string
	identity   string
}

type stopCachedPolicyIdentity struct {
	resolved   string
	generation string
	identity   policyInputIdentity
}

type stopPolicyAttemptMetrics struct {
	contentHashReads     int
	contentHashBytes     int64
	thresholdTreeEntries int
}

func (c *stopPolicyScanCache) recordContentHash(size int64) {
	if c == nil || size < 0 {
		return
	}
	c.metrics.contentHashReads++
	c.metrics.contentHashBytes += size
}

func (c *stopPolicyScanCache) get(repoRoot string, writePaths []string) stopPolicyLockScan {
	if c == nil {
		return scanStopPolicyLockfile(repoRoot, writePaths)
	}
	normalized := sortedUniqueExact(writePaths)
	writeKey := strings.Join(normalized, "\x00")
	if c.loaded && c.root == repoRoot && c.writeKey == writeKey {
		return c.scan
	}
	scan := scanStopPolicyLockfile(repoRoot, normalized)
	c.root, c.writeKey, c.scan, c.loaded, c.mutated = repoRoot, writeKey, scan, true, false
	return scan
}

// stable verifies the exact lock bytes used by the cached scan. Callers use
// this as the attempt's post-observation barrier; a changed or unreadable
// lock fails closed instead of allowing a stale scan to authorize reuse.
func (c *stopPolicyScanCache) stable(repoRoot string, writePaths []string) bool {
	if c == nil {
		return false
	}
	scan := c.get(repoRoot, writePaths)
	if !c.loaded || c.root != repoRoot || scan.LockHash == "" {
		return false
	}
	body, err := boundedio.ReadFile(filepath.Join(repoRoot, policyLockfilePath), stopPolicyLockfileScanBound)
	if err != nil || hashBytes(body) != scan.LockHash {
		c.mutated = true
		return false
	}
	return !c.mutated
}

type stopPolicyFreshFile struct {
	Path        string
	MaxAgeHours int
}

// scanStopPolicyLockfile decodes the compiled policy instead of scanning it for
// tokens: a rule message that quotes a kind must not change caching, and a
// check nested in an all_of / any_of / not rule must.
func scanStopPolicyLockfile(repoRoot string, writePaths []string) stopPolicyLockScan {
	if repoRoot == "" {
		// Fingerprint unit tests exercise cacheability without a repo root;
		// production Stop always supplies a resolved root before caching.
		return stopPolicyLockScan{Cacheable: true}
	}
	body, err := boundedio.ReadFile(filepath.Join(repoRoot, policyLockfilePath), stopPolicyLockfileScanBound)
	if err != nil {
		// Unreadable lock already fails evaluation; treat uncertainty as
		// non-cacheable so we never reuse a report we cannot revalidate.
		return stopPolicyLockScan{}
	}
	scan := stopPolicyLockScan{Cacheable: true, LockHash: hashBytes(body)}
	var lock struct {
		Rules []stopPolicyLockRule `json:"rules"`
	}
	if err := json.Unmarshal(body, &lock); err != nil {
		// A lock we cannot decode is a lock we cannot reason about.
		scan.Cacheable = false
		return scan
	}
	paths := map[string]struct{}{}
	collect := func(path string, maxAgeHours int, fresh bool, captures []map[string]string) {
		if path == "" {
			return
		}
		if runtime.HasTemplateVars(path) {
			scan.Cacheable = false
			for _, capture := range captures {
				resolved, substituteErr := runtime.SubstituteTemplate(path, capture)
				if substituteErr != nil {
					continue
				}
				paths[resolved] = struct{}{}
				if fresh && maxAgeHours > 0 {
					scan.FreshFiles = append(scan.FreshFiles, stopPolicyFreshFile{Path: resolved, MaxAgeHours: maxAgeHours})
				}
			}
			return
		}
		paths[path] = struct{}{}
		if fresh && maxAgeHours > 0 {
			scan.FreshFiles = append(scan.FreshFiles, stopPolicyFreshFile{Path: path, MaxAgeHours: maxAgeHours})
		}
	}
	dynamicInputRule := false
	for _, rule := range lock.Rules {
		// A require_script rule matches its when_paths against the session's
		// write paths. One this Stop cannot trigger runs no script, so it can
		// neither contribute to the report nor invalidate its reuse.
		if !stopPolicyRuleReachable(rule.WhenPaths, writePaths) {
			continue
		}
		captures := stopPolicyTemplateCaptures(rule.WhenPaths, writePaths)
		rule.collectInto(collect, captures, &dynamicInputRule)
		if rule.Kind == string(policy.KindRequireAssurance) {
			scan.Assurance = append(scan.Assurance, rule.Assurance...)
		}
		for _, check := range rule.Checks {
			// Composite sub-checks inherit the parent rule's trigger surface.
			check.collectInto(collect, captures, &dynamicInputRule)
		}
	}
	if dynamicInputRule {
		scan.Cacheable = false
	}
	// Paths stay bound even when the plan is not cacheable. The same
	// fingerprint identifies the completion candidate, and a candidate must not
	// survive a change to a policy-named input just because some other rule in
	// the same policy keeps the plan off the Stop warm path.
	scan.Paths = sortedKeys(paths)
	sort.Slice(scan.FreshFiles, func(i, j int) bool {
		if scan.FreshFiles[i].Path == scan.FreshFiles[j].Path {
			return scan.FreshFiles[i].MaxAgeHours < scan.FreshFiles[j].MaxAgeHours
		}
		return scan.FreshFiles[i].Path < scan.FreshFiles[j].Path
	})
	return scan
}

// stopPolicyLockRule decodes both the list form used by rules and the inline
// form used by composite checks, so one type covers every place a policy names
// a repository path.
type stopPolicyLockRule struct {
	Kind          string                 `json:"kind"`
	Path          string                 `json:"path"`
	File          string                 `json:"file"`
	Script        string                 `json:"script"`
	WhenPaths     []string               `json:"when_paths"`
	CacheInputs   []string               `json:"cache_inputs"`
	MaxAgeHours   int                    `json:"max_age_hours"`
	RequiredFiles []stopPolicyLockFile   `json:"required_files"`
	Evidence      []stopPolicyLockFile   `json:"evidence"`
	Assurance     []policy.AssuranceGate `json:"assurance"`
	Checks        []stopPolicyLockRule   `json:"checks"`
}

type stopPolicyLockFile struct {
	Path        string `json:"path"`
	File        string `json:"file"`
	MaxAgeHours int    `json:"max_age_hours"`
}

func (r stopPolicyLockRule) collectInto(collect func(path string, maxAgeHours int, fresh bool, captures []map[string]string), captures []map[string]string, dynamicInputRule *bool) {
	fresh := r.Kind == string(policy.KindRequireFreshFile)
	collect(r.Path, r.MaxAgeHours, fresh, captures)
	collect(r.File, 0, false, captures)
	// A require_script target is an input by definition. Git binds it only
	// while it is tracked: a gitignored check script could otherwise be
	// rewritten and the stored report would still be served.
	collect(r.Script, 0, false, captures)
	if r.Kind == string(policy.KindRequireScript) {
		// The script body itself is opaque. Only the inputs its author
		// declares can be bound, so an undeclared script plan is not a
		// function of the fingerprint and must not reuse a report.
		if len(r.CacheInputs) == 0 {
			*dynamicInputRule = true
		}
		for _, input := range r.CacheInputs {
			collect(input, 0, false, captures)
		}
	}
	// Native assurance may inspect complete globbed authority surfaces and
	// wall-clock-aged proof records. Those inputs are intentionally richer than
	// the fixed path set this cache scanner can bind, so an applicable assurance
	// rule always evaluates instead of reusing a Stop report.
	if r.Kind == string(policy.KindRequireAssurance) {
		*dynamicInputRule = true
		for _, gate := range r.Assurance {
			collect(gate.ProofFile, gate.MaxAgeHours, false, captures)
		}
	}
	for _, required := range r.RequiredFiles {
		collect(required.Path, required.MaxAgeHours, fresh, captures)
	}
	for _, evidence := range r.Evidence {
		collect(evidence.File, 0, false, captures)
		collect(evidence.Path, 0, false, captures)
	}
}

// stopPolicyTemplateCaptures mirrors the evaluator's one-context-per-write,
// first-matching-pattern rule. It is deliberately best-effort: any malformed
// or unresolved template already makes the plan non-cacheable, while every
// successfully resolved concrete target remains bound to the completion
// candidate.
func stopPolicyTemplateCaptures(patterns, writePaths []string) []map[string]string {
	contexts := make([]map[string]string, 0, len(writePaths))
	for _, writePath := range writePaths {
		_, captures, matched, err := runtime.MatchTemplateAny(patterns, writePath)
		if err != nil || !matched {
			continue
		}
		contexts = append(contexts, captures)
	}
	return contexts
}

// stopPolicyInputIdentities binds every repository path the policy names.
//
// Git alone does not cover them: `git status` never lists ignored files, so a
// gitignored evidence file can be deleted or rewritten without moving any
// fingerprint field. Every policy path is resolved and hashed independently
// even when Git also reports it: dirty-set symlink identity deliberately
// describes the link itself, while policy evaluation follows contained links.
func stopPolicyInputIdentities(repoRoot string, paths []string) []policyInputIdentity {
	return stopPolicyInputIdentitiesWithScan(repoRoot, paths, nil)
}

func stopPolicyInputIdentitiesWithScan(repoRoot string, paths []string, scanCache *stopPolicyScanCache) []policyInputIdentity {
	if len(paths) == 0 {
		return nil
	}
	identities := make([]policyInputIdentity, 0, len(paths))
	for _, path := range paths {
		identities = append(identities, stopPolicyInputIdentityWithScan(repoRoot, path, scanCache))
	}
	return identities
}

func stopPolicyInputIdentity(repoRoot, path string) policyInputIdentity {
	return stopPolicyInputIdentityWithScan(repoRoot, path, nil)
}

func stopPolicyInputIdentityWithScan(repoRoot, path string, scanCache *stopPolicyScanCache) policyInputIdentity {
	identity := policyInputIdentity{Path: path}
	resolved, err := resolveStopPolicyInputPath(repoRoot, path)
	if err != nil {
		identity.Identity = "resolve-error:" + err.Error()
		return identity
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			identity.Identity = "missing"
			identity.Trusted = true
			return identity
		}
		identity.Identity = "error:" + err.Error()
		return identity
	}
	generation, generationOK := stopPolicyInputGeneration(resolved, info)
	cacheKey := repoRoot + "\x00" + path
	if generationOK && scanCache != nil {
		if cached, ok := scanCache.policyIdentities[cacheKey]; ok && cached.resolved == resolved && cached.generation == generation {
			return cached.identity
		}
	}
	switch {
	case info.IsDir():
		identity.Identity = stopDirectoryContentHashObserved(resolved, scanCache.recordContentHash)
		identity.Trusted = trustedStopDirectoryIdentity(identity.Identity)
	case info.Mode().IsRegular():
		contentHash, hashErr := hashFileContentExpected(resolved, info)
		if hashErr != nil {
			identity.Identity = "error:" + hashErr.Error()
			return identity
		}
		if info.Size() <= stopPolicyContentHashBound {
			scanCache.recordContentHash(info.Size())
		}
		metadata, metadataOK := stopPathMetadataGeneration(resolved, info)
		if !metadataOK {
			identity.Identity = "metadata-error:platform identity unavailable"
			return identity
		}
		identity.Identity = fmt.Sprintf("file:%s:%s", contentHash, metadata)
		identity.Trusted = exactSHA256(contentHash)
	default:
		identity.Identity = "mode:" + info.Mode().String()
	}
	if generationOK && identity.Trusted && scanCache != nil {
		if scanCache.policyIdentities == nil {
			scanCache.policyIdentities = make(map[string]stopCachedPolicyIdentity)
		}
		scanCache.policyIdentities[cacheKey] = stopCachedPolicyIdentity{
			resolved: resolved, generation: generation, identity: identity,
		}
	}
	return identity
}

func stopPolicyInputGeneration(path string, info os.FileInfo) (string, bool) {
	if info.IsDir() {
		return stopPolicyDirectoryGeneration(path)
	}
	if info.Mode().IsRegular() {
		return stopPathMetadataGeneration(path, info)
	}
	return "", false
}

func resolveStopPolicyInputPath(repoRoot, path string) (string, error) {
	configured := filepath.FromSlash(path)
	cleaned := filepath.Clean(configured)
	if configured == "" || pathidentity.Rooted(path) || pathidentity.EscapesLexically(path) ||
		cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("policy input %q escapes the repository", path)
	}
	resolvedRoot, err := pathidentity.ResolveExisting(repoRoot)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	if cleaned == "." {
		return resolvedRoot, nil
	}
	lexical := filepath.Join(resolvedRoot, cleaned)
	resolvedParent, err := pathidentity.ResolveProspective(filepath.Dir(lexical))
	if err != nil {
		return "", fmt.Errorf("resolve policy input parent %q: %w", path, err)
	}
	parentRelative, err := filepath.Rel(resolvedRoot, resolvedParent)
	if err != nil {
		return "", fmt.Errorf("validate policy input %q containment: %w", path, err)
	}
	if parentRelative == ".." || strings.HasPrefix(parentRelative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("policy input %q resolves outside the repository", path)
	}
	candidate := filepath.Join(resolvedParent, filepath.Base(lexical))
	leaf, leafErr := os.Lstat(candidate)
	if leafErr != nil {
		if errors.Is(leafErr, os.ErrNotExist) {
			return candidate, nil
		}
		return "", fmt.Errorf("inspect policy input %q: %w", path, leafErr)
	}
	// ResolveExisting uses an operating-system file handle to normalize leaf
	// identity. Opening a FIFO blocks, so special leaves and symlinks whose
	// current target is special must stay unresolved and non-cacheable.
	if leaf.Mode()&os.ModeSymlink == 0 && !leaf.IsDir() && !leaf.Mode().IsRegular() {
		return candidate, nil
	}
	if leaf.Mode()&os.ModeSymlink != 0 {
		target, statErr := os.Stat(candidate)
		if statErr != nil || (!target.IsDir() && !target.Mode().IsRegular()) {
			return candidate, nil
		}
	}
	resolved, err := pathidentity.ResolveExisting(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve policy input %q: %w", path, err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil {
		return "", fmt.Errorf("validate policy input %q containment: %w", path, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("policy input %q resolves outside the repository", path)
	}
	return resolved, nil
}

func trustedStopDirectoryIdentity(identity string) bool {
	return strings.HasPrefix(identity, "dir:") && exactSHA256(strings.TrimPrefix(identity, "dir:"))
}

func exactSHA256(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

// stopPolicyReportExpiry is the instant a stored report stops describing its
// inputs because an age requirement elapses. Zero means the report has no
// wall-clock dependence. A missing required file is already a violation, and
// its later appearance moves a bound identity, so it needs no expiry.
func stopPolicyReportExpiry(repoRoot string, freshFiles []stopPolicyFreshFile) int64 {
	expiry := int64(0)
	for _, fresh := range freshFiles {
		resolved, err := resolveStopPolicyInputPath(repoRoot, fresh.Path)
		if err != nil {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		candidate := info.ModTime().Add(time.Duration(fresh.MaxAgeHours) * time.Hour).Unix()
		if expiry == 0 || candidate < expiry {
			expiry = candidate
		}
	}
	return expiry
}

// stopPolicyReportExpired reports whether a stored report has outlived the age
// requirement that produced it. Zero means the policy has no wall-clock
// dependence, so the report never expires on time alone.
func stopPolicyReportExpired(expiresAt int64) bool {
	return expiresAt != 0 && time.Now().Unix() >= expiresAt
}

// stopPolicyRuleReachable reports whether any recorded write path can trigger a
// rule. Template patterns use the same matcher as evaluation. Malformed input
// fails toward reachable so uncertainty never admits a report the rule might
// have changed.
func stopPolicyRuleReachable(whenPaths, writePaths []string) bool {
	if len(whenPaths) == 0 {
		return true
	}
	if runtime.PatternHasAnyTemplateVar(whenPaths) {
		for _, writePath := range writePaths {
			_, _, matched, err := runtime.MatchTemplateAny(whenPaths, writePath)
			if err != nil {
				return true
			}
			if matched {
				return true
			}
		}
		return false
	}
	_, _, matched, err := runtime.MatchAnyPath(whenPaths, writePaths)
	if err != nil {
		return true
	}
	return matched
}

func sortedKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func stopPolicyGitStatus(repoRoot string) (status string, mode string) {
	snapshot := stopPolicyGitSnapshotFor(repoRoot)
	return snapshot.Status, snapshot.StatusMode
}

func stopPolicyGitSnapshotFor(repoRoot string) stopPolicyGitSnapshot {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv(stopPolicyUntrackedModeEnv)))
	switch mode {
	case "all", "no", "normal":
	default:
		mode = "normal"
	}
	raw, err := gitCommandOutput(repoRoot, "status", "--porcelain=v1", "-z", "--untracked-files="+mode)
	status := filterStopPolicyGitStatus(raw)
	if err != nil {
		status = "error:" + err.Error() + "\n" + status
	}
	return stopPolicyGitSnapshot{
		Head:       gitHeadFingerprint(repoRoot),
		Status:     status,
		StatusMode: mode,
		StatusOK:   err == nil,
	}
}

func completionPolicyGitSnapshotFor(repoRoot string) stopPolicyGitSnapshot {
	raw, err := gitCommandOutput(repoRoot, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	status := filterStopPolicyGitStatus(raw)
	if err != nil {
		status = "error:" + err.Error() + "\n" + status
	}
	return stopPolicyGitSnapshot{
		Head: gitHeadFingerprint(repoRoot), Status: status, StatusMode: "all", StatusOK: err == nil,
	}
}

func stopPolicyEvidenceHash(state SessionState) (string, error) {
	input := stopPolicyEvidenceInputFor(state)
	body, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal Stop evidence identity: %w", err)
	}
	return hashBytes(body), nil
}

func stopPolicyEvidenceRevision(state SessionState) (string, error) {
	input := stopPolicyEvidenceRevisionInput{
		Evidence:             stopPolicyEvidenceInputFor(state),
		EvidenceEpoch:        state.EvidenceEpoch,
		EvidenceSegmentCount: state.EvidenceSegmentCount,
		EvidenceSegmentHash:  state.EvidenceSegmentDigest,
		EvidenceOverflow:     state.EvidenceOverflow,
		MaterialEvents:       state.MaterialEvents,
	}
	body, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("marshal Stop evidence revision: %w", err)
	}
	return hashBytes(body), nil
}

func stopPolicyEvidenceInputFor(state SessionState) stopPolicyEvidenceInput {
	return stopPolicyEvidenceInput{
		ReadPaths:      sortedUniqueExact(state.ReadPaths),
		WritePaths:     sortedUniqueExact(state.WritePaths),
		WriteEpochs:    cloneWriteEpochs(state.WriteEpochs),
		Commands:       sortedUnique(state.Commands),
		Claims:         sortedUnique(state.Claims),
		CommandResults: append([]CommandResult{}, state.CommandResults...),
	}
}

// CaptureCompletionState returns a stable, content-bound snapshot without
// evaluating policy or mutating repository/session state. Callers must capture
// again after evaluation and require the fingerprints to match, which closes
// the candidate-mutation race across HEAD, dirty index/worktree entries,
// policy bytes, active-session evidence, and the saved session report.
