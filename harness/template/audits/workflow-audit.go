package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	"reconc-harness/template/audits/lib/donecheck"
)

const schemaRel = "tools/reconc/harness/template/config/workflow/task-schema.yaml"
const allAuditWorkerLimit = 8

// loadedSchema is initialised once in main() from task-schema.yaml; falls
// back to donecheck.DefaultSchema() so audits that depend on schema do not
// crash when the YAML is missing -- schema-present then surfaces the gap.
var loadedSchema = donecheck.DefaultSchema()

// projectRoot returns the repo-internal directory that holds product code,
// scripts, config, db artifacts and frontend workspace. Larger repos use a
// codebase/ subdirectory; smaller repos lay these out flat at the repo root.
// Callers join further path fragments onto the result.
func projectRoot(repoRoot string) string {
	if info, err := os.Stat(filepath.Join(repoRoot, "codebase")); err == nil && info.IsDir() {
		return filepath.Join(repoRoot, "codebase")
	}
	return repoRoot
}

// projectRel returns the repo-relative slash-style path that maps to the
// flat-root or codebase/-style location of relPath. It is used in audit
// failure messages so operators see the path that actually exists in their
// repository layout.
func projectRel(repoRoot string, relPath string) string {
	if info, err := os.Stat(filepath.Join(repoRoot, "codebase")); err == nil && info.IsDir() {
		return "codebase/" + relPath
	}
	return relPath
}

type taskEntry struct {
	icon        string
	id          string
	name        string
	description string
	target      string
	line        int
}

type taskIndex struct {
	currentName   string
	currentTarget string
	currentLine   int
	entries       []taskEntry
}

type taskDetailInfo struct {
	state           string
	priority        string
	dependsRaw      string
	dependencies    []string
	parallelGroup   string
	touchSurfaces   []string
	orderRationale  string
	scopeType       string
	specLinesRaw    string
	researchRefs    []string
	completionClaim string
}

type batchAuditOutput struct {
	Results []batchAuditResult `json:"results"`
}

type batchAuditResult struct {
	Mode     string   `json:"mode"`
	Failures []string `json:"failures"`
}

type auditFunc func(string) []string

func main() {
	mode := "all"
	var batchModes []string
	if len(os.Args) > 1 {
		if os.Args[1] == "--batch-json" {
			batchModes = os.Args[2:]
		} else {
			mode = os.Args[1]
		}
	}
	root, err := os.Getwd()
	if err != nil {
		block("get cwd: %v", err)
	}
	if schema, schemaErr := donecheck.LoadSchema(filepath.Join(root, filepath.FromSlash(schemaRel))); schemaErr == nil {
		loadedSchema = schema
	}
	if batchModes != nil {
		os.Exit(writeBatchJSON(root, batchModes))
	}
	failures := run(root, mode)
	if len(failures) > 0 {
		sort.Strings(failures)
		for _, failure := range failures {
			fmt.Println(failure)
		}
		os.Exit(2)
	}
}

func writeBatchJSON(root string, modes []string) int {
	output, hasFailures := batchAuditResults(root, modes)
	if err := json.NewEncoder(os.Stdout).Encode(output); err != nil {
		fmt.Fprintf(os.Stderr, "encode batch audit output: %v\n", err)
		return 1
	}
	if hasFailures {
		return 2
	}
	return 0
}

func batchAuditResults(root string, modes []string) (batchAuditOutput, bool) {
	return batchAuditResultsWithRunner(root, modes, run)
}

func batchAuditResultsWithRunner(root string, modes []string, runner func(string, string) []string) (batchAuditOutput, bool) {
	if len(modes) == 0 {
		return batchAuditOutput{Results: []batchAuditResult{{
			Mode:     "",
			Failures: []string{"no audit modes provided"},
		}}}, true
	}

	results := make([]batchAuditResult, len(modes))
	hasFailuresByMode := make([]bool, len(modes))
	var wg sync.WaitGroup
	for i, mode := range modes {
		i, mode := i, mode
		wg.Add(1)
		go func() {
			defer wg.Done()
			failures := runner(root, mode)
			sort.Strings(failures)
			if failures == nil {
				failures = []string{}
			}
			if len(failures) > 0 {
				hasFailuresByMode[i] = true
			}
			results[i] = batchAuditResult{
				Mode:     mode,
				Failures: failures,
			}
		}()
	}
	wg.Wait()

	hasFailures := false
	for _, modeFailed := range hasFailuresByMode {
		if modeFailed {
			hasFailures = true
			break
		}
	}
	return batchAuditOutput{Results: results}, hasFailures
}

func run(root string, mode string) []string {
	switch mode {
	case "all":
		return runAllAudits(root)
	case "repo-layout":
		return auditRepoLayout(root)
	case "repo-cleanliness":
		return auditRepoCleanliness(root)
	case "agent-quality":
		return auditAgentQuality(root)
	case "task-state":
		return cachedTaskState(root)
	case "tasks-md-rows-immutable":
		return auditTasksMdRowsImmutable(root)
	case "git-hooks":
		return auditGitHooks(root)
	case "agent-hooks":
		return auditAgentHooks(root)
	case "start-entrypoint":
		return cachedStartEntrypoint(root)
	case "spec-format":
		return cachedSpecFormat(root)
	case "spec-audit-artifacts":
		return auditSpecAuditArtifacts(root)
	case "arch-boundaries":
		return auditArchitectureBoundaries(root)
	case "module-contracts":
		return auditModuleContracts(root)
	case "generated-references":
		return auditGeneratedReferences(root)
	case "build-baseline":
		return cachedBuildBaseline(root)
	case "durable-store":
		return cachedDurableStoreBaseline(root)
	case "test-coverage":
		return cachedTestCoverage(root)
	case "agents-md-mirror":
		return cachedAgentsMdMirror(root)
	case "schema-present":
		return cachedSchemaPresent(root)
	case "spec-task-coverage":
		return auditSpecTaskCoverage(root)
	default:
		return []string{fmt.Sprintf("unknown audit mode %q", mode)}
	}
}

func runAllAudits(root string) []string {
	return runAuditFuncs(root, []auditFunc{
		auditRepoLayout,
		auditRepoCleanliness,
		auditAgentQuality,
		cachedTaskState,
		auditTasksMdRowsImmutable,
		auditAgentHooks,
		cachedStartEntrypoint,
		cachedSpecFormat,
		auditSpecAuditArtifacts,
		auditArchitectureBoundaries,
		auditModuleContracts,
		auditGeneratedReferences,
		cachedBuildBaseline,
		cachedDurableStoreBaseline,
		cachedTestCoverage,
		cachedAgentsMdMirror,
		cachedSchemaPresent,
	})
}

func runAuditFuncs(root string, funcs []auditFunc) []string {
	if len(funcs) == 0 {
		return nil
	}
	workers := allAuditWorkerLimit
	if workers > len(funcs) {
		workers = len(funcs)
	}
	results := make([][]string, len(funcs))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				results[index] = funcs[index](root)
			}
		}()
	}
	for index := range funcs {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	var failures []string
	for _, result := range results {
		failures = append(failures, result...)
	}
	return failures
}

func auditRepoLayout(root string) []string {
	var failures []string
	allowedRoot := map[string]bool{
		".DS_Store": true, ".agents": true, ".claude": true, ".codex": true, ".git": true, ".github": true, ".gitignore": true,
		".cursor": true, ".opencode": true, ".reconc": true, ".reconc.yml": true, "AGENTS.md": true, "README.md": true, "_drop": true,
		"codebase": true, "docs": true, "go.mod": true, "go.sum": true, "research": true, "start.md": true, "tools": true, "workflow-complete-loop.md": true,
	}
	// Repo with codebase/ enforces a thin root: product subtrees live under
	// codebase/. Repo without codebase/ uses a flat root where the same
	// subtrees sit at the top level, so backend/frontend/db/scripts/etc.
	// become legitimate root entries.
	codebaseLayout := exists(filepath.Join(root, "codebase"))
	forbiddenRootDirs := map[string]bool{}
	if codebaseLayout {
		forbiddenRootDirs = map[string]bool{
			"cmd": true, "internal": true, "pkg": true, "ui-kit": true, "sdk": true, "skills": true,
			"industry-packs": true, "scripts": true, "config": true, "assets": true, "apps": true,
			"packages": true, "build": true, "dist": true,
		}
	} else {
		for _, name := range []string{"backend", "frontend", "db", "scripts", "config", "assets", "modules", "sdk", "shared"} {
			allowedRoot[name] = true
		}
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return []string{fmt.Sprintf("read repo root: %v", err)}
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "CLAUDE"+".md" {
			continue
		}
		if forbiddenRootDirs[name] && entry.IsDir() {
			failures = append(failures, fmt.Sprintf("root directory %s is forbidden; use codebase/%s owner paths", name, name))
			continue
		}
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(name, ".env") {
			continue
		}
		if !allowedRoot[name] {
			failures = append(failures, fmt.Sprintf("unexpected root entry %s; root must stay thin", name))
		}
	}
	if codebaseLayout {
		for _, name := range []string{"package.json", "bun.lock", "bun.lockb", "package-lock.json", "pnpm-lock.yaml", "yarn.lock"} {
			if exists(filepath.Join(root, name)) {
				failures = append(failures, fmt.Sprintf("root %s is forbidden; frontend dependency files belong under codebase/frontend/", name))
			}
		}
	}
	failures = append(failures, auditDependencyLocality(root)...)
	return failures
}

func auditDependencyLocality(root string) []string {
	var failures []string
	frontendPrefix := projectRel(root, "frontend")
	allowedNodeModules := filepath.ToSlash(filepath.Clean(frontendPrefix + "/node_modules"))
	allowedPackageFiles := map[string]bool{
		filepath.ToSlash(filepath.Clean(frontendPrefix + "/package.json")):      true,
		filepath.ToSlash(filepath.Clean(frontendPrefix + "/bun.lock")):          true,
		filepath.ToSlash(filepath.Clean(frontendPrefix + "/bun.lockb")):         true,
		filepath.ToSlash(filepath.Clean(frontendPrefix + "/package-lock.json")): false,
		filepath.ToSlash(filepath.Clean(frontendPrefix + "/pnpm-lock.yaml")):    false,
		filepath.ToSlash(filepath.Clean(frontendPrefix + "/yarn.lock")):         false,
	}
	skipDirs := map[string]bool{
		".git": true, ".reconc": true, "_drop": true, "research": true,
		".agents": true, ".claude": true, ".codex": true, ".cursor": true, ".kilo": true, ".opencode": true, ".vscode": true,
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			failures = append(failures, fmt.Sprintf("walk %s: %v", rel(root, path), err))
			return nil
		}
		relative := filepath.ToSlash(rel(root, path))
		if entry.IsDir() {
			if relative == "." {
				return nil
			}
			parts := strings.Split(relative, "/")
			if skipDirs[parts[0]] {
				return filepath.SkipDir
			}
			clean := filepath.ToSlash(filepath.Clean(relative))
			if entry.Name() == "node_modules" && clean != allowedNodeModules {
				if clean == "codebase/frontend/node_modules" || strings.HasPrefix(clean, "codebase/frontend/node_modules/") {
					return filepath.SkipDir
				}
				failures = append(failures, fmt.Sprintf("node_modules found at %s; only %s is allowed", relative, allowedNodeModules))
				return filepath.SkipDir
			}
			return nil
		}
		name := entry.Name()
		switch name {
		case "package.json", "bun.lock", "bun.lockb", "package-lock.json", "pnpm-lock.yaml", "yarn.lock":
			clean := filepath.ToSlash(filepath.Clean(relative))
			allowed, ok := allowedPackageFiles[clean]
			if !ok || !allowed {
				failures = append(failures, fmt.Sprintf("dependency file %s violates single Bun workspace policy", relative))
			}
		}
		return nil
	})
	if err != nil {
		failures = append(failures, fmt.Sprintf("walk repo: %v", err))
	}
	return failures
}

// auditGitHooks verifies that the source-controlled `.githooks/pre-commit`
// hook is present and activated via `core.hooksPath`. Source-controlled
// git hooks close the gap where an agent could `git commit` via Bash and
// bypass agent-runtime Reconc hooks. The audit runs in warn mode so a
// fresh clone can bootstrap before `git config core.hooksPath .githooks`
// is set, but flags the gap until activation.
func auditGitHooks(root string) []string {
	var failures []string
	hookPath := filepath.Join(root, ".githooks/pre-commit")
	info, err := os.Stat(hookPath)
	if err != nil {
		failures = append(failures, fmt.Sprintf(".githooks/pre-commit missing or unreadable: %v (run `reconc hook install git-pre-commit` and copy/commit the result to .githooks/pre-commit)", err))
	} else if runtime.GOOS != "windows" && info.Mode()&0o111 == 0 {
		failures = append(failures, ".githooks/pre-commit is not executable (run `chmod +x .githooks/pre-commit`)")
	}
	cmd, cancel := commandWithTimeout(shortAuditCommandTimeout, "git", "-C", root, "config", "--get", "core.hooksPath")
	out, err := cmd.Output()
	cancel()
	configured := strings.TrimSpace(string(out))
	if err != nil || configured != ".githooks" {
		failures = append(failures, fmt.Sprintf("git core.hooksPath is %q, must be `.githooks` (run `git config core.hooksPath .githooks` once per fresh clone)", configured))
	}
	return failures
}

// auditTasksMdRowsImmutable enforces the APPEND-ONLY invariant on
// docs/tasks.md: any row that exists in HEAD must still exist in the
// working tree with the same name and target. A `[x]` row in HEAD must
// remain `[x]` (no reverse flip). A `[ ]` row in HEAD must still be
// present (it may have flipped to `[x]` which is the one legitimate
// state transition). New rows in the working tree are unconstrained.
//
// Graceful no-op cases: no HEAD yet (first commit), HEAD does not contain
// docs/tasks.md, or either side has a fatal parse error (the regular
// task-state audit surfaces parse problems separately).
func auditTasksMdRowsImmutable(root string) []string {
	wtPath := filepath.Join(root, "docs/tasks.md")
	wtBytes, err := os.ReadFile(wtPath)
	if err != nil {
		// Missing working-tree file is handled by auditTaskState.
		return nil
	}
	cmd, cancel := commandWithTimeout(shortAuditCommandTimeout, "git", "-C", root, "show", "HEAD:docs/tasks.md")
	headBytes, err := cmd.Output()
	cancel()
	if err != nil {
		// No HEAD yet, or file not in HEAD: nothing to compare against.
		return nil
	}
	headIndex, headFails := parseTaskIndex(string(headBytes))
	wtIndex, wtFails := parseTaskIndex(string(wtBytes))
	if len(headFails) > 0 || len(wtFails) > 0 {
		// Parse problems are reported by auditTaskState; do not double-report.
		return nil
	}
	wtByName := make(map[string]taskEntry, len(wtIndex.entries))
	for _, e := range wtIndex.entries {
		wtByName[e.name] = e
	}
	var failures []string
	for _, headEntry := range headIndex.entries {
		wtEntry, present := wtByName[headEntry.name]
		if !present {
			failures = append(failures, fmt.Sprintf(
				"docs/tasks.md APPEND-ONLY violation: row %s present in HEAD is missing from working tree (historical rows cannot be deleted)",
				headEntry.name))
			continue
		}
		if headEntry.icon == "x" && wtEntry.icon != "x" {
			failures = append(failures, fmt.Sprintf(
				"docs/tasks.md APPEND-ONLY violation: row %s was [x] in HEAD but is now [%s] (reverse-flip forbidden)",
				headEntry.name, wtEntry.icon))
		}
	}
	return failures
}

func auditTaskState(root string) []string {
	var failures []string
	path := filepath.Join(root, "docs/tasks.md")
	content, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("read docs/tasks.md: %v", err)}
	}
	index, parseFailures := parseTaskIndex(string(content))
	failures = append(failures, parseFailures...)
	if len(parseFailures) > 0 {
		return failures
	}
	openCount := 0
	referenced := map[string]bool{}
	seenNames := map[string]int{}
	seenTargets := map[string]int{}
	doneTasks := map[string]bool{}
	taskPositions := map[string]int{}
	detailInfos := map[string]taskDetailInfo{}
	currentMatches := 0
	// Diff-aware loop gate: a done TASK whose detail file is part of this
	// change (newly promoted or edited) must carry the Reality-Check loop
	// attestation, while already-archived tasks not in the diff stay exempt so
	// the full-history sweep does not retroactively invalidate them.
	changedFiles, diffFailures := collectGitDiffFiles(root)
	failures = append(failures, diffFailures...)
	for position, entry := range index.entries {
		taskPositions[entry.name] = position
		if entry.icon == "x" {
			doneTasks[entry.name] = true
		}
	}
	for _, entry := range index.entries {
		seenNames[entry.name]++
		if seenNames[entry.name] > 1 {
			failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: duplicate task name %s", entry.line, entry.name))
		}
		cleanTarget := filepath.Clean(entry.target)
		seenTargets[cleanTarget]++
		if seenTargets[cleanTarget] > 1 {
			failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: duplicate task target %s", entry.line, entry.target))
		}
		targetName := strings.TrimSuffix(filepath.Base(entry.target), ".md")
		if targetName != entry.name {
			failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: task name %s and target filename %s disagree", entry.line, entry.name, targetName))
		}
		if !strings.HasPrefix(entry.name, entry.id+"-") {
			failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: task name must start with %s-", entry.line, entry.id))
		}
		isCurrent := entry.name == index.currentName && filepath.Clean(entry.target) == filepath.Clean(index.currentTarget)
		if isCurrent {
			currentMatches++
			if entry.icon != " " {
				failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: Current header must point to an unchecked [ ] TASK row", entry.line))
			}
		}
		if entry.icon == "x" {
			if !strings.HasPrefix(entry.target, "tasks/done/") {
				failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: checked task must point to tasks/done/", entry.line))
			}
		} else {
			openCount++
			if strings.HasPrefix(entry.target, "tasks/done/") {
				failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: open task must point to tasks/", entry.line))
			}
		}
		referenced[filepath.Clean(entry.target)] = true
		detailPath := filepath.Join(root, "docs", filepath.FromSlash(entry.target))
		if !exists(detailPath) {
			failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: missing detail file docs/%s", entry.line, entry.target))
			continue
		}
		loopRequired := entry.icon == "x" && changedFiles["docs/"+entry.target] != nil
		info, detailFailures := auditTaskDetail(root, detailPath, entry, isCurrent, loopRequired)
		detailInfos[entry.name] = info
		failures = append(failures, detailFailures...)
	}
	if openCount == 0 {
		failures = append(failures, "docs/tasks.md has no open [ ] tasks; run AGENTS.md Continuity Sweep and create the next TASK, or report zero-finding Terminal Gate status from workflow-complete-loop.md without committing an empty board")
	}
	if currentMatches == 0 {
		failures = append(failures, "docs/tasks.md Current header must point to exactly one unchecked [ ] TASK row")
	}
	if currentMatches > 1 {
		failures = append(failures, "docs/tasks.md Current header matches multiple TASK rows")
	}
	if index.currentLine > 0 && len(index.entries) > 0 && index.currentLine > index.entries[0].line {
		failures = append(failures, "docs/tasks.md Current header must be the fixed control line before TASK entries")
	}
	failures = append(failures, auditScheduling(index, detailInfos, doneTasks, taskPositions)...)
	failures = append(failures, auditUnreferencedTaskFiles(root, referenced)...)
	return failures
}

func parseTaskIndex(content string) (taskIndex, []string) {
	var index taskIndex
	var failures []string
	entryRe := regexp.MustCompile(`^- \[([ x])\] (TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*) - (.+) -> (tasks(?:/done)?/TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*\.md)$`)
	currentRe := regexp.MustCompile(`^Current: (TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*) -> (tasks(?:/done)?/TASK-[0-9]{4}-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*\.md)$`)
	seenCurrent := false
	for i, line := range strings.Split(content, "\n") {
		lineNo := i + 1
		if strings.TrimSpace(line) == "" || strings.TrimSpace(line) == "# Tasks" {
			continue
		}
		if strings.HasPrefix(line, "Current: ") {
			if seenCurrent {
				failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: duplicate Current header", lineNo))
				continue
			}
			seenCurrent = true
			match := currentRe.FindStringSubmatch(line)
			if match == nil {
				failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: invalid Current header format", lineNo))
				continue
			}
			index.currentName = match[1]
			index.currentTarget = match[2]
			index.currentLine = lineNo
			if strings.TrimSuffix(filepath.Base(index.currentTarget), ".md") != index.currentName {
				failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: Current task name and target filename disagree", lineNo))
			}
			continue
		}
		if !strings.HasPrefix(line, "- [") {
			if strings.HasPrefix(line, "## ") {
				failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: sections are forbidden; tasks.md is a flat logbook", lineNo))
			}
			continue
		}
		match := entryRe.FindStringSubmatch(line)
		if match == nil {
			failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: invalid task entry format", lineNo))
			continue
		}
		name := match[2]
		id := name[:9]
		index.entries = append(index.entries, taskEntry{icon: match[1], id: id, name: name, description: match[3], target: match[4], line: lineNo})
	}
	if !seenCurrent {
		failures = append(failures, "docs/tasks.md missing Current header")
	}
	if len(index.entries) == 0 {
		failures = append(failures, "docs/tasks.md must contain at least one TASK logbook entry")
	}
	return index, failures
}

func auditTaskDetail(root string, path string, entry taskEntry, isCurrent bool, loopRequired bool) (taskDetailInfo, []string) {
	var info taskDetailInfo
	var failures []string
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return info, []string{fmt.Sprintf("read %s: %v", path, err)}
	}
	content := string(contentBytes)
	relative := filepath.ToSlash(path)
	if index := strings.Index(relative, "docs/tasks/"); index >= 0 {
		relative = relative[index:]
	}
	if !strings.HasPrefix(content, "# "+entry.name+"\n") {
		failures = append(failures, fmt.Sprintf("%s: H1 must be '# %s'", relative, entry.name))
	}
	requiredSections := []string{"## Why", "## Status", "## Scheduling", "## Technical Plan", "## Acceptance", "## Sub-Tasks", "## Notes", "## Deviations"}
	for _, section := range requiredSections {
		if !strings.Contains(content, section+"\n") && !strings.HasSuffix(content, section) {
			failures = append(failures, fmt.Sprintf("%s: missing %s", relative, section))
			continue
		}
		sectionBody := strings.TrimSpace(donecheck.ExtractSection(content, section))
		if sectionBody == "" || donecheck.IsPlaceholderValue(sectionBody, loadedSchema) {
			failures = append(failures, fmt.Sprintf("%s: %s is empty or placeholder content", relative, section))
		}
	}
	subTasks := donecheck.ExtractSection(content, "## Sub-Tasks")
	statusState := parseStatusState(donecheck.ExtractSection(content, "## Status"))
	info.state = statusState
	info.priority, info.dependsRaw, info.dependencies, info.parallelGroup, info.touchSurfaces, info.orderRationale, info.scopeType, info.specLinesRaw, info.researchRefs, info.completionClaim = parseSchedulingFields(donecheck.ExtractSection(content, "## Scheduling"))
	activeSubTasks := countSubtaskIcon(subTasks, "~")
	openSubTasks := countSubtaskIcon(subTasks, " ")
	doneSubTasks := countSubtaskIcon(subTasks, "x")
	if statusState == "" {
		failures = append(failures, fmt.Sprintf("%s: Status must contain 'State: Active|Queued|Blocked|Paused|Done'", relative))
	} else if !validStatusState(statusState) {
		failures = append(failures, fmt.Sprintf("%s: Status state %q is invalid; use Active, Queued, Blocked, Paused, or Done", relative, statusState))
	}
	if !hasTaskBullet(donecheck.ExtractSection(content, "## Technical Plan")) {
		failures = append(failures, fmt.Sprintf("%s: Technical Plan must contain concrete bullet steps", relative))
	}
	if !hasTaskBullet(donecheck.ExtractSection(content, "## Acceptance")) {
		failures = append(failures, fmt.Sprintf("%s: Acceptance must contain at least one concrete bullet", relative))
	}
	failures = append(failures, auditSchedulingFields(relative, info, entry.icon == " ")...)
	failures = append(failures, auditTaskSpecBindings(root, relative, content, info, entry.icon == " ")...)
	failures = append(failures, auditTaskScopeTruth(relative, content, info, entry.icon)...)
	if taskHasCodeSurface(info) {
		if !donecheck.HasTestIntent(donecheck.ExtractSection(content, "## Technical Plan"), loadedSchema) {
			failures = append(failures, fmt.Sprintf("%s: code TASK Technical Plan must include same-TASK tests or coverage work", relative))
		}
		if !donecheck.HasTestIntent(donecheck.ExtractSection(content, "## Acceptance"), loadedSchema) {
			failures = append(failures, fmt.Sprintf("%s: code TASK Acceptance must include test/coverage evidence", relative))
		}
		if !donecheck.HasTestIntent(subTasks, loadedSchema) {
			failures = append(failures, fmt.Sprintf("%s: code TASK Sub-Tasks must include a test/coverage sub-task", relative))
		}
	}
	if activeSubTasks+openSubTasks+doneSubTasks == 0 {
		failures = append(failures, fmt.Sprintf("%s: Sub-Tasks must contain at least one status bullet", relative))
	}
	if entry.icon == " " {
		if isCurrent {
			if statusState != "" && statusState != "Active" {
				failures = append(failures, fmt.Sprintf("%s: current TASK Status state must be Active", relative))
			}
			if activeSubTasks != 1 {
				failures = append(failures, fmt.Sprintf("%s: current TASK must have exactly one [~] Sub-Task, got %d", relative, activeSubTasks))
			}
			if activeSubTasks+openSubTasks == 0 {
				failures = append(failures, fmt.Sprintf("%s: current TASK has no remaining Sub-Task", relative))
			}
			return info, failures
		}
		if statusState == "Active" {
			failures = append(failures, fmt.Sprintf("%s: non-current open TASK Status state must not be Active", relative))
		}
		if activeSubTasks != 0 {
			failures = append(failures, fmt.Sprintf("%s: non-current open TASK must not contain [~] Sub-Tasks", relative))
		}
		if openSubTasks == 0 {
			failures = append(failures, fmt.Sprintf("%s: non-current open TASK must contain at least one open [ ] Sub-Task", relative))
		}
		return info, failures
	}
	if entry.icon == "x" {
		if statusState != "" && statusState != "Done" {
			failures = append(failures, fmt.Sprintf("%s: done TASK Status state must be Done", relative))
		}
		if activeSubTasks != 0 || openSubTasks != 0 {
			failures = append(failures, fmt.Sprintf("%s: done TASK still contains active/open Sub-Tasks", relative))
		}
		failures = append(failures, auditFinalRealityCheck(relative, content)...)
		if loopRequired {
			if strings.TrimSpace(donecheck.ParseBulletFields(donecheck.ExtractSection(content, "## Final Reality Check"))["Reality Check Loop"]) == "" {
				failures = append(failures, fmt.Sprintf("%s: done TASK changed in this commit is missing the Reality Check Loop attestation in Final Reality Check; run the per-TASK loop in docs/task-loop-workflow.md and record e.g. 'PASS - 2 passes, nothing left'", relative))
			}
		}
	}
	return info, failures
}

func auditScheduling(index taskIndex, infos map[string]taskDetailInfo, doneTasks map[string]bool, taskPositions map[string]int) []string {
	var failures []string
	for _, entry := range index.entries {
		info := infos[entry.name]
		for _, dep := range info.dependencies {
			depPosition, ok := taskPositions[dep]
			if !ok {
				failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: TASK %s depends on unknown %s", entry.line, entry.name, dep))
				continue
			}
			if dep == entry.name {
				failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: TASK %s depends on itself", entry.line, entry.name))
				continue
			}
			if depPosition >= taskPositions[entry.name] {
				failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: TASK %s dependency %s must appear earlier in the logbook", entry.line, entry.name, dep))
			}
		}
	}
	failures = append(failures, auditParallelTouchSurfaces(index, infos)...)
	if index.currentName == "" {
		return failures
	}
	for _, entry := range index.entries {
		if entry.icon == "x" {
			continue
		}
		info := infos[entry.name]
		executable := taskExecutable(info, doneTasks)
		if entry.name == index.currentName {
			if !executable {
				failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: Current TASK %s is not executable; unresolved dependencies or blocked/paused state", entry.line, entry.name))
			}
			return failures
		}
		if executable {
			failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: executable TASK %s appears before Current %s; row order must make the earliest executable task current", entry.line, entry.name, index.currentName))
			return failures
		}
	}
	return failures
}

func auditParallelTouchSurfaces(index taskIndex, infos map[string]taskDetailInfo) []string {
	var failures []string
	type parallelTask struct {
		name     string
		line     int
		surfaces []string
	}
	groups := map[string][]parallelTask{}
	for _, entry := range index.entries {
		info := infos[entry.name]
		if info.parallelGroup == "" || info.parallelGroup == "none" {
			continue
		}
		groups[info.parallelGroup] = append(groups[info.parallelGroup], parallelTask{name: entry.name, line: entry.line, surfaces: info.touchSurfaces})
	}
	for group, tasks := range groups {
		for i := 0; i < len(tasks); i++ {
			for j := i + 1; j < len(tasks); j++ {
				overlap := overlappingTouchSurface(tasks[i].surfaces, tasks[j].surfaces)
				if overlap != "" {
					failures = append(failures, fmt.Sprintf("docs/tasks.md line %d: Parallel Group %s task %s overlaps %s on Expected Touch Surfaces %s", tasks[j].line, group, tasks[j].name, tasks[i].name, overlap))
				}
			}
		}
	}
	return failures
}

func overlappingTouchSurface(left []string, right []string) string {
	for _, a := range left {
		for _, b := range right {
			if touchSurfaceOverlaps(a, b) {
				return a + " <-> " + b
			}
		}
	}
	return ""
}

func touchSurfaceOverlaps(left string, right string) bool {
	left = normalizeTouchSurface(left)
	right = normalizeTouchSurface(right)
	if left == "" || right == "" {
		return false
	}
	leftPrefix := strings.TrimSuffix(left, "/**")
	rightPrefix := strings.TrimSuffix(right, "/**")
	if left == right || leftPrefix == rightPrefix {
		return true
	}
	return strings.HasPrefix(leftPrefix+"/", rightPrefix+"/") || strings.HasPrefix(rightPrefix+"/", leftPrefix+"/")
}

func normalizeTouchSurface(surface string) string {
	surface = strings.TrimSpace(filepath.ToSlash(surface))
	surface = strings.TrimPrefix(surface, "./")
	for strings.Contains(surface, "//") {
		surface = strings.ReplaceAll(surface, "//", "/")
	}
	return strings.TrimSuffix(surface, "/")
}

func taskExecutable(info taskDetailInfo, doneTasks map[string]bool) bool {
	if info.state != "Active" && info.state != "Queued" {
		return false
	}
	for _, dep := range info.dependencies {
		if !doneTasks[dep] {
			return false
		}
	}
	return true
}

func auditSchedulingFields(relative string, info taskDetailInfo, requireScopeFields bool) []string {
	var failures []string
	if !validPriority(info.priority) {
		failures = append(failures, fmt.Sprintf("%s: Scheduling Priority must be P0, P1, P2, or P3", relative))
	}
	if info.parallelGroup == "" || donecheck.IsPlaceholderValue(info.parallelGroup, loadedSchema) {
		failures = append(failures, fmt.Sprintf("%s: Scheduling Parallel Group is empty or placeholder content", relative))
	} else if info.parallelGroup != "none" && !regexp.MustCompile(`^PG-[A-Za-z0-9]+(?:-[A-Za-z0-9]+)*$`).MatchString(info.parallelGroup) {
		failures = append(failures, fmt.Sprintf("%s: Scheduling Parallel Group must be none or PG-Name", relative))
	}
	if len(info.touchSurfaces) == 0 {
		failures = append(failures, fmt.Sprintf("%s: Scheduling Expected Touch Surfaces must list at least one repo-relative owner path/glob", relative))
	}
	for _, surface := range info.touchSurfaces {
		if invalidTouchSurface(surface) {
			failures = append(failures, fmt.Sprintf("%s: Scheduling Expected Touch Surfaces contains invalid surface %q", relative, surface))
		}
	}
	if len(info.dependencies) == 0 && strings.TrimSpace(info.dependsRaw) != "none" {
		failures = append(failures, fmt.Sprintf("%s: Scheduling Depends On must be none or comma-separated TASK names", relative))
	}
	if len(info.orderRationale) < 30 || donecheck.IsPlaceholderValue(info.orderRationale, loadedSchema) {
		failures = append(failures, fmt.Sprintf("%s: Scheduling Order Rationale must explain why this task sits here in the queue", relative))
	}
	if requireScopeFields {
		failures = append(failures, auditScopeSchedulingFields(relative, info)...)
	}
	return failures
}

func auditScopeSchedulingFields(relative string, info taskDetailInfo) []string {
	var failures []string
	if !loadedSchema.IsValidScopeType(info.scopeType) {
		failures = append(failures, fmt.Sprintf("%s: Scheduling Scope Type must be Slice, Complete Feature, Coverage Index, or Audit Repair", relative))
	}
	if info.specLinesRaw == "" || donecheck.IsPlaceholderValue(info.specLinesRaw, loadedSchema) {
		failures = append(failures, fmt.Sprintf("%s: Scheduling Spec Lines must name exact docs/spec.md line anchors or none", relative))
	} else if info.specLinesRaw != "none" && !validSpecLineRefs(info.specLinesRaw) {
		failures = append(failures, fmt.Sprintf("%s: Scheduling Spec Lines must use docs/spec.md:Lx or docs/spec.md:Lx-Ly anchors", relative))
	}
	for _, ref := range info.researchRefs {
		if ref != "none" && !strings.HasPrefix(ref, "research/") {
			failures = append(failures, fmt.Sprintf("%s: Scheduling Research Refs contains invalid ref %q", relative, ref))
		}
	}
	if len(info.researchRefs) == 0 {
		failures = append(failures, fmt.Sprintf("%s: Scheduling Research Refs must be none or research/... paths", relative))
	}
	if len(info.completionClaim) < 30 || donecheck.IsPlaceholderValue(info.completionClaim, loadedSchema) {
		failures = append(failures, fmt.Sprintf("%s: Scheduling Completion Claim must state the exact truth that Done will assert", relative))
	}
	return failures
}

func validSpecLineRefs(value string) bool {
	lineRef := regexp.MustCompile(`^docs/spec\.md:L[0-9]+(?:-L?[0-9]+)?$`)
	for _, ref := range parseCSVFields(value) {
		if !lineRef.MatchString(ref) {
			return false
		}
	}
	return true
}

func auditSpecTaskCoverage(root string) []string {
	var failures []string
	specBytes, err := os.ReadFile(filepath.Join(root, "docs/spec.md"))
	if err != nil {
		return []string{fmt.Sprintf("read docs/spec.md: %v", err)}
	}
	tasksBytes, err := os.ReadFile(filepath.Join(root, "docs/tasks.md"))
	if err != nil {
		return []string{fmt.Sprintf("read docs/tasks.md: %v", err)}
	}
	index, parseFailures := parseTaskIndex(string(tasksBytes))
	failures = append(failures, parseFailures...)
	if len(parseFailures) > 0 {
		return failures
	}
	specLines := lineCount(string(specBytes))
	covered := make([]bool, specLines+1)
	openTasks := 0
	for _, entry := range index.entries {
		if entry.icon != " " {
			continue
		}
		openTasks++
		detailPath := filepath.Join(root, "docs", filepath.FromSlash(entry.target))
		content, err := os.ReadFile(detailPath)
		if err != nil {
			failures = append(failures, fmt.Sprintf("read docs/%s: %v", entry.target, err))
			continue
		}
		_, _, _, _, _, _, _, specLinesRaw, _, _ := parseSchedulingFields(donecheck.ExtractSection(string(content), "## Scheduling"))
		if specLinesRaw == "" || specLinesRaw == "none" {
			failures = append(failures, fmt.Sprintf("docs/%s: open TASK must declare docs/spec.md line coverage", entry.target))
			continue
		}
		for _, ref := range parseCSVFields(specLinesRaw) {
			start, end, ok := parseSpecLineRef(ref)
			if !ok {
				failures = append(failures, fmt.Sprintf("docs/%s: invalid Spec Lines ref %q", entry.target, ref))
				continue
			}
			if start < 1 || end > specLines {
				failures = append(failures, fmt.Sprintf("docs/%s: Spec Lines ref %q is outside docs/spec.md:L1-L%d", entry.target, ref, specLines))
				continue
			}
			for line := start; line <= end; line++ {
				covered[line] = true
			}
		}
	}
	if openTasks == 0 {
		failures = append(failures, "docs/tasks.md has no open TASKs to carry spec coverage")
		return failures
	}
	var gaps []string
	for line := 1; line <= specLines; line++ {
		if !covered[line] {
			gaps = append(gaps, fmt.Sprintf("L%d", line))
			if len(gaps) >= 10 {
				break
			}
		}
	}
	if len(gaps) > 0 {
		failures = append(failures, fmt.Sprintf("docs/spec.md uncovered by open TASK Spec Lines: %s", strings.Join(gaps, ", ")))
	}
	return failures
}

func parseSpecLineRef(ref string) (int, int, bool) {
	matches := regexp.MustCompile(`^docs/spec\.md:L([0-9]+)(?:-L?([0-9]+))?$`).FindStringSubmatch(ref)
	if matches == nil {
		return 0, 0, false
	}
	start, err := strconv.Atoi(matches[1])
	if err != nil {
		return 0, 0, false
	}
	end := start
	if matches[2] != "" {
		end, err = strconv.Atoi(matches[2])
		if err != nil {
			return 0, 0, false
		}
	}
	if start <= 0 || end < start {
		return 0, 0, false
	}
	return start, end, true
}

func auditTaskScopeTruth(relative string, content string, info taskDetailInfo, icon string) []string {
	var failures []string
	if info.scopeType == "" {
		return failures
	}
	if info.scopeType == "Complete Feature" && donecheck.HasScopeReductionMarker(content) {
		failures = append(failures, fmt.Sprintf("%s: Complete Feature TASK must not contain gap/deferred/follow-up/partial language", relative))
	}
	if info.scopeType == "Slice" && donecheck.HasScopeReductionMarker(content) && !regexp.MustCompile(`TASK-[0-9]{4}-`).MatchString(content) {
		failures = append(failures, fmt.Sprintf("%s: Slice TASK may mention remaining work only when linked to concrete follow-up TASKs", relative))
	}
	if len(info.researchRefs) > 0 && !(len(info.researchRefs) == 1 && info.researchRefs[0] == "none") {
		planAndAcceptance := donecheck.ExtractSection(content, "## Technical Plan") + "\n" + donecheck.ExtractSection(content, "## Acceptance")
		normalized := strings.ToLower(planAndAcceptance)
		if !strings.Contains(normalized, "research") || !strings.Contains(normalized, "read") {
			failures = append(failures, fmt.Sprintf("%s: TASK with Research Refs must require reading/adapting the referenced research before implementation", relative))
		}
	}
	if icon == "x" && info.scopeType == "Complete Feature" && donecheck.HasScopeReductionMarker(donecheck.ExtractSection(content, "## Final Reality Check")) {
		failures = append(failures, fmt.Sprintf("%s: Done Complete Feature TASK cannot close with unresolved follow-up or reduced scope", relative))
	}
	return failures
}

func auditAgentHooks(root string) []string {
	cfg, cfgFailures := loadStackConfig(root)
	if len(cfgFailures) > 0 {
		return cfgFailures
	}
	var failures []string
	hooks := map[string][]string{}
	if cfg.AgentHooks.RequireCodexConfig {
		hooks[filepath.Join(root, ".codex/config.toml")] = []string{
			"[features]",
			"hooks = true",
		}
	}
	if cfg.AgentHooks.RequireCodexHookFile {
		hooks[filepath.Join(root, ".codex/hooks.json")] = []string{
			"tools/reconc/bin/hook",
			"RECONC_HOOK_REPO_RESOLVED=1",
			"codex-session-start",
			"codex-pre-tool-use",
			"codex-post-tool-use",
			"codex-stop",
		}
	}
	if cfg.AgentHooks.RequireCursorHooks {
		hooks[filepath.Join(root, ".cursor/hooks.json")] = []string{
			"sh -lc",
			"failClosed",
			"tools/reconc/bin/hook",
			"RECONC_HOOK_REPO_RESOLVED=1",
			"cursor-session-start",
			"cursor-pre-tool-use",
			"cursor-post-tool-use",
			"cursor-before-shell-execution",
			"cursor-after-shell-execution",
			"cursor-after-file-edit",
			"cursor-after-tab-file-edit",
			"Write|Edit|MultiEdit|StrReplace|Delete|FileEdit|TabWrite",
			"StrReplace|Delete|FileEdit",
			"cursor-stop",
		}
	}
	if cfg.AgentHooks.RequireClaudeSettings {
		hooks[filepath.Join(root, ".claude/settings.json")] = []string{
			"Edit|Write|MultiEdit|NotebookEdit|TabWrite|StrReplace|Delete|Bash",
			"Read|Edit|Write|MultiEdit|NotebookEdit|TabWrite|StrReplace|Delete|Bash",
			"tools/reconc/bin/hook",
			"\"args\"",
			"claude-session-start",
			"claude-post-compaction",
			`"compact"`,
			`"timeout"`,
			"claude-pre-tool-use",
			"claude-post-tool-use",
			"claude-stop",
			"CLAUDE_PROJECT_DIR",
		}
	}
	if cfg.AgentHooks.RequireOpenCodePlugin {
		hooks[filepath.Join(root, ".opencode/plugins/reconc.js")] = []string{
			"Managed by reconc",
			"Policy, session state, and continuation decisions stay in the Go runtime",
			`const wrapper = repo + "/tools/reconc/bin/hook"`,
			`return [wrapper, event, repo]`,
			`killSignal: "SIGKILL"`,
			"opencode-session-start",
			"opencode-pre-tool-use",
			"opencode-permission-request",
			"opencode-post-tool-use",
			"opencode-post-tool-use-failure",
			"opencode-post-compaction",
			"opencode-session-end",
			"opencode-stop",
			"session.idle",
			"client.session.prompt",
		}
	}
	if cfg.AgentHooks.RequireDevinHooks {
		hooks[filepath.Join(root, ".devin/hooks.v1.json")] = []string{
			"DEVIN_PROJECT_DIR",
			"tools/reconc/bin/hook",
			"devin-session-start",
			"devin-pre-tool-use",
			"devin-permission-request",
			"devin-post-tool-use",
			"devin-post-compaction",
			"devin-stop",
			"devin-session-end",
		}
	}
	if cfg.AgentHooks.RequireAntigravityHooks {
		hooks[filepath.Join(root, ".agents/hooks.json")] = []string{
			"sh -lc",
			"tools/reconc/bin/hook",
			"RECONC_HOOK_REPO_RESOLVED=1",
			"antigravity-pre-invocation",
			"antigravity-pre-tool-use",
			"antigravity-post-tool-use",
			"antigravity-post-invocation",
			"antigravity-stop",
			"write_to_file|replace_file_content|multi_replace_file_content|run_command",
			"view_file|write_to_file|replace_file_content|multi_replace_file_content|list_dir|find_by_name|grep_search|run_command",
		}
	}
	if cfg.AgentHooks.RequireKiloPlugin {
		hooks[filepath.Join(root, ".kilo/plugin/reconc.js")] = []string{
			"Managed by reconc",
			"Policy, session state, and continuation decisions stay in the Go runtime",
			`const wrapper = repo + "/tools/reconc/bin/hook"`,
			`return [wrapper, event, repo]`,
			`killSignal: "SIGKILL"`,
			"kilo-session-start",
			"kilo-pre-tool-use",
			"kilo-permission-request",
			"kilo-post-tool-use",
			"kilo-post-tool-use-failure",
			"kilo-post-compaction",
			"kilo-session-end",
			"kilo-stop",
			`export default { id: "reconc", server: ReconcKiloServer }`,
		}
	}
	if cfg.AgentHooks.RequireGrokHooks {
		hooks[filepath.Join(root, ".grok/hooks/reconc.json")] = []string{
			`"reconcManaged": true`,
			"tools/reconc/bin/hook",
			"grok-session-start",
			"grok-user-prompt-submit",
			"grok-pre-tool-use",
			"grok-post-tool-use",
			"grok-post-tool-use-failure",
			"grok-permission-denied",
			"grok-stop",
			"grok-stop-failure",
			"grok-notification",
			"grok-subagent-start",
			"grok-subagent-stop",
			"grok-pre-compaction",
			"grok-post-compaction",
			"grok-session-end",
			`{\"decision\":\"deny\"`,
			"run_terminal_command",
			"run_terminal_cmd",
			"hashline_edit",
		}
	}
	forbidden := map[string][]string{
		".claude/settings.json":       {`"PostCompact"`, `"UserPromptSubmit"`, "claude-user-prompt-submit"},
		".codex/hooks.json":           {`"UserPromptSubmit"`, "codex-user-prompt-submit", `"SessionEnd"`, "codex-session-end"},
		".cursor/hooks.json":          {`"beforeSubmitPrompt"`, "cursor-user-prompt-submit"},
		".opencode/plugins/reconc.js": {".reconc/runloop", "chat.message", "opencode-user-prompt-submit", "runloop autocontinue", "opencode_continuation_driver", "STFU", "tools/reconc/dist", "reconc-0.6.0-"},
		".devin/hooks.v1.json":        {`"UserPromptSubmit"`, "devin-user-prompt-submit"},
		".kilo/plugin/reconc.js":      {".reconc/runloop", "chat.message", "kilo-user-prompt-submit", "runloop autocontinue", "opencode_continuation_driver", "STFU", "tools/reconc/dist", "reconc-0.6.0-"},
		".agents/hooks.json":          {`"timeout": 120`},
		".grok/hooks/reconc.json":     {"claude-", "cursor-", "opencode-", "kilo-"},
	}
	for path, required := range hooks {
		relative := filepath.ToSlash(rel(root, path))
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s missing or unreadable: %v", relative, err))
			continue
		}
		content := string(contentBytes)
		if strings.HasSuffix(relative, ".json") && !json.Valid(contentBytes) {
			failures = append(failures, fmt.Sprintf("%s is not valid JSON", relative))
			continue
		}
		for _, token := range required {
			if !strings.Contains(content, token) {
				failures = append(failures, fmt.Sprintf("%s missing required Reconc hook token %q", relative, token))
			}
		}
		for _, token := range forbidden[relative] {
			if strings.Contains(content, token) {
				failures = append(failures, fmt.Sprintf("%s contains forbidden Reconc hook token %q", relative, token))
			}
		}
		if relative == ".grok/hooks/reconc.json" {
			failures = append(failures, auditGrokRouteCoverage(relative, content)...)
		}
		if relative == ".codex/hooks.json" || relative == ".cursor/hooks.json" || relative == ".claude/settings.json" || relative == ".agents/hooks.json" {
			for _, stale := range []string{"reconc hook runtime ", "tools/reconc/dist/reconc-0.5.0-", "for bin in "} {
				if strings.Contains(content, stale) {
					failures = append(failures, fmt.Sprintf("%s uses stale heavy hook command token %q; reinstall hooks so configs exec tools/reconc/bin/hook only", relative, stale))
				}
			}
			if relative == ".cursor/hooks.json" {
				for _, staleReadHook := range []string{"beforeReadFile", "beforeTabFileRead", "cursor-before-read-file", "cursor-before-tab-file-read"} {
					if strings.Contains(content, staleReadHook) {
						failures = append(failures, fmt.Sprintf("%s uses stale read-only pre-hook token %q; reinstall hooks so read evidence stays PostToolUse-only", relative, staleReadHook))
					}
				}
			}
			failures = append(failures, auditHookLauncherShape(relative, content)...)
		} else if relative == ".devin/hooks.v1.json" {
			failures = append(failures, auditHookLauncherShape(relative, content)...)
		}
	}
	return failures
}

func auditGrokRouteCoverage(relative string, content string) []string {
	var decoded interface{}
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		return []string{fmt.Sprintf("%s is not valid JSON: %v", relative, err)}
	}
	expected := []string{
		"grok-session-start",
		"grok-user-prompt-submit",
		"grok-pre-tool-use",
		"grok-post-tool-use",
		"grok-post-tool-use-failure",
		"grok-permission-denied",
		"grok-stop",
		"grok-stop-failure",
		"grok-notification",
		"grok-subagent-start",
		"grok-subagent-stop",
		"grok-pre-compaction",
		"grok-post-compaction",
		"grok-session-end",
	}
	seen := make(map[string]bool, len(expected))
	visitJSONCommands(decoded, func(command string, _ interface{}) {
		for _, field := range strings.Fields(command) {
			field = strings.Trim(field, "'\"")
			for _, route := range expected {
				if field == route {
					seen[route] = true
				}
			}
		}
	})
	var failures []string
	for _, route := range expected {
		if !seen[route] {
			failures = append(failures, fmt.Sprintf("%s missing exact native Grok route %q", relative, route))
		}
	}
	return failures
}

func auditHookLauncherShape(relative string, content string) []string {
	var decoded interface{}
	if err := json.Unmarshal([]byte(content), &decoded); err != nil {
		return []string{fmt.Sprintf("%s is not valid JSON: %v", relative, err)}
	}
	var failures []string
	visitJSONCommands(decoded, func(command string, args interface{}) {
		if !strings.Contains(command, "tools/reconc/bin/hook") {
			return
		}
		if relative == ".claude/settings.json" {
			if strings.Contains(command, "sh -lc") || strings.Contains(command, "git -C") {
				failures = append(failures, fmt.Sprintf("%s uses shell/git launcher for Claude exec-form hook command %q", relative, command))
			}
			if values, ok := args.([]interface{}); !ok || len(values) < 2 {
				failures = append(failures, fmt.Sprintf("%s Claude hook command %q is missing exec-form args", relative, command))
			}
			return
		}
		if relative == ".devin/hooks.v1.json" {
			if strings.Contains(command, "sh -lc") || strings.Contains(command, "git -C") {
				failures = append(failures, fmt.Sprintf("%s uses shell/git launcher for Devin direct hook command %q", relative, command))
			}
			if !strings.Contains(command, "DEVIN_PROJECT_DIR") {
				failures = append(failures, fmt.Sprintf("%s Devin hook command %q does not use DEVIN_PROJECT_DIR", relative, command))
			}
			return
		}
		for _, token := range []string{
			`hook="$repo/tools/reconc/bin/hook"`,
			`if [ -x "$hook" ]; then exec "$hook"`,
			`git -C "$repo" rev-parse --show-toplevel`,
			`RECONC_HOOK_REPO_RESOLVED=1 exec "$repo/tools/reconc/bin/hook"`,
		} {
			if !strings.Contains(command, token) {
				failures = append(failures, fmt.Sprintf("%s hook command for tools/reconc/bin/hook missing fast-launch token %q", relative, token))
			}
		}
	})
	return failures
}

func visitJSONCommands(value interface{}, visit func(command string, args interface{})) {
	switch v := value.(type) {
	case map[string]interface{}:
		if command, ok := v["command"].(string); ok {
			visit(command, v["args"])
		}
		for _, child := range v {
			visitJSONCommands(child, visit)
		}
	case []interface{}:
		for _, child := range v {
			visitJSONCommands(child, visit)
		}
	}
}

func auditRepoCleanliness(root string) []string {
	insideCommand, cancel := commandWithTimeout(shortAuditCommandTimeout, "git", "-C", root, "rev-parse", "--is-inside-work-tree")
	inside, err := insideCommand.CombinedOutput()
	cancel()
	if err != nil {
		if _, statErr := os.Stat(filepath.Join(root, ".git")); statErr == nil {
			return []string{fmt.Sprintf("git rev-parse --is-inside-work-tree failed: %v: %s", err, strings.TrimSpace(string(inside)))}
		}
		return nil
	}
	if strings.TrimSpace(string(inside)) != "true" {
		return nil
	}
	cleanCommand, cancel := commandWithTimeout(shortAuditCommandTimeout, "git", "-C", root, "clean", "-nd")
	out, err := cleanCommand.CombinedOutput()
	cancel()
	if err != nil {
		return []string{fmt.Sprintf("git clean dry-run failed: %v: %s", err, strings.TrimSpace(string(out)))}
	}
	lines := repoCleanDryRunLines(string(out))
	if len(lines) == 0 {
		return nil
	}
	var failures []string
	for _, line := range lines {
		failures = append(failures, fmt.Sprintf("untracked non-ignored repo content is not protected by Git and would be deleted by git clean: %s", line))
	}
	return failures
}

func repoCleanDryRunLines(output string) []string {
	var lines []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func auditFinalRealityCheck(relative string, content string) []string {
	raw := donecheck.CheckFinalRealityCheck(content, loadedSchema)
	if len(raw) == 0 {
		return nil
	}
	failures := make([]string, len(raw))
	for i, msg := range raw {
		failures[i] = fmt.Sprintf("%s: %s", relative, msg)
	}
	return failures
}

func auditUnreferencedTaskFiles(root string, referenced map[string]bool) []string {
	var failures []string
	for _, pattern := range []string{"docs/tasks/*.md", "docs/tasks/done/*.md"} {
		matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(pattern)))
		if err != nil {
			return []string{fmt.Sprintf("glob %s: %v", pattern, err)}
		}
		for _, match := range matches {
			if filepath.Base(match) == ".gitkeep" {
				continue
			}
			target := filepath.ToSlash(rel(filepath.Join(root, "docs"), match))
			if !referenced[filepath.Clean(target)] {
				failures = append(failures, fmt.Sprintf("docs/%s exists but is not referenced from docs/tasks.md; if you are completing a TASK, run `tools/reconc/harness/template/utils/promote-task-done/run-promote-task-done` (or with --dry-run) to atomically move the detail file and update tasks.md instead of editing them separately", target))
			}
		}
	}
	return failures
}

func auditStartEntrypoint(root string) []string {
	path := filepath.Join(root, "start.md")
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("read start.md: %v", err)}
	}
	content := string(contentBytes)
	var failures []string
	required := []string{
		"AGENTS.md",
		"docs/tasks.md",
		"session-briefing . --json",
		"tools/reconc/dist/reconc-<os>-<arch>",
		"No file writes",
		"_drop/",
		"reconc run on .",
		"reconc run off .",
		"reconc run status .",
		"not a parallel workflow",
		"only the current invocation",
		"only manual disable action",
	}
	for _, token := range required {
		if !strings.Contains(content, token) {
			failures = append(failures, fmt.Sprintf("start.md missing required onboarding token %q", token))
		}
	}
	forbidden := []string{"docs/todo", "active-task.md", "/runloop", "RUNLOOP_CONTINUE", "utils/runloop.go", ".reconc/runloop/stop"}
	for _, token := range forbidden {
		if strings.Contains(content, token) {
			failures = append(failures, fmt.Sprintf("start.md contains forbidden stale/setup token %q", token))
		}
	}
	agentsBytes, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		failures = append(failures, fmt.Sprintf("read AGENTS.md: %v", err))
		return failures
	}
	agentsContent := string(agentsBytes)
	agentsRequired := []string{
		"reconc run on .",
		"reconc run off .",
		"only the current invocation",
		"only manual disable action",
		"not a second workflow",
		"fully autonomous, non-interactive mode",
	}
	for _, token := range agentsRequired {
		if !strings.Contains(agentsContent, token) {
			failures = append(failures, fmt.Sprintf("AGENTS.md missing required repository run token %q", token))
		}
	}
	return failures
}

func auditSpecFormat(root string) []string {
	path := filepath.Join(root, "docs/spec.md")
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return []string{fmt.Sprintf("read docs/spec.md: %v", err)}
	}
	var failures []string
	for i, line := range strings.Split(string(contentBytes), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			failures = append(failures, fmt.Sprintf("docs/spec.md line %d: fenced code blocks are forbidden", i+1))
		}
	}
	return failures
}

// auditGeneratedReferences runs the generated-reference drift audit. Its input
// fingerprint cache skips the subprocess only when every declared input is
// unchanged. On a cache miss the audit binary is rebuilt from source rather
// than trusted by mtime, so restored timestamps cannot validate stale code.
func auditGeneratedReferences(root string) []string {
	cfg, cfgFailures := loadStackConfig(root)
	if len(cfgFailures) > 0 {
		return cfgFailures
	}
	if !cfg.GeneratedReferences.Enabled {
		return nil
	}
	inputs := newCacheInputs()
	inputs.AddFile(stackConfigPath(root))
	inputs.AddFile(filepath.Join(projectRoot(root), "backend/shared/contracts/generated_reference/contracts.yaml"))
	inputs.AddTree(filepath.Join(projectRoot(root), "backend/shared/contracts/generated_reference/generated"), nil)
	inputs.AddTree(filepath.Join(projectRoot(root), "scripts/generators/generated_reference"), []string{".go"})
	inputs.AddTree(filepath.Join(root, "tools/reconc/harness/template/audits/generated_reference"), []string{".go"})
	return runWithCache(root, "generated-references", inputs, func() []string {
		bin := filepath.Join(root, ".reconc/cache/generated-reference-audit")
		src := filepath.Join(root, "tools/reconc/harness/template/audits/generated_reference")
		if err := validateGeneratedReferenceSource(src); err != nil {
			return []string{err.Error()}
		}
		build, cancel := commandWithTimeout(buildAuditCommandTimeout, "go", "build", "-ldflags=-s -w", "-trimpath", "-buildvcs=false", "-o", bin, "./audits/generated_reference")
		build.Dir = filepath.Join(root, "tools/reconc/harness/template")
		out, err := build.CombinedOutput()
		cancel()
		if err != nil {
			return []string{fmt.Sprintf("generated-references audit build failed: %v\n%s", err, string(out))}
		}
		cmd, cancel := commandWithTimeout(buildAuditCommandTimeout, bin)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			return []string{fmt.Sprintf("generated-references audit failed: %v\n%s", err, string(output))}
		}
		return nil
	})
}

func validateGeneratedReferenceSource(srcDir string) error {
	found := false
	err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		found = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("generated-references audit source unavailable: %w", err)
	}
	if !found {
		return fmt.Errorf("generated-references audit source contains no Go files: %s", srcDir)
	}
	return nil
}

func auditBuildBaseline(root string) []string {
	cfg, cfgFailures := loadStackConfig(root)
	if len(cfgFailures) > 0 {
		return cfgFailures
	}
	if !cfg.Build.Enabled {
		return nil
	}
	var failures []string
	var requiredFiles []string
	if cfg.Build.RequireGoMod {
		requiredFiles = append(requiredFiles, "go.mod")
	}
	if cfg.Build.RequireCargoToml {
		requiredFiles = append(requiredFiles, "Cargo.toml")
	}
	if cfg.Build.RequireFrontendPackage {
		requiredFiles = append(requiredFiles, projectRel(root, "frontend/package.json"))
	}
	if cfg.Build.RequireBuildRunner {
		requiredFiles = append(requiredFiles, projectRel(root, "scripts/build/build.go"))
	}
	if cfg.Build.RequireBuildRunnerTest {
		requiredFiles = append(requiredFiles, projectRel(root, "scripts/build/build_test.go"))
	}
	for _, entrypoint := range cfg.Build.BackendEntrypoints {
		requiredFiles = append(requiredFiles, projectRel(root, filepath.ToSlash(filepath.Join("backend", entrypoint, "main.go"))))
	}
	for _, relative := range requiredFiles {
		if !exists(filepath.Join(root, filepath.FromSlash(relative))) {
			failures = append(failures, fmt.Sprintf("build baseline missing %s", relative))
		}
	}
	if cfg.Build.RequireGoMod {
		goMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
		if err != nil {
			failures = append(failures, fmt.Sprintf("read go.mod: %v", err))
		} else {
			text := string(goMod)
			for _, token := range cfg.Build.GoModTokens {
				if !strings.Contains(text, token) {
					failures = append(failures, fmt.Sprintf("go.mod missing %q", token))
				}
			}
		}
	}
	if cfg.Build.RequireCargoToml {
		cargoToml, err := os.ReadFile(filepath.Join(root, "Cargo.toml"))
		if err != nil {
			failures = append(failures, fmt.Sprintf("read Cargo.toml: %v", err))
		} else {
			text := string(cargoToml)
			for _, token := range cfg.Build.CargoTomlTokens {
				if !strings.Contains(text, token) {
					failures = append(failures, fmt.Sprintf("Cargo.toml missing %q", token))
				}
			}
		}
	}
	if cfg.Build.RequireFrontendPackage {
		packageJSONRel := projectRel(root, "frontend/package.json")
		packageJSON, err := os.ReadFile(filepath.Join(root, packageJSONRel))
		if err != nil {
			failures = append(failures, fmt.Sprintf("read %s: %v", packageJSONRel, err))
		} else {
			text := string(packageJSON)
			for _, token := range cfg.Build.FrontendPackageTokens {
				if !strings.Contains(text, token) {
					failures = append(failures, fmt.Sprintf("%s missing %s", packageJSONRel, token))
				}
			}
			for _, token := range cfg.Build.ForbiddenFrontendTokens {
				if strings.Contains(text, token) {
					failures = append(failures, fmt.Sprintf("%s uses forbidden package manager token %s", packageJSONRel, token))
				}
			}
		}
	}
	if cfg.Build.RequireBuildRunner {
		buildRunnerRel := projectRel(root, "scripts/build/build.go")
		buildRunner, err := os.ReadFile(filepath.Join(root, buildRunnerRel))
		if err != nil {
			failures = append(failures, fmt.Sprintf("read %s: %v", buildRunnerRel, err))
		} else {
			text := string(buildRunner)
			for _, token := range cfg.Build.BuildRunnerTokens {
				if !strings.Contains(text, token) {
					failures = append(failures, fmt.Sprintf("%s missing build-baseline token %q", buildRunnerRel, token))
				}
			}
		}
	}
	return failures
}

func auditDurableStoreBaseline(root string) []string {
	cfg, cfgFailures := loadStackConfig(root)
	if len(cfgFailures) > 0 {
		return cfgFailures
	}
	if !cfg.DurableStore.Enabled {
		return nil
	}
	var failures []string
	requiredFiles := append([]string{}, cfg.DurableStore.StoreFiles...)
	requiredFiles = append(requiredFiles, cfg.DurableStore.MigrationGoFiles...)
	if cfg.DurableStore.InitialSQL != "" {
		requiredFiles = append(requiredFiles, cfg.DurableStore.InitialSQL)
	}
	for _, relative := range requiredFiles {
		relative = stackProjectRel(root, cfg, relative)
		if !exists(filepath.Join(root, filepath.FromSlash(relative))) {
			failures = append(failures, fmt.Sprintf("durable store baseline missing %s", relative))
		}
	}
	if len(cfg.DurableStore.StoreFiles) > 0 {
		storeRel := stackProjectRel(root, cfg, cfg.DurableStore.StoreFiles[0])
		storeGo, err := os.ReadFile(filepath.Join(root, storeRel))
		if err != nil {
			failures = append(failures, fmt.Sprintf("read %s: %v", storeRel, err))
		} else {
			text := string(storeGo)
			for _, token := range cfg.DurableStore.StoreGoTokens {
				if !strings.Contains(text, token) {
					failures = append(failures, fmt.Sprintf("store.go missing durable-store token %q", token))
				}
			}
		}
	}
	if cfg.DurableStore.InitialSQL != "" {
		initialSQLRel := stackProjectRel(root, cfg, cfg.DurableStore.InitialSQL)
		initialSQL, err := os.ReadFile(filepath.Join(root, initialSQLRel))
		if err != nil {
			failures = append(failures, fmt.Sprintf("read %s: %v", initialSQLRel, err))
		} else {
			text := string(initialSQL)
			for _, token := range cfg.DurableStore.InitialSQLTokens {
				if !strings.Contains(text, token) {
					failures = append(failures, fmt.Sprintf("initial migration missing durable-store token %q", token))
				}
			}
		}
	}
	return failures
}

func auditTestCoverage(root string) []string {
	var failures []string
	owners := []string{"tools/reconc"}
	if exists(filepath.Join(root, "codebase")) {
		owners = append([]string{"codebase"}, owners...)
	} else {
		for _, owner := range []string{"backend", "frontend", "scripts", "shared", "modules"} {
			if exists(filepath.Join(root, owner)) {
				owners = append([]string{owner}, owners...)
			}
		}
	}
	codebaseBuildPrefix := projectRel(root, "build")
	for _, owner := range owners {
		base := filepath.Join(root, filepath.FromSlash(owner))
		if !exists(base) {
			continue
		}
		err := filepath.WalkDir(base, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				failures = append(failures, fmt.Sprintf("walk %s: %v", rel(root, path), err))
				return nil
			}
			if entry.IsDir() {
				relative := filepath.ToSlash(rel(root, path))
				switch {
				case relative == ".":
					return nil
				case entry.Name() == "node_modules" || entry.Name() == "dist" || entry.Name() == "build":
					return filepath.SkipDir
				case strings.HasPrefix(relative, codebaseBuildPrefix):
					return filepath.SkipDir
				case strings.HasPrefix(relative, "tools/reconc/dist"):
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				return nil
			}
			if strings.Contains(entry.Name(), ".generated.") || strings.HasSuffix(entry.Name(), "_generated.go") {
				return nil
			}
			testMatches, globErr := filepath.Glob(filepath.Join(filepath.Dir(path), "*_test.go"))
			if globErr != nil {
				failures = append(failures, fmt.Sprintf("glob tests for %s: %v", rel(root, path), globErr))
				return nil
			}
			if len(testMatches) == 0 {
				failures = append(failures, fmt.Sprintf("%s: Go code directory has no co-located *_test.go; code TASKs require same-package substantive tests", rel(root, path)))
			}
			return nil
		})
		if err != nil {
			failures = append(failures, fmt.Sprintf("walk %s: %v", owner, err))
		}
	}
	return failures
}

func taskHasCodeSurface(info taskDetailInfo) bool {
	for _, surface := range info.touchSurfaces {
		normalized := normalizeTouchSurface(surface)
		codePrefixes := []string{
			"codebase/backend/",
			"codebase/frontend/",
			"codebase/scripts/",
			"codebase/shared/",
			"backend/",
			"frontend/",
			"scripts/",
			"shared/",
			"tools/reconc/",
		}
		for _, prefix := range codePrefixes {
			if strings.HasPrefix(normalized, prefix) {
				return true
			}
		}
	}
	return false
}

func countSubtaskIcon(content string, icon string) int {
	count := 0
	prefix := "- [" + icon + "]"
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), prefix) {
			count++
		}
	}
	return count
}

func hasTaskBullet(content string) bool {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "- ") {
			return true
		}
	}
	return false
}

func parseStatusState(section string) string {
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "State: ") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, "State: "))
		}
	}
	return ""
}

func parseSchedulingFields(section string) (string, string, []string, string, []string, string, string, string, []string, string) {
	fields := donecheck.ParseBulletFields(section)
	priority := strings.TrimSpace(fields["Priority"])
	dependsRaw := strings.TrimSpace(fields["Depends On"])
	if dependsRaw == "" {
		dependsRaw = strings.TrimSpace(fields["Depends"])
	}
	dependencies := parseDependencies(dependsRaw)
	parallelGroup := strings.TrimSpace(fields["Parallel Group"])
	touchSurfaces := parseCSVFields(fields["Expected Touch Surfaces"])
	orderRationale := strings.TrimSpace(fields["Order Rationale"])
	scopeType := strings.TrimSpace(fields["Scope Type"])
	specLinesRaw := strings.TrimSpace(fields["Spec Lines"])
	researchRaw := strings.TrimSpace(fields["Research Refs"])
	researchRefs := parseCSVFields(researchRaw)
	if researchRaw == "none" {
		researchRefs = []string{"none"}
	}
	completionClaim := strings.TrimSpace(fields["Completion Claim"])
	return priority, dependsRaw, dependencies, parallelGroup, touchSurfaces, orderRationale, scopeType, specLinesRaw, researchRefs, completionClaim
}

func parseDependencies(value string) []string {
	return parseCSVFields(value)
}

func parseCSVFields(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || value == "none" {
		return nil
	}
	var values []string
	for _, part := range strings.Split(value, ",") {
		value := strings.TrimSpace(part)
		if value != "" {
			values = append(values, value)
		}
	}
	return values
}

func invalidTouchSurface(surface string) bool {
	normalized := normalizeTouchSurface(surface)
	if normalized == "" || donecheck.IsPlaceholderValue(normalized, loadedSchema) {
		return true
	}
	if strings.HasPrefix(normalized, "/") || strings.Contains(normalized, "..") || strings.ContainsAny(normalized, "\\\t\n\r") {
		return true
	}
	if normalized == "." || normalized == "*" || normalized == "**" || normalized == "codebase" || normalized == "docs" || normalized == "research" {
		return true
	}
	return false
}

func validPriority(priority string) bool {
	switch priority {
	case "P0", "P1", "P2", "P3":
		return true
	default:
		return false
	}
}

func validStatusState(state string) bool {
	switch state {
	case "Active", "Queued", "Blocked", "Paused", "Done":
		return true
	default:
		return false
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func rel(root string, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return relative
}

func lineCount(content string) int {
	if content == "" {
		return 0
	}
	// Match wc -l / editor line numbering: a trailing newline terminates the
	// final line and must not add a phantom extra line. Only an unterminated
	// final line counts as the extra +1.
	n := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		n++
	}
	return n
}

func block(format string, args ...interface{}) {
	fmt.Printf(format+"\n", args...)
	os.Exit(2)
}
