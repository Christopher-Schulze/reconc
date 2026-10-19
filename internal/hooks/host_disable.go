package hooks

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"reconc.dev/reconc/internal/boundedio"
)

// hostDisableSwitch is a host setting that keeps an installed, generator-exact
// Reconc artifact from running: Claude Code and GitHub Copilot
// `disableAllHooks`, Claude Code managed `allowManagedHooksOnly`, and an OMP
// `disabledExtensions` entry for the Reconc extension.
type hostDisableSwitch struct {
	path   string
	detail string
}

const maxHostSettingsBytes = 1 << 20

// maxManagedSettingsDropIns bounds the managed-settings.d listing.
const maxManagedSettingsDropIns = 4096

// ompReconcExtensionID is the id OMP derives from .omp/extensions/reconc.ts.
const ompReconcExtensionID = "extension-module:reconc"

// claudeManagedSettingsDir is the file-based managed policy directory; status
// tests point it at a fixture directory.
var claudeManagedSettingsDir = defaultClaudeManagedSettingsDir

func defaultClaudeManagedSettingsDir() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Library/Application Support/ClaudeCode"
	case "windows":
		programFiles := os.Getenv("ProgramFiles")
		if programFiles == "" {
			programFiles = `C:\Program Files`
		}
		return filepath.Join(programFiles, "ClaudeCode")
	default:
		return "/etc/claude-code"
	}
}

// inspectHostDisableSwitch returns the host setting that disables an
// installed Reconc artifact, or nil when no inspected setting does.
func inspectHostDisableSwitch(root, kind string) (*hostDisableSwitch, error) {
	switch kind {
	case KindClaudeCode:
		return claudeHooksDisableSwitch(root)
	case KindGitHubCopilot:
		return copilotHooksDisableSwitch(root)
	case KindOMP:
		return ompExtensionDisableSwitch(root)
	default:
		return nil, nil
	}
}

// claudeHooksDisableSwitch follows Claude Code settings precedence: managed
// file policy (managed-settings.json, then managed-settings.d/*.json in
// order), project local, shared project, then user settings. The first level
// that sets a boolean disableAllHooks decides; managed allowManagedHooksOnly
// blocks every project hook. MDM, server-managed and --settings sources are
// not readable here and stay unverified.
func claudeHooksDisableSwitch(root string) (*hostDisableSwitch, error) {
	managed, err := claudeManagedSettingsFiles(claudeManagedSettingsDir())
	if err != nil {
		return nil, err
	}
	var managedDisable *hostDisableSwitch
	managedSet := false
	for _, path := range managed {
		document, err := readHostSettingsJSON(path)
		if err != nil {
			return nil, err
		}
		if only, ok := document["allowManagedHooksOnly"].(bool); ok && only {
			return &hostDisableSwitch{path: path, detail: "managed allowManagedHooksOnly=true in " + path + " blocks project hooks"}, nil
		}
		if disabled, ok := document["disableAllHooks"].(bool); ok {
			managedSet = true
			managedDisable = nil
			if disabled {
				managedDisable = &hostDisableSwitch{path: path, detail: "managed disableAllHooks=true in " + path}
			}
		}
	}
	if managedSet {
		return managedDisable, nil
	}
	userPath, err := claudeUserSettingsPath()
	if err != nil {
		return nil, err
	}
	layers := []string{
		filepath.Join(root, ".claude", "settings.local.json"),
		filepath.Join(root, ".claude", "settings.json"),
		userPath,
	}
	return firstBooleanDisableAllHooks(layers)
}

// copilotHooksDisableSwitch follows Copilot CLI settings precedence: the
// repository local file, the repository file, then the user file. The cloud
// agent loads none of them.
func copilotHooksDisableSwitch(root string) (*hostDisableSwitch, error) {
	home := os.Getenv("COPILOT_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		home = filepath.Join(userHome, ".copilot")
	}
	return firstBooleanDisableAllHooks([]string{
		filepath.Join(root, ".github", "copilot", "settings.local.json"),
		filepath.Join(root, ".github", "copilot", "settings.json"),
		filepath.Join(home, "settings.json"),
	})
}

func firstBooleanDisableAllHooks(layers []string) (*hostDisableSwitch, error) {
	for _, path := range layers {
		document, err := readHostSettingsJSON(path)
		if err != nil {
			return nil, err
		}
		disabled, ok := document["disableAllHooks"].(bool)
		if !ok {
			continue
		}
		if !disabled {
			return nil, nil
		}
		return &hostDisableSwitch{path: path, detail: "disableAllHooks=true in " + path}, nil
	}
	return nil, nil
}

// ompExtensionDisableSwitch evaluates OMP's disabledExtensions. Settings
// layers replace arrays, so a project layer that sets the list decides over
// the user layer. The main session applies a change without a restart.
func ompExtensionDisableSwitch(root string) (*hostDisableSwitch, error) {
	projectSet := false
	for _, path := range []string{
		filepath.Join(root, ".omp", "config.yml"),
		filepath.Join(root, ".omp", "settings.json"),
	} {
		list, set, err := ompDisabledExtensions(path)
		if err != nil {
			return nil, err
		}
		if !set {
			continue
		}
		projectSet = true
		if slices.Contains(list, ompReconcExtensionID) {
			return &hostDisableSwitch{path: path, detail: "disabledExtensions lists " + ompReconcExtensionID + " in " + path}, nil
		}
	}
	if projectSet {
		return nil, nil
	}
	agentDir, err := ompAgentDir(root)
	if err != nil {
		return nil, err
	}
	for _, name := range []string{"config.yml", "config.yaml"} {
		path := filepath.Join(agentDir, name)
		list, set, err := ompDisabledExtensions(path)
		if err != nil {
			return nil, err
		}
		if !set {
			if _, statErr := os.Stat(path); statErr == nil {
				return nil, nil
			}
			continue
		}
		if slices.Contains(list, ompReconcExtensionID) {
			return &hostDisableSwitch{path: path, detail: "disabledExtensions lists " + ompReconcExtensionID + " in " + path}, nil
		}
		return nil, nil
	}
	return nil, nil
}

func ompDisabledExtensions(path string) ([]string, bool, error) {
	data, err := boundedio.ReadFile(path, maxHostSettingsBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var document map[string]interface{}
	if strings.HasSuffix(path, ".json") {
		err = json.Unmarshal(data, &document)
	} else {
		err = yaml.Unmarshal(data, &document)
	}
	if err != nil {
		// OMP reports and skips a settings file it cannot parse.
		return nil, false, nil
	}
	raw, ok := document["disabledExtensions"].([]interface{})
	if !ok {
		return nil, false, nil
	}
	list := make([]string, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok {
			list = append(list, strings.TrimSpace(text))
		}
	}
	return list, true, nil
}

// ompAgentDir resolves OMP's agent directory: a named profile (OMP_PROFILE,
// else PI_PROFILE) selects <config root>/profiles/<name>/agent; the default
// profile honors PI_CODING_AGENT_DIR, resolved against the session directory
// like OMP's path.resolve, else <config root>/agent. The config root is the
// home directory joined with PI_CONFIG_DIR or ".omp".
func ompAgentDir(root string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	configRoot := filepath.Join(home, ".omp")
	if name := os.Getenv("PI_CONFIG_DIR"); name != "" {
		configRoot = filepath.Join(home, name)
	}
	profile, ompSet := os.LookupEnv("OMP_PROFILE")
	if !ompSet {
		profile = os.Getenv("PI_PROFILE")
	}
	if profile = strings.TrimSpace(profile); profile != "" {
		return filepath.Join(configRoot, "profiles", profile, "agent"), nil
	}
	if configured := os.Getenv("PI_CODING_AGENT_DIR"); configured != "" {
		if !filepath.IsAbs(configured) {
			configured = filepath.Join(root, configured)
		}
		return filepath.Clean(configured), nil
	}
	return filepath.Join(configRoot, "agent"), nil
}

func claudeUserSettingsPath() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "settings.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "settings.json"), nil
}

func claudeManagedSettingsFiles(dir string) ([]string, error) {
	files := []string{filepath.Join(dir, "managed-settings.json")}
	entries, err := boundedio.ReadDir(filepath.Join(dir, "managed-settings.d"), maxManagedSettingsDropIns)
	if errors.Is(err, fs.ErrNotExist) {
		return files, nil
	}
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || !strings.HasSuffix(name, ".json") || entry.IsDir() {
			continue
		}
		files = append(files, filepath.Join(dir, "managed-settings.d", name))
	}
	return files, nil
}

// readHostSettingsJSON reads one host settings file. A missing file or a
// document the host would skip as invalid contributes no settings.
func readHostSettingsJSON(path string) (map[string]interface{}, error) {
	data, err := boundedio.ReadFile(path, maxHostSettingsBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var document map[string]interface{}
	if json.Unmarshal(data, &document) != nil {
		return nil, nil
	}
	return document, nil
}

// ClaudeConfigChangeBlockReason decides a Claude Code ConfigChange for the
// settings sources a session can block. It returns a reason when the changed
// settings would disable Reconc's hooks or drop its managed project entries,
// so the running session keeps its current configuration.
func ClaudeConfigChangeBlockReason(root, source string) (string, error) {
	switch source {
	case "user_settings", "project_settings", "local_settings":
	default:
		return "", nil
	}
	disabled, err := claudeHooksDisableSwitch(root)
	if err != nil {
		return "", err
	}
	if disabled != nil {
		return "Reconc keeps its Claude Code hooks enabled in this repository; the change sets " + disabled.detail, nil
	}
	if source != "project_settings" {
		return "", nil
	}
	report, err := InspectPlatform(root, KindClaudeCode)
	if err != nil {
		return "", err
	}
	if report.State != StateConfigured {
		return "Reconc keeps its managed Claude Code hooks in .claude/settings.json; after the change: " + report.Detail, nil
	}
	return "", nil
}
