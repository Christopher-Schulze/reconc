package assurance

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"reconc.dev/reconc/internal/pathidentity"
	"reconc.dev/reconc/internal/policy"
	"reconc.dev/reconc/internal/stackdetect"
)

type moduleScope struct {
	repoRootAbs             string
	rootAbs                 string
	rootRel                 string
	manifest                string
	workspaceRootAbs        string
	workspaceRootRel        string
	requireWorkingDirectory bool
	changedPaths            []string
	effectivePaths          []string
}

// selectModuleScopes returns module-owned scopes for a gate whose
// applicable_if patterns identify one or more manifests. The second return
// value distinguishes "module-aware gate with no changed owner" from a legacy
// gate that should retain repository-root semantics.
func selectModuleScopes(root string, detection stackdetect.Result, gate policy.AssuranceGate, changed []string) ([]moduleScope, bool, error) {
	if len(gate.ApplicableIf) == 0 {
		return nil, false, nil
	}
	for _, pattern := range gate.ApplicableIf {
		if !doublestar.ValidatePattern(filepath.ToSlash(pattern)) {
			return nil, true, doublestar.ErrBadPattern
		}
		clean := filepath.Clean(filepath.FromSlash(pattern))
		if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, true, fmt.Errorf("applicability pattern escapes repository: %q", pattern)
		}
	}
	if err := verifyChangedModuleManifests(root, changed, gate.ApplicableIf); err != nil {
		return nil, true, err
	}
	matching := make([]stackdetect.Module, 0, len(detection.Modules))
	for _, module := range detection.Modules {
		if !moduleSupportsAssuranceScope(module.Stack) {
			continue
		}
		matched, err := moduleManifestMatches(module.Manifest, gate.ApplicableIf)
		if err != nil {
			return nil, true, err
		}
		if !matched {
			continue
		}
		if err := verifyModuleManifest(root, module.Manifest); err != nil {
			return nil, true, err
		}
		matching = append(matching, module)
	}
	if len(matching) == 0 {
		if missingModuleEvidence(changed, gate.ApplicableIf) {
			return nil, true, fmt.Errorf("no detected module root covers changed paths for applicable module manifest patterns %v", gate.ApplicableIf)
		}
		return nil, false, nil
	}

	assigned := make(map[string][]string, len(matching))
	assignedEffective := make(map[string][]string, len(matching))
	for _, raw := range changed {
		relative := filepath.ToSlash(filepath.Clean(raw))
		owner, ok := nearestModuleOwner(relative, matching)
		if !ok {
			continue
		}
		key := owner.Root + "\x00" + owner.Stack
		assigned[key] = append(assigned[key], moduleRelativePath(relative, owner.Root))
		assignedEffective[key] = append(assignedEffective[key], relative)
	}
	if missingModuleOwner(changed, matching, gate.ApplicableIf) {
		return nil, true, fmt.Errorf("no detected module root covers changed paths for applicable module manifest patterns %v", gate.ApplicableIf)
	}
	if len(changed) == 0 {
		for _, module := range matching {
			key := module.Root + "\x00" + module.Stack
			assigned[key] = []string{}
			assignedEffective[key] = []string{}
		}
	}

	scopes := make([]moduleScope, 0, len(assigned))
	for _, module := range matching {
		key := module.Root + "\x00" + module.Stack
		effective, ok := assignedEffective[key]
		if !ok {
			continue
		}
		moduleRoot := filepath.Join(root, filepath.FromSlash(module.Root))
		resolvedRoot, err := pathidentity.ResolveExisting(moduleRoot)
		if err != nil {
			return nil, true, fmt.Errorf("resolve assurance module root %s: %w", module.Root, err)
		}
		workspaceRootAbs := ""
		if module.WorkspaceRoot != "" {
			workspaceRootAbs, err = pathidentity.ResolveExisting(filepath.Join(root, filepath.FromSlash(module.WorkspaceRoot)))
			if err != nil {
				return nil, true, fmt.Errorf("resolve assurance workspace root %s: %w", module.WorkspaceRoot, err)
			}
		}
		local := append([]string(nil), assigned[key]...)
		sort.Strings(local)
		sort.Strings(effective)
		requireWorkingDirectory := false
		if module.Root == "." {
			for _, other := range matching {
				if other.Stack == module.Stack && other.Root != "." {
					requireWorkingDirectory = true
					break
				}
			}
		}
		scopes = append(scopes, moduleScope{
			repoRootAbs: root, rootAbs: resolvedRoot, rootRel: module.Root, manifest: module.Manifest,
			workspaceRootAbs: workspaceRootAbs, workspaceRootRel: module.WorkspaceRoot,
			requireWorkingDirectory: requireWorkingDirectory,
			changedPaths:            local, effectivePaths: append([]string(nil), effective...),
		})
	}
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].rootRel != scopes[j].rootRel {
			return scopes[i].rootRel < scopes[j].rootRel
		}
		return scopes[i].manifest < scopes[j].manifest
	})
	return scopes, true, nil
}

func verifyChangedModuleManifests(root string, changed, patterns []string) error {
	for _, raw := range changed {
		manifest, ok := stackdetect.CanonicalDiscoveryPath(raw)
		if !ok || !isAssuranceModuleManifest(manifest) {
			continue
		}
		matched, err := moduleManifestMatches(manifest, patterns)
		if err != nil {
			return err
		}
		if !matched {
			continue
		}
		if err := verifyModuleManifest(root, manifest); err != nil {
			return err
		}
	}
	return nil
}

func isAssuranceModuleManifest(path string) bool {
	switch strings.ToLower(filepath.Base(filepath.FromSlash(path))) {
	case "go.mod", "cargo.toml", "pyproject.toml", "requirements.txt", "setup.cfg", "setup.py":
		return true
	default:
		return false
	}
}

func moduleSupportsAssuranceScope(stack string) bool {
	switch stack {
	case "go", "rust", "python":
		return true
	default:
		return false
	}
}

func missingModuleEvidence(changed, patterns []string) bool {
	stacks := moduleStacksForPatterns(patterns)
	if len(stacks) == 0 {
		return false
	}
	for _, path := range changed {
		if stack := moduleStackForChangedPath(path); stacks[stack] {
			return true
		}
	}
	return false
}

func missingModuleOwner(changed []string, modules []stackdetect.Module, patterns []string) bool {
	stacks := moduleStacksForPatterns(patterns)
	if len(stacks) == 0 {
		return false
	}
	for _, path := range changed {
		stack := moduleStackForChangedPath(path)
		if !stacks[stack] {
			continue
		}
		if _, ok := nearestModuleOwner(path, modules); !ok {
			return true
		}
	}
	return false
}

func moduleStacksForPatterns(patterns []string) map[string]bool {
	stacks := map[string]bool{}
	for _, pattern := range patterns {
		clean := filepath.ToSlash(pattern)
		for _, manifest := range []string{"go.mod", "Cargo.toml", "pyproject.toml", "requirements.txt", "setup.cfg", "setup.py", "package.json"} {
			if clean == manifest || strings.HasSuffix(clean, "/"+manifest) {
				stack := ""
				switch manifest {
				case "go.mod":
					stack = "go"
				case "Cargo.toml":
					stack = "rust"
				case "pyproject.toml", "requirements.txt", "setup.cfg", "setup.py":
					stack = "python"
				}
				if stack != "" {
					stacks[stack] = true
				}
			}
		}
	}
	return stacks
}

func moduleStackForChangedPath(path string) string {
	switch strings.ToLower(filepath.Ext(filepath.FromSlash(path))) {
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	case ".py":
		return "python"
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts":
		return "javascript"
	default:
		return ""
	}
}

func moduleManifestMatches(manifest string, patterns []string) (bool, error) {
	base := filepath.Base(filepath.FromSlash(manifest))
	for _, pattern := range patterns {
		pattern = filepath.ToSlash(pattern)
		if !strings.Contains(pattern, "/") && base == pattern {
			return true, nil
		}
		matched, err := doublestar.Match(pattern, manifest)
		if err != nil {
			return false, err
		}
		if matched {
			return true, nil
		}
	}
	return false, nil
}

func verifyModuleManifest(root, manifest string) error {
	path := filepath.Join(root, filepath.FromSlash(manifest))
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("applicable module manifest %s is unavailable: %w", manifest, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("applicable module manifest %s is not a regular file", manifest)
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("applicable module manifest %s is unreadable: %w", manifest, err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close applicable module manifest %s: %w", manifest, err)
	}
	return nil
}

func nearestModuleOwner(path string, modules []stackdetect.Module) (stackdetect.Module, bool) {
	var owner stackdetect.Module
	bestLength := -1
	for _, module := range modules {
		if !pathWithinModule(path, module.Root) {
			continue
		}
		length := len(module.Root)
		if module.Root == "." {
			length = 0
		}
		if length > bestLength {
			owner, bestLength = module, length
		}
	}
	return owner, bestLength >= 0
}

func pathWithinModule(path, moduleRoot string) bool {
	path = filepath.ToSlash(filepath.Clean(path))
	moduleRoot = filepath.ToSlash(filepath.Clean(moduleRoot))
	if moduleRoot == "." || moduleRoot == "" {
		return path != ".." && !strings.HasPrefix(path, "../")
	}
	return path == moduleRoot || strings.HasPrefix(path, moduleRoot+"/")
}

func moduleRelativePath(path, moduleRoot string) string {
	if moduleRoot == "." || moduleRoot == "" {
		return path
	}
	relative := strings.TrimPrefix(path, moduleRoot)
	return strings.TrimPrefix(relative, "/")
}

func rebaseScopedFindings(findings []Finding, scope moduleScope) []Finding {
	for index := range findings {
		finding := &findings[index]
		for pathIndex, relative := range finding.Paths {
			clean := filepath.ToSlash(filepath.Clean(relative))
			if scope.rootRel != "." && clean != "." && !strings.HasPrefix(clean, "../") {
				finding.Paths[pathIndex] = filepath.ToSlash(filepath.Join(scope.rootRel, filepath.FromSlash(clean)))
			} else {
				finding.Paths[pathIndex] = clean
			}
		}
		finding.ModuleRoot = scope.rootRel
		finding.Manifest = scope.manifest
		finding.EffectiveScope = append([]string(nil), scope.effectivePaths...)
	}
	return findings
}

func scopeCommandEvidence(repoRoot string, inputs Inputs, scope *moduleScope) map[string]bool {
	if scope != nil && scope.repoRootAbs != "" {
		repoRoot = scope.repoRootAbs
	}
	commands := map[string]bool{}
	for _, item := range commandEvidenceForInputs(inputs) {
		command := normalizeScopedCommand(item.Command)
		if command == "" || !commandEvidenceCoversScope(repoRoot, item, scope) {
			continue
		}
		commands[command] = true
	}
	return commands
}

func scopePackageScriptEvidence(repoRoot string, inputs Inputs, scope *moduleScope) map[string]bool {
	if scope != nil && scope.repoRootAbs != "" {
		repoRoot = scope.repoRootAbs
	}
	commands := map[string]bool{}
	for _, item := range commandEvidenceForInputs(inputs) {
		if !commandEvidenceCoversScope(repoRoot, item, scope) {
			continue
		}
		command := normalizeScopedCommand(item.Command)
		if command != "" {
			if scope != nil && scope.rootRel != "." {
				command = stripCommandDirectoryFlag(command)
			}
			commands[normalizePackageScriptEvidence(command)] = true
		}
	}
	return commands
}

func stripCommandDirectoryFlag(command string) string {
	for _, flag := range []string{"--prefix", "--dir", "--cwd"} {
		marker := " " + flag
		index := strings.Index(command, marker)
		if index < 0 {
			continue
		}
		value := index + len(marker)
		if value < len(command) && command[value] == '=' {
			value++
		}
		for value < len(command) && (command[value] == ' ' || command[value] == '\t') {
			value++
		}
		length := shellWordLength(command[value:])
		if length == 0 {
			continue
		}
		return strings.TrimSpace(command[:index] + " " + command[value+length:])
	}
	return command
}

func shellWordLength(value string) int {
	if value == "" {
		return 0
	}
	if value[0] == '\'' || value[0] == '"' {
		if end := strings.IndexByte(value[1:], value[0]); end >= 0 {
			return end + 2
		}
		return 0
	}
	for index, character := range value {
		if character == ' ' || character == '\t' {
			return index
		}
	}
	return len(value)
}

func commandEvidenceForInputs(inputs Inputs) []CommandEvidence {
	evidence := make([]CommandEvidence, 0, len(inputs.SuccessfulCommands)+len(inputs.SuccessfulCommandEvidence))
	for _, command := range inputs.SuccessfulCommands {
		evidence = append(evidence, CommandEvidence{Command: command})
	}
	return append(evidence, inputs.SuccessfulCommandEvidence...)
}

func normalizeScopedCommand(command string) string {
	command = normalizeCommand(command)
	for _, separator := range []string{" && ", "; "} {
		if strings.HasPrefix(command, "cd ") {
			if index := strings.Index(command, separator); index >= 0 {
				command = strings.TrimSpace(command[index+len(separator):])
				break
			}
		}
	}
	return strings.TrimPrefix(command, "rtk ")
}

func commandEvidenceCoversScope(repoRoot string, evidence CommandEvidence, scope *moduleScope) bool {
	if scope == nil || (scope.rootRel == "." && !scope.requireWorkingDirectory) {
		return true
	}
	directory := strings.TrimSpace(evidence.WorkingDirectory)
	if directory == "" {
		directory = commandDirectory(evidence.Command)
	}
	if directory == "" {
		return false
	}
	if !filepath.IsAbs(filepath.FromSlash(directory)) {
		directory = filepath.Join(repoRoot, filepath.FromSlash(directory))
	}
	resolved, err := pathidentity.ResolveExisting(directory)
	if err != nil {
		return false
	}
	return resolved == scope.rootAbs || (scope.workspaceRootAbs != "" && resolved == scope.workspaceRootAbs)
}

func commandDirectory(command string) string {
	command = strings.TrimSpace(command)
	if strings.HasPrefix(command, "cd ") {
		remainder := command[len("cd "):]
		separator := -1
		for _, candidate := range []string{" && ", "; "} {
			if index := strings.Index(remainder, candidate); index >= 0 && (separator < 0 || index < separator) {
				separator = index
			}
		}
		if separator >= 0 {
			argument := strings.TrimSpace(remainder[:separator])
			if len(argument) >= 2 && ((argument[0] == '\'' && argument[len(argument)-1] == '\'') || (argument[0] == '"' && argument[len(argument)-1] == '"')) {
				argument = argument[1 : len(argument)-1]
			}
			if argument != "" && !strings.ContainsAny(argument, "$`*?[]{}") {
				return argument
			}
		}
	}
	for _, flag := range []string{"--prefix", "--dir", "--cwd"} {
		if value := commandFlagDirectory(command, flag); value != "" {
			return value
		}
	}
	return ""
}

func commandFlagDirectory(command, flag string) string {
	for _, marker := range []string{flag + "=", flag + " "} {
		index := strings.Index(command, marker)
		if index < 0 || index > 0 && !strings.ContainsAny(command[index-1:index], " \t") {
			continue
		}
		value := strings.TrimSpace(command[index+len(marker):])
		if value == "" {
			return ""
		}
		if value[0] == '\'' || value[0] == '"' {
			quote := value[0]
			if end := strings.IndexByte(value[1:], quote); end >= 0 {
				return value[1 : end+1]
			}
			return ""
		}
		return strings.Fields(value)[0]
	}
	return ""
}
