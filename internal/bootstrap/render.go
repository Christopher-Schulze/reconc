package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"reconc.dev/reconc/internal/execfile"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/repositoryignore"
)

const (
	agentBlockStart = "<!-- reconc-bootstrap:agent:start -->"
	agentBlockEnd   = "<!-- reconc-bootstrap:agent:end -->"
	docsBlockStart  = "<!-- reconc-bootstrap:docs:start -->"
	docsBlockEnd    = "<!-- reconc-bootstrap:docs:end -->"
)

func buildDesiredArtifacts(root string, selection Selection) ([]desiredArtifact, error) {
	profile, err := profileByName(selection.Profile)
	if err != nil {
		return nil, err
	}
	artifacts := []desiredArtifact{}
	if profile.Policy {
		artifacts = append(artifacts, textArtifact("policy", ".reconc.yml", 0o644, renderPolicy(selection.Packs, profile.Tasks)))
	}
	if profile.AgentDoc {
		content, err := renderManagedDocument(root, "AGENTS.md", "Repository instructions", agentBlockStart, agentBlockEnd, renderAgentBlock())
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, textArtifact("agent-doc", "AGENTS.md", 0o644, content))
	}
	if profile.Tasks {
		artifacts = append(artifacts,
			textArtifact("task-workflow", "docs/tasks.md", 0o644, renderTasksOverview()),
			textArtifact("task-workflow", "docs/tasks/001-bootstrap-reconc.md", 0o644, renderBootstrapTask()),
		)
	}
	if profile.Docs {
		content, err := renderManagedDocument(root, "docs/documentation.md", "Repository documentation", docsBlockStart, docsBlockEnd, renderDocumentationBlock())
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts,
			textArtifact("documentation", "docs/documentation.md", 0o644, content),
			textArtifact("documentation", "start.md", 0o644, renderStart()),
		)
	}
	if profile.Ignores {
		content, err := renderManagedFile(root, repositoryignore.RelativePath, repositoryignore.BlockStart, repositoryignore.BlockEnd, repositoryignore.Body())
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, textArtifact("ignore-policy", ".gitignore", 0o644, content))
	}
	requiresWrapper := profile.Wrapper
	for _, kind := range selection.Hooks {
		platform, ok := hooks.PlatformForKind(kind)
		if !ok {
			return nil, fmt.Errorf("unsupported hook kind %q", kind)
		}
		if platform.Activation.RequiresWrapper {
			requiresWrapper = true
		}
		artifact, err := hooks.Generate(kind)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, textArtifact("hook:"+kind, artifact.TargetPath, modeFor(artifact.Executable), artifact.Content))
		if kind == hooks.KindCodex {
			content, err := renderCodexActivation(root)
			if err != nil {
				return nil, err
			}
			artifacts = append(artifacts, textArtifact("hook-activation:codex", ".codex/config.toml", 0o644, content))
		}
	}
	if requiresWrapper && !trustedExistingWrapper(root, selection.TrustExistingWrapper) {
		wrapper := hooks.GenerateWrapper()
		artifacts = append(artifacts, textArtifact("hook-wrapper", wrapper.TargetPath, 0o755, wrapper.Content))
	}
	if selection.Binary != nil {
		name, err := StableBinaryName(selection.Binary.OS, selection.Binary.Arch)
		if err != nil {
			return nil, err
		}
		artifacts = append(artifacts, desiredArtifact{
			component: "binary", path: filepath.ToSlash(filepath.Join("tools", "reconc", "dist", name)),
			mode: 0o755, sourcePath: selection.Binary.SourcePath,
		})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].path < artifacts[j].path })
	for index := 1; index < len(artifacts); index++ {
		if artifacts[index-1].path == artifacts[index].path {
			return nil, fmt.Errorf("bootstrap components collide at %s", artifacts[index].path)
		}
	}
	return artifacts, nil
}

func renderCodexActivation(root string) (string, error) {
	const relative = ".codex/config.toml"
	path := filepath.Join(root, filepath.FromSlash(relative))
	existing := ""
	info, err := os.Lstat(path)
	if err == nil && info.Mode().IsRegular() {
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read %s for Codex hook activation: %w", relative, readErr)
		}
		existing = string(data)
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect %s for Codex hook activation: %w", relative, err)
	}

	content, err := hooks.RenderCodexActivation(existing, true)
	if err != nil {
		return "", fmt.Errorf("%s: %w", relative, err)
	}
	return content, nil
}

func enableCodexHooks(existing string) (string, error) {
	return hooks.RenderCodexActivation(existing, true)
}

func renderManagedFile(root, relative, start, end, block string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return managedBlock(start, end, block), nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect %s for managed bootstrap block: %w", relative, err)
	}
	if !info.Mode().IsRegular() {
		return managedBlock(start, end, block), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s for managed bootstrap block: %w", relative, err)
	}
	existing := string(data)
	if strings.Count(existing, start) > 1 || strings.Count(existing, end) > 1 {
		return "", fmt.Errorf("%s has duplicate reconc bootstrap managed block markers", relative)
	}
	startIndex := strings.Index(existing, start)
	endIndex := strings.Index(existing, end)
	if startIndex == -1 && endIndex == -1 {
		separator := ""
		if existing != "" && !strings.HasSuffix(existing, "\n") {
			separator = "\n"
		}
		if existing != "" {
			separator += "\n"
		}
		return existing + separator + managedBlock(start, end, block), nil
	}
	if startIndex == -1 || endIndex == -1 || endIndex < startIndex {
		return "", fmt.Errorf("%s has an incomplete reconc bootstrap managed block", relative)
	}
	endIndex += len(end)
	if endIndex < len(existing) && existing[endIndex] == '\n' {
		endIndex++
	}
	return existing[:startIndex] + managedBlock(start, end, block) + existing[endIndex:], nil
}

func renderManagedDocument(root, relative, title, start, end, block string) (string, error) {
	path := filepath.Join(root, filepath.FromSlash(relative))
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return "# " + title + "\n\n" + managedBlock(start, end, block), nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect %s for managed bootstrap document: %w", relative, err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return "# " + title + "\n\n" + managedBlock(start, end, block), nil
	}
	return renderManagedFile(root, relative, start, end, block)
}

func managedBlock(start, end, block string) string {
	return start + "\n" + strings.TrimSpace(block) + "\n" + end + "\n"
}

func renderPolicy(packs []string, tasks bool) string {
	var builder strings.Builder
	builder.WriteString("# Generated by `reconc bootstrap plan`.\n")
	builder.WriteString("# Review pack manifests with `reconc preset show NAME`.\n")
	builder.WriteString("extends:\n")
	for _, pack := range packs {
		builder.WriteString("  - ")
		builder.WriteString(pack)
		builder.WriteByte('\n')
	}
	if tasks {
		builder.WriteString("task_lifecycle:\n")
		builder.WriteString("  profile: sections-v1\n")
		builder.WriteString("  overview_path: docs/tasks.md\n")
		builder.WriteString("  detail_dir: docs/tasks\n")
		builder.WriteString("  done_dir: docs/tasks/done\n")
		builder.WriteString("  done_visible: 10\n")
		builder.WriteString("  completion:\n")
		builder.WriteString("    require_committed: true\n")
	}
	builder.WriteString("rules: []\n")
	return builder.String()
}

func renderAgentBlock() string {
	return strings.Join([]string{
		"## Reconc repository control",
		"",
		"This repository compiles policy from `.reconc.yml` and agent instructions into",
		"`.reconc/policy.lock.json`. Inspection commands are read-only and never refresh",
		"policy implicitly. After policy edits, run `reconc refresh .` explicitly.",
		"Use `reconc session-briefing . --json` for one versioned, machine-readable TASK,",
		"policy, and repository-run delta. Fetch only needed reference sections with",
		"`reconc agent-intro --section NAME` instead of loading the full guide.",
		"",
		"Before implementation, read `docs/tasks.md` and its active detail when those",
		"files exist. Before claiming completion, run the repository's real tests and",
		"`reconc done .`. Existing files remain user-owned: never overwrite them during",
		"bootstrap; resolve `.reconc-candidate-*` files explicitly.",
	}, "\n")
}

func renderTasksOverview() string {
	return `# TASK Control Plane

## Active

- [~] 001 Bootstrap Reconc -> tasks/001-bootstrap-reconc.md

## Queue

## Blocked

## Done
`
}

func renderBootstrapTask() string {
	return strings.Join([]string{
		"# TASK 001: Bootstrap Reconc", "", "## Why", "",
		"The repository needs a verified, project-specific Reconc rollout before normal",
		"implementation begins.", "", "## Acceptance", "",
		"- Selected policy packs and hook platforms match real repository intent.",
		"- Bare `reconc` resolves to the exact bootstrap build and `reconc bootstrap verify` passes against the reviewed plan.",
		"- Repository build and test commands pass with truthful evidence.",
		"- Bootstrap candidates are integrated or explicitly rejected without overwriting user files.",
		"", "## Sub-Tasks", "",
		"- [~] Review the bootstrap plan, selected packs, hooks, and detected stack evidence.",
		"- [ ] Resolve any non-destructive candidate files.",
		"- [ ] Run bootstrap verification and repository-native tests.",
		"- [ ] Record the resulting workflow in docs and finish the TASK lifecycle.",
		"", "## Notes", "", "## Deviations", "", "None.", "",
	}, "\n")
}

func renderDocumentationBlock() string {
	return strings.Join([]string{
		"## Reconc workflow", "",
		"Reconc policy lives in `.reconc.yml`; the deterministic compiled contract lives",
		"in `.reconc/policy.lock.json`. `reconc bootstrap inspect` and `plan` are",
		"read-only unless an explicit plan output path is requested. `apply` is",
		"transactional and create-only for repository targets. Existing drift produces",
		"hash-addressed candidate files for review. Mutating bootstrap installs the exact",
		"running build as bare `reconc`; `verify` checks it again read-only.", "",
		"The detailed AI-operated rollout and manual recovery checklist remains in the",
		"Reconc distribution at `harness/template/BOOTSTRAP.md`.",
	}, "\n")
}

func renderStart() string {
	return strings.Join([]string{
		"# START", "",
		"Read `AGENTS.md`, then `docs/tasks.md` and its active TASK detail. After those",
		"reads, run `reconc session-briefing . --json` once for the versioned TASK,",
		"policy, and repository-run delta. Onboarding is read-only: do not bootstrap,",
		"refresh, install hooks, or",
		"transition TASK state until the user authorizes implementation.", "",
	}, "\n")
}

func textArtifact(component, path string, mode uint32, content string) desiredArtifact {
	return desiredArtifact{component: component, path: path, mode: mode, content: []byte(content)}
}

func modeFor(executable bool) uint32 {
	if executable {
		return 0o755
	}
	return 0o644
}

func trustedExistingWrapper(root string, trust bool) bool {
	if !trust {
		return false
	}
	return execfile.Is(filepath.Join(root, filepath.FromSlash(hooks.WrapperPath)))
}
