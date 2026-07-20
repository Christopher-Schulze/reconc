package grokacp

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	nativeStopGateGuidePath = "docs/user-guide/10-hooks.md"
	nativeStopGateGuideMax  = 1 << 20
)

// NativeStopGateProbe reports whether the installed Grok distribution
// explicitly documents the blocking Stop-hook protocol that Reconc relies on.
type NativeStopGateProbe struct {
	Supported         bool
	DocumentationPath string
	Detail            string
}

// ProbeNativeStopGate checks the guide shipped with the installed Grok build.
// Version numbers are deliberately ignored because a published version can be
// built from source that predates a later same-version capability commit.
func ProbeNativeStopGate() NativeStopGateProbe {
	home := strings.TrimSpace(os.Getenv(grokHomeEnv))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return NativeStopGateProbe{Detail: "resolve Grok home: " + err.Error()}
		}
		home = filepath.Join(userHome, ".grok")
	}
	return probeNativeStopGateAt(filepath.Join(home, filepath.FromSlash(nativeStopGateGuidePath)))
}

func probeNativeStopGateAt(path string) NativeStopGateProbe {
	probe := NativeStopGateProbe{DocumentationPath: path}
	pathInfo, err := os.Lstat(path)
	if err != nil {
		probe.Detail = "inspect installed Grok hook guide: " + err.Error()
		return probe
	}
	if !pathInfo.Mode().IsRegular() {
		probe.Detail = "installed Grok hook guide is not a regular file"
		return probe
	}
	if pathInfo.Size() > nativeStopGateGuideMax {
		probe.Detail = fmt.Sprintf("installed Grok hook guide exceeds %d bytes", nativeStopGateGuideMax)
		return probe
	}
	file, err := os.Open(path)
	if err != nil {
		probe.Detail = "read installed Grok hook guide: " + err.Error()
		return probe
	}
	defer file.Close()
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openedInfo) {
		probe.Detail = "installed Grok hook guide changed while opening"
		return probe
	}

	guide, err := io.ReadAll(io.LimitReader(file, nativeStopGateGuideMax+1))
	if err != nil {
		probe.Detail = "read installed Grok hook guide: " + err.Error()
		return probe
	}
	if len(guide) > nativeStopGateGuideMax {
		probe.Detail = fmt.Sprintf("installed Grok hook guide exceeds %d bytes", nativeStopGateGuideMax)
		return probe
	}
	if !documentationSupportsNativeStopGate(guide) {
		probe.Detail = "installed Grok hook guide does not advertise blocking Stop decision control"
		return probe
	}
	probe.Supported = true
	probe.Detail = "installed Grok hook guide advertises blocking Stop decision control"
	return probe
}

func documentationSupportsNativeStopGate(guide []byte) bool {
	stopRow := false
	decisionSection := false
	scanner := bufio.NewScanner(strings.NewReader(string(guide)))
	for scanner.Scan() {
		line := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if strings.HasPrefix(line, "### stop decision control") {
			decisionSection = true
		}
		if strings.HasPrefix(line, "| `stop` |") && strings.Contains(line, "yes") && strings.Contains(line, "block") {
			stopRow = true
		}
	}
	return scanner.Err() == nil && stopRow && decisionSection
}
