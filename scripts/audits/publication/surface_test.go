package main

import (
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode"

	"reconc.dev/reconc/internal/commandmeta"
	"reconc.dev/reconc/internal/hooks"
	"reconc.dev/reconc/internal/policy"

	"gopkg.in/yaml.v3"
)

var markdownLinkPattern = regexp.MustCompile(`!?\[[^]\n]*\]\(([^)\s]+)(?:\s+"[^"]*")?\)`)
var htmlImagePattern = regexp.MustCompile(`<img[^>]+src="([^"]+)"`)

func TestPublicREADMEListsEveryShippedAssurancePack(t *testing.T) {
	root := publicSurfaceRoot(t)
	readme := readPublicSurfaceFile(t, root, "README.md")
	entries, err := os.ReadDir(filepath.Join(root, "internal", "presets", "packs"))
	if err != nil {
		t.Fatal(err)
	}
	packCount := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, "-assurance.yml") {
			continue
		}
		packCount++
		pack := strings.TrimSuffix(name, ".yml")
		if !strings.Contains(readme, "`"+pack+"`") {
			t.Errorf("README.md omits shipped assurance pack %q", pack)
		}
	}
	if packCount == 0 {
		t.Fatal("no shipped assurance packs found")
	}
}

func TestPublicMarkdownLocalLinksAndAnchorsResolve(t *testing.T) {
	root := publicSurfaceRoot(t)
	for _, source := range trackedMarkdownFiles(t, root) {
		body := readPublicSurfaceFile(t, root, source)
		links := markdownLinkPattern.FindAllStringSubmatch(body, -1)
		links = append(links, htmlImagePattern.FindAllStringSubmatch(body, -1)...)
		for _, match := range links {
			if len(match) < 2 || isRemoteLink(match[1]) {
				continue
			}
			assertLocalLink(t, root, source, match[1])
		}
	}
}

func trackedMarkdownFiles(t *testing.T, root string) []string {
	t.Helper()
	command := exec.Command("git", "ls-files", "-z", "--", "*.md")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		t.Fatalf("list tracked Markdown files: %v", err)
	}
	trimmed := strings.TrimSuffix(string(output), "\x00")
	if trimmed == "" {
		t.Fatal("repository has no tracked Markdown files")
	}
	return strings.Split(trimmed, "\x00")
}

func TestCanonicalDailyLoopMatchesEveryTeachingSurface(t *testing.T) {
	root := publicSurfaceRoot(t)
	tokens := []string{"reconc session-briefing . --json", "reconc check .", "reconc next .", "reconc done ."}
	for _, path := range []string{"README.md", "docs/documentation.md", "docs/commands.md", "skills/reconc/SKILL.md", "internal/agentguide/guide.md"} {
		assertOrderedTokens(t, path, readPublicSurfaceFile(t, root, path), tokens)
	}
}

func TestCurrentDocumentationListsCanonicalCommandSurface(t *testing.T) {
	root := publicSurfaceRoot(t)
	documentation := readPublicSurfaceFile(t, root, "docs/documentation.md")
	commandSurface := markdownSection(t, "docs/documentation.md", documentation, "Command Surface")
	commandReference := readPublicSurfaceFile(t, root, "docs/commands.md")
	for _, command := range commandmeta.All() {
		if !strings.Contains(commandSurface, "`"+command.Name+"`") {
			t.Errorf("docs/documentation.md command surface omits canonical command %q", command.Name)
		}
		if !strings.Contains(commandReference, "reconc "+command.Name) {
			t.Errorf("docs/commands.md omits canonical command %q", command.Name)
		}
	}
}

func TestCommandReferenceUsesCanonicalCanGrammar(t *testing.T) {
	root := publicSurfaceRoot(t)
	commandReference := readPublicSurfaceFile(t, root, "docs/commands.md")
	for _, command := range commandmeta.All() {
		if command.Name != "can" {
			continue
		}
		if !strings.Contains(commandReference, "### `"+command.Synopsis+"`") {
			t.Errorf("docs/commands.md does not use canonical can synopsis %q", command.Synopsis)
		}
		return
	}
	t.Fatal("canonical command metadata omits can")
}

func TestArchitectureListsEveryTopLevelInternalPackage(t *testing.T) {
	root := publicSurfaceRoot(t)
	architecture := readPublicSurfaceFile(t, root, "docs/architecture.md")
	entries, err := os.ReadDir(filepath.Join(root, "internal"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(entry.Name()) + `/`).MatchString(architecture) {
			t.Errorf("docs/architecture.md package map omits internal/%s", entry.Name())
		}
	}
}

func TestFrozenRuleKindRFCCoversImplementedKinds(t *testing.T) {
	root := publicSurfaceRoot(t)
	rfc := readPublicSurfaceFile(t, root, "docs/rfcs/RECONC-0004-rule-kinds.md")
	for _, kind := range policy.AllKinds() {
		if !strings.Contains(rfc, "`"+string(kind)+"`") {
			t.Errorf("RFC 0004 omits implemented rule kind %q", kind)
		}
	}
	for _, kind := range policy.AllAssuranceKinds() {
		if !strings.Contains(rfc, "`"+string(kind)+"`") {
			t.Errorf("RFC 0004 omits implemented assurance gate %q", kind)
		}
	}
}

func TestAgentGuidanceCoversRegisteredPlatformsAndPortableProof(t *testing.T) {
	root := publicSurfaceRoot(t)
	for _, path := range []string{"skills/reconc/SKILL.md", "internal/agentguide/guide.md"} {
		guidance := readPublicSurfaceFile(t, root, path)
		for _, platform := range hooks.AgentPlatforms() {
			if !strings.Contains(guidance, platform.DisplayName) {
				t.Errorf("%s omits registered platform %s", path, platform.DisplayName)
			}
		}
		if !strings.Contains(guidance, "reconc proof .") {
			t.Errorf("%s omits the portable proof command", path)
		}
	}
}

func TestCommandReferenceCoversPublicHostEnvironment(t *testing.T) {
	root := publicSurfaceRoot(t)
	commandReference := readPublicSurfaceFile(t, root, "docs/commands.md")
	for _, variable := range []string{
		"CLAUDE_CONFIG_DIR",
		"COLUMNS",
		"GITHUB_ACTIONS",
		"GROK_HOME",
		"GROK_LEADER_SOCKET",
		"KIMI_CODE_HOME",
		"PI_CODING_AGENT_DIR",
		"SOURCE_DATE_EPOCH",
		"XAI_API_KEY",
	} {
		if !strings.Contains(commandReference, "`"+variable+"`") {
			t.Errorf("docs/commands.md omits public host environment variable %s", variable)
		}
	}
}

func TestHostIntegrationDocumentationCoversStructuredContract(t *testing.T) {
	root := publicSurfaceRoot(t)
	readme := readPublicSurfaceFile(t, root, "README.md")
	documentation := readPublicSurfaceFile(t, root, "docs/documentation.md")
	commands := readPublicSurfaceFile(t, root, "docs/commands.md")
	rfc := readPublicSurfaceFile(t, root, "docs/rfcs/RECONC-0006-hooks-and-agent-sessions.md")
	skill := readPublicSurfaceFile(t, root, "skills/reconc/SKILL.md")

	if !strings.Contains(readme, "docs/documentation.md#host-integration-truth") {
		t.Error("README.md does not link to the authoritative host-integration contract")
	}
	for _, state := range []string{
		"`configured`",
		"`discoverable`",
		"`loaded`",
		"`observed`",
		"`enforced`",
		"`inferred`",
		"`degraded`",
		"`unsupported`",
	} {
		if !strings.Contains(documentation, state) {
			t.Errorf("host-integration documentation omits support state %s", state)
		}
	}
	for _, surface := range []string{
		"Cursor desktop Agent",
		"Cursor desktop Cmd+K",
		"Cursor inline Tab",
		"Cursor CLI interactive",
		"Cursor CLI print mode",
		"Cursor cloud agents",
		"OpenCode CLI",
		"Kilo Code CLI",
		"Kilo Code VS Code host",
		"Kimi Code CLI",
		"Oh My Pi CLI",
		"Pi Coding Agent",
		"ZCode",
	} {
		if !strings.Contains(documentation, surface) {
			t.Errorf("host-integration documentation omits surface %q", surface)
		}
	}
	for _, disposition := range hooks.CursorEventDispositions() {
		if !strings.Contains(documentation, "`"+disposition.NativeEvent+"`") {
			t.Errorf("host-integration documentation omits Cursor event %q", disposition.NativeEvent)
		}
	}
	for token, surfaces := range map[string][]string{
		"output.metadata.exit":                    {documentation, rfc, skill},
		"client.session.promptAsync":              {documentation, rfc},
		"messageID":                               {documentation, rfc},
		"server_fingerprint":                      {documentation, rfc},
		"reconc why mcp":                          {documentation, commands, skill},
		"afterShellExecution":                     {documentation, rfc, skill},
		"postToolUseFailure":                      {documentation, rfc, skill},
		"surface_events":                          {readme, documentation, commands, skill},
		"workspaceOpen":                           {readme, documentation, rfc, skill},
		"AskQuestion":                             {readme, documentation, rfc, skill},
		"scripts/tests/host-integration-probe.sh": {documentation},
	} {
		for index, body := range surfaces {
			if !strings.Contains(body, token) {
				t.Errorf("host contract surface %d omits semantic token %q", index, token)
			}
		}
	}
}

func TestBootstrapUsesDeclaredLatestPublishedReleaseTag(t *testing.T) {
	root := publicSurfaceRoot(t)
	readme := readPublicSurfaceFile(t, root, "README.md")
	versionSource := readPublicSurfaceFile(t, root, "cmd/reconc/main.go")
	versionPattern := regexp.MustCompile(`(?m)^var Version = "([0-9]+\.[0-9]+\.[0-9]+)"$`)
	versionMatch := versionPattern.FindStringSubmatch(versionSource)
	if len(versionMatch) != 2 {
		t.Fatal("cmd/reconc/main.go does not expose one stable source version")
	}
	publishedPattern := regexp.MustCompile("latest published release is the immutable `?(reconc-v[0-9]+\\.[0-9]+\\.[0-9]+)`? tag")
	publishedMatch := publishedPattern.FindStringSubmatch(readme)
	if len(publishedMatch) != 2 {
		t.Fatal("README.md does not declare one latest published release tag")
	}
	expectedRef := publishedMatch[1]
	command := exec.Command("git", "rev-parse", "--verify", "refs/tags/"+expectedRef+"^{commit}")
	command.Dir = root
	if err := command.Run(); err != nil {
		t.Fatalf("declared latest published tag %q does not exist: %v", expectedRef, err)
	}
	command = exec.Command("git", "show", expectedRef+":cmd/reconc/main.go")
	command.Dir = root
	taggedVersionSource, err := command.Output()
	if err != nil {
		t.Fatalf("read source version from %s: %v", expectedRef, err)
	}
	taggedVersionMatch := versionPattern.FindStringSubmatch(string(taggedVersionSource))
	if len(taggedVersionMatch) != 2 || expectedRef != "reconc-v"+taggedVersionMatch[1] {
		t.Fatalf("declared latest published tag %q does not match its source version", expectedRef)
	}
	if compareSemanticCore(versionMatch[1], taggedVersionMatch[1]) < 0 {
		t.Fatalf("source version %s precedes latest published version %s", versionMatch[1], taggedVersionMatch[1])
	}
	for _, installer := range []string{"install.sh", "install.ps1"} {
		pattern := regexp.MustCompile(`https://raw\.githubusercontent\.com/Christopher-Schulze/reconc/([^/]+)/` + regexp.QuoteMeta(installer))
		matches := pattern.FindAllStringSubmatch(readme, -1)
		if len(matches) == 0 {
			t.Errorf("README.md omits the %s installer URL", installer)
			continue
		}
		for _, match := range matches {
			if match[1] != expectedRef {
				t.Errorf("README.md %s source = %q, want declared latest published tag %q", installer, match[1], expectedRef)
			}
		}
	}
}

func compareSemanticCore(left string, right string) int {
	leftParts := strings.Split(left, ".")
	rightParts := strings.Split(right, ".")
	for index := range leftParts {
		if len(leftParts[index]) != len(rightParts[index]) {
			if len(leftParts[index]) < len(rightParts[index]) {
				return -1
			}
			return 1
		}
		if leftParts[index] < rightParts[index] {
			return -1
		}
		if leftParts[index] > rightParts[index] {
			return 1
		}
	}
	return 0
}

func TestPublicBrandImageHasExactDimensionsAndBoundedSize(t *testing.T) {
	root := publicSurfaceRoot(t)
	assertPNGAsset(t, filepath.Join(root, "assets/reconc.png"), 1774, 887, 1_000_000)
}

func TestGitHubCommunitySurfaceIsSubstantive(t *testing.T) {
	root := publicSurfaceRoot(t)
	for path, required := range map[string][]string{
		"CONTRIBUTING.md":                  {"make test", "make vet", "make lint", "make self-host", "make publication-audit", "SECURITY.md"},
		".github/pull_request_template.md": {"- [ ]", "make test", "make publication-audit"},
	} {
		body := readPublicSurfaceFile(t, root, path)
		for _, token := range required {
			if !strings.Contains(body, token) {
				t.Errorf("%s omits required contributor contract %q", path, token)
			}
		}
	}

	for _, path := range []string{
		".github/ISSUE_TEMPLATE/bug.yml",
		".github/ISSUE_TEMPLATE/feature.yml",
	} {
		var form issueForm
		if err := yaml.Unmarshal([]byte(readPublicSurfaceFile(t, root, path)), &form); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if strings.TrimSpace(form.Name) == "" || strings.TrimSpace(form.Description) == "" {
			t.Errorf("%s must identify the form", path)
		}
		ids := map[string]bool{}
		requiredInputs := 0
		for _, element := range form.Body {
			if element.Type == "markdown" {
				continue
			}
			if element.ID == "" || ids[element.ID] {
				t.Errorf("%s has missing or duplicate form id %q", path, element.ID)
			}
			ids[element.ID] = true
			if element.Validations.Required {
				requiredInputs++
			}
		}
		if len(ids) < 4 || requiredInputs < 3 {
			t.Errorf("%s does not collect enough structured required evidence", path)
		}
	}

	var config issueTemplateConfig
	configPath := ".github/ISSUE_TEMPLATE/config.yml"
	configBody := readPublicSurfaceFile(t, root, configPath)
	if err := yaml.Unmarshal([]byte(configBody), &config); err != nil {
		t.Fatalf("parse %s: %v", configPath, err)
	}
	var rawConfig map[string]any
	if err := yaml.Unmarshal([]byte(configBody), &rawConfig); err != nil {
		t.Fatalf("parse raw %s: %v", configPath, err)
	}
	blankIssues, hasBlankIssues := rawConfig["blank_issues_enabled"].(bool)
	if !hasBlankIssues || blankIssues || config.BlankIssuesEnabled || len(config.ContactLinks) < 2 {
		t.Errorf("%s must disable blank issues and expose private security plus documentation routes", configPath)
	}
	for _, link := range config.ContactLinks {
		if !strings.HasPrefix(link.URL, "https://github.com/Christopher-Schulze/reconc/") {
			t.Errorf("%s contact link leaves the canonical repository: %s", configPath, link.URL)
		}
	}
}

func TestCodeQLWorkflowHasBoundedAdvancedSetup(t *testing.T) {
	root := publicSurfaceRoot(t)
	path := ".github/workflows/codeql.yml"
	body := readPublicSurfaceFile(t, root, path)
	var workflow githubWorkflow
	if err := yaml.Unmarshal([]byte(body), &workflow); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("parse raw %s: %v", path, err)
	}
	triggerMap, ok := raw["on"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no structured trigger map", path)
	}
	for _, trigger := range []string{"push", "pull_request", "schedule", "workflow_dispatch"} {
		if _, ok := triggerMap[trigger]; !ok {
			t.Errorf("%s omits %s trigger", path, trigger)
		}
	}
	if len(triggerMap) != 4 {
		t.Errorf("%s contains unexpected triggers: %v", path, triggerMap)
	}
	if len(workflow.On.Push.Branches) != 0 || !equalStrings(workflow.On.PullRequest.Branches, []string{"main"}) {
		t.Errorf("%s must scan every pushed candidate and pull requests against main", path)
	}
	if len(workflow.On.Schedule) != 1 || len(strings.Fields(workflow.On.Schedule[0].Cron)) != 5 {
		t.Errorf("%s must contain one bounded cron schedule", path)
	}
	if len(workflow.Permissions) != 2 || workflow.Permissions["contents"] != "read" || workflow.Permissions["security-events"] != "write" {
		t.Errorf("%s permissions must be exactly contents:read and security-events:write", path)
	}
	job, ok := workflow.Jobs["analyze"]
	if !ok || len(workflow.Jobs) != 1 {
		t.Fatalf("%s must contain one analyze job", path)
	}
	if job.RunsOn != "ubuntu-24.04" || job.TimeoutMinutes <= 0 || job.TimeoutMinutes > 30 {
		t.Errorf("%s analyze job has an unbounded or unexpected runner contract", path)
	}
	trustedAction := regexp.MustCompile(`^(actions/checkout|actions/setup-go|github/codeql-action/(init|analyze))@[0-9a-f]{40}$`)
	actions := map[string]int{}
	var build string
	for _, step := range job.Steps {
		if step.Uses != "" {
			if !trustedAction.MatchString(step.Uses) {
				t.Errorf("%s uses an untrusted or unpinned action: %s", path, step.Uses)
			}
			actions[strings.Split(step.Uses, "@")[0]]++
		}
		if strings.Contains(step.Run, "go build") {
			build += step.Run
		}
		if strings.HasPrefix(step.Uses, "github/codeql-action/init@") {
			if step.With["languages"] != "go" || step.With["build-mode"] != "manual" {
				t.Errorf("%s CodeQL init must use manual Go analysis", path)
			}
		}
	}
	for action, count := range map[string]int{
		"actions/checkout":             1,
		"actions/setup-go":             1,
		"github/codeql-action/init":    1,
		"github/codeql-action/analyze": 1,
	} {
		if actions[action] != count {
			t.Errorf("%s action %s count = %d, want %d", path, action, actions[action], count)
		}
	}
	for _, command := range []string{"go build ./...", "(cd harness/template && go build ./...)"} {
		if !strings.Contains(build, command) {
			t.Errorf("%s manual build omits %q", path, command)
		}
	}
	documentation := readPublicSurfaceFile(t, root, "docs/documentation.md")
	for _, token := range []string{path, "CodeQL", "harness/template", "security-events: write"} {
		if !strings.Contains(documentation, token) {
			t.Errorf("docs/documentation.md omits CodeQL contract %q", token)
		}
	}
}

func TestCIWorkflowRunsOnCandidateRefs(t *testing.T) {
	root := publicSurfaceRoot(t)
	path := ".github/workflows/reconc-ci.yml"
	body := readPublicSurfaceFile(t, root, path)
	var workflow githubWorkflow
	if err := yaml.Unmarshal([]byte(body), &workflow); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(body), &raw); err != nil {
		t.Fatalf("parse raw %s: %v", path, err)
	}
	triggerMap, ok := raw["on"].(map[string]any)
	if !ok {
		t.Fatalf("%s has no structured trigger map", path)
	}
	if _, ok := triggerMap["push"]; !ok {
		t.Fatalf("%s omits push trigger", path)
	}
	if len(workflow.On.Push.Branches) != 0 {
		t.Errorf("%s must run for pushed candidate refs before protected main advances", path)
	}
}

func TestDependabotCoversBoundedDependencySurfaces(t *testing.T) {
	root := publicSurfaceRoot(t)
	path := ".github/dependabot.yml"
	var config dependabotConfig
	if err := yaml.Unmarshal([]byte(readPublicSurfaceFile(t, root, path)), &config); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if config.Version != 2 || len(config.Updates) != 3 {
		t.Fatalf("%s must define version 2 with exactly three update surfaces", path)
	}
	want := map[string]bool{
		"github-actions@/":        true,
		"gomod@/":                 true,
		"gomod@/harness/template": true,
	}
	seen := map[string]bool{}
	for _, update := range config.Updates {
		key := update.PackageEcosystem + "@" + update.Directory
		if !want[key] || seen[key] {
			t.Errorf("%s has unexpected or duplicate update surface %s", path, key)
		}
		seen[key] = true
		if update.Schedule.Interval != "weekly" || update.Schedule.Day == "" || update.Schedule.Time == "" || update.Schedule.Timezone != "Europe/Berlin" {
			t.Errorf("%s surface %s lacks the bounded weekly schedule", path, key)
		}
		if update.OpenPullRequestsLimit != 0 || update.TargetBranch != "" {
			t.Errorf("%s surface %s must disable routine version PRs and use the default branch", path, key)
		}
		appliesTo := map[string]bool{}
		for _, group := range update.Groups {
			if !equalStrings(group.Patterns, []string{"*"}) {
				t.Errorf("%s surface %s has an incomplete dependency group", path, key)
			}
			appliesTo[group.AppliesTo] = true
		}
		if len(update.Groups) != 1 || !appliesTo["security-updates"] || len(appliesTo) != 1 {
			t.Errorf("%s surface %s must group security updates without enabling routine version PRs", path, key)
		}
	}
	if len(seen) != len(want) {
		t.Errorf("%s does not cover every required dependency surface", path)
	}
	documentation := readPublicSurfaceFile(t, root, "docs/documentation.md")
	for _, token := range []string{path, "security-update", "version-update", "auto-merge"} {
		if !strings.Contains(documentation, token) {
			t.Errorf("docs/documentation.md omits Dependabot contract %q", token)
		}
	}
}

func TestNativeWindowsInstallerIsWiredIntoCIAndRelease(t *testing.T) {
	root := publicSurfaceRoot(t)
	for _, path := range []string{
		"install.ps1",
		"scripts/tests/test-windows-installer.ps1",
	} {
		assertBoundedFile(t, filepath.Join(root, filepath.FromSlash(path)), 64*1024)
	}

	ci := readPublicSurfaceFile(t, root, ".github/workflows/reconc-ci.yml")
	for _, token := range []string{
		"shell: pwsh",
		"make release-one TARGET=windows/amd64",
		`artifact="dist/reconc-$version-windows-amd64.exe"`,
		"./scripts/tests/test-windows-installer.ps1",
		"-InstallerPath ./install.ps1",
		`-BinaryPath "./dist/reconc-$version-windows-amd64.exe"`,
		"live_release:",
		"RECONC_LIVE_RELEASE",
		"-LiveRelease",
	} {
		if !strings.Contains(ci, token) {
			t.Errorf("native Windows CI omits installer contract %q", token)
		}
	}

	// The build, the verifier, and the release trust fixture all read the same
	// copied-asset manifest, so publishing both installers is stated once.
	makefile := readPublicSurfaceFile(t, root, "Makefile")
	if !strings.Contains(makefile, "./scripts/release/copy-assets.sh $(DISTDIR)") {
		t.Error("release build no longer copies its artifacts through the shared manifest")
	}
	copied := readPublicSurfaceFile(t, root, "scripts/release/copied-assets.tsv")
	for _, installer := range []string{"install.sh\tinstall.sh", "install.ps1\tinstall.ps1"} {
		if !strings.Contains(copied, installer) {
			t.Errorf("release build does not publish %q", strings.ReplaceAll(installer, "\t", " -> "))
		}
	}
}

type issueForm struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	Body        []struct {
		Type        string `yaml:"type"`
		ID          string `yaml:"id"`
		Validations struct {
			Required bool `yaml:"required"`
		} `yaml:"validations"`
	} `yaml:"body"`
}

type issueTemplateConfig struct {
	BlankIssuesEnabled bool `yaml:"blank_issues_enabled"`
	ContactLinks       []struct {
		URL string `yaml:"url"`
	} `yaml:"contact_links"`
}

type githubWorkflow struct {
	On struct {
		Push struct {
			Branches []string `yaml:"branches"`
		} `yaml:"push"`
		PullRequest struct {
			Branches []string `yaml:"branches"`
		} `yaml:"pull_request"`
		Schedule []struct {
			Cron string `yaml:"cron"`
		} `yaml:"schedule"`
	} `yaml:"on"`
	Permissions map[string]string `yaml:"permissions"`
	Jobs        map[string]struct {
		RunsOn         string `yaml:"runs-on"`
		TimeoutMinutes int    `yaml:"timeout-minutes"`
		Steps          []struct {
			Uses string            `yaml:"uses"`
			Run  string            `yaml:"run"`
			With map[string]string `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

type dependabotConfig struct {
	Version int `yaml:"version"`
	Updates []struct {
		PackageEcosystem      string `yaml:"package-ecosystem"`
		Directory             string `yaml:"directory"`
		TargetBranch          string `yaml:"target-branch"`
		OpenPullRequestsLimit int    `yaml:"open-pull-requests-limit"`
		Schedule              struct {
			Interval string `yaml:"interval"`
			Day      string `yaml:"day"`
			Time     string `yaml:"time"`
			Timezone string `yaml:"timezone"`
		} `yaml:"schedule"`
		Groups map[string]struct {
			AppliesTo string   `yaml:"applies-to"`
			Patterns  []string `yaml:"patterns"`
		} `yaml:"groups"`
	} `yaml:"updates"`
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func publicSurfaceRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	return root
}

func readPublicSurfaceFile(t *testing.T, root, relative string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(body)
}

func assertPNGAsset(t *testing.T, path string, width, height int, maxBytes int64) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	config, err := png.DecodeConfig(file)
	closeErr := file.Close()
	if err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
	if closeErr != nil {
		t.Fatalf("close %s: %v", path, closeErr)
	}
	if config.Width != width || config.Height != height {
		t.Errorf("%s dimensions = %dx%d, want %dx%d", path, config.Width, config.Height, width, height)
	}
	assertBoundedFile(t, path, maxBytes)
}

func assertBoundedFile(t *testing.T, path string, maxBytes int64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() <= 0 || info.Size() > maxBytes {
		t.Errorf("%s size = %d bytes, expected 1..%d", path, info.Size(), maxBytes)
	}
}

func isRemoteLink(target string) bool {
	return strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "mailto:")
}

func assertLocalLink(t *testing.T, root, source, target string) {
	t.Helper()
	pathPart, anchor, _ := strings.Cut(target, "#")
	path := filepath.Join(root, filepath.Dir(source), filepath.FromSlash(pathPart))
	if pathPart == "" {
		path = filepath.Join(root, source)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("%s link %q does not resolve: %v", source, target, err)
		return
	}
	if anchor != "" && !markdownAnchors(string(body))[anchor] {
		t.Errorf("%s link %q has no matching heading", source, target)
	}
}

func markdownAnchors(body string) map[string]bool {
	anchors := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "#") {
			continue
		}
		heading := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		anchors[markdownSlug(heading)] = true
	}
	return anchors
}

func markdownSlug(value string) string {
	var clean strings.Builder
	for _, char := range strings.ToLower(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == ' ' || char == '-' {
			clean.WriteRune(char)
		}
	}
	return strings.Join(strings.Fields(clean.String()), "-")
}

func assertOrderedTokens(t *testing.T, path, body string, tokens []string) {
	t.Helper()
	position := 0
	for _, token := range tokens {
		next := strings.Index(body[position:], token)
		if next < 0 {
			t.Fatalf("%s omits or reorders daily-loop command %q", path, token)
		}
		position += next + len(token)
	}
}

func markdownSection(t *testing.T, path, body, heading string) string {
	t.Helper()
	startToken := "## " + heading
	start := strings.Index(body, startToken)
	if start < 0 {
		t.Fatalf("%s omits section %q", path, heading)
	}
	section := body[start+len(startToken):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}
	return section
}
