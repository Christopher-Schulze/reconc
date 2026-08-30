package bootstrap

import (
	"fmt"
	"strings"

	"reconc.dev/reconc/harness"
	"reconc.dev/reconc/internal/harnesspack"
)

func attachHarnessPacks(selection Selection, productVersion string) (Selection, error) {
	if selection.Profile != ProfileAdvanced {
		selection.HarnessPacks = []HarnessPackSelection{}
		return selection, nil
	}
	pack, err := harness.Advanced(productVersion)
	if err != nil {
		return Selection{}, err
	}
	selection.HarnessPacks = []HarnessPackSelection{packSelection(pack)}
	return selection, nil
}

func validatedHarnessPack(selection Selection, productVersion string) (*harnesspack.Pack, error) {
	if selection.Profile != ProfileAdvanced {
		if len(selection.HarnessPacks) != 0 {
			return nil, fmt.Errorf("profile %q cannot select harness packs", selection.Profile)
		}
		return nil, nil
	}
	pack, err := harness.Advanced(productVersion)
	if err != nil {
		return nil, err
	}
	if len(selection.HarnessPacks) != 1 || selection.HarnessPacks[0] != packSelection(pack) {
		return nil, fmt.Errorf("advanced bootstrap selection must contain the exact embedded harness pack")
	}
	return pack, nil
}

func validateHarnessPackActions(pack *harnesspack.Pack, actions []Action) error {
	if pack == nil {
		for _, action := range actions {
			if strings.HasPrefix(action.Component, "harness-pack:") {
				return fmt.Errorf("bootstrap plan contains foreign harness pack artifact %s", action.Path)
			}
		}
		return nil
	}
	component := fmt.Sprintf("harness-pack:%s@%s", pack.Manifest.Name, pack.Manifest.Version)
	actionByPath := make(map[string]Action, len(actions))
	for _, action := range actions {
		actionByPath[action.Path] = action
	}
	packPaths := make(map[string]bool, len(pack.Files))
	for _, file := range pack.Files {
		packPaths[file.File.Path] = true
		action, ok := actionByPath[file.File.Path]
		if !ok || action.Component != component || action.Mode != file.File.Mode || action.DesiredSHA256 != file.File.SHA256 {
			return fmt.Errorf("bootstrap plan harness pack artifact %s is not bound to the embedded pack", file.File.Path)
		}
	}
	for _, action := range actions {
		if strings.HasPrefix(action.Component, "harness-pack:") && !packPaths[action.Path] {
			return fmt.Errorf("bootstrap plan contains foreign harness pack artifact %s", action.Path)
		}
	}
	return nil
}

func harnessArtifacts(selection Selection, productVersion string) ([]desiredArtifact, error) {
	if len(selection.HarnessPacks) == 0 {
		return nil, nil
	}
	pack, err := validatedHarnessPack(selection, productVersion)
	if err != nil {
		return nil, err
	}
	component := fmt.Sprintf("harness-pack:%s@%s", pack.Manifest.Name, pack.Manifest.Version)
	artifacts := make([]desiredArtifact, 0, len(pack.Files))
	for _, file := range pack.Files {
		artifacts = append(artifacts, desiredArtifact{
			component: component,
			path:      file.File.Path,
			mode:      file.File.Mode,
			content:   file.Body,
		})
	}
	return artifacts, nil
}

func packSelection(pack *harnesspack.Pack) HarnessPackSelection {
	return HarnessPackSelection{
		Name: pack.Manifest.Name, Version: pack.Manifest.Version, Digest: pack.Manifest.Digest,
	}
}
