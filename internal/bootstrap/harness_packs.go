package bootstrap

import (
	"fmt"

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

func validateHarnessPacks(selection Selection, productVersion string) error {
	if selection.Profile != ProfileAdvanced {
		if len(selection.HarnessPacks) != 0 {
			return fmt.Errorf("profile %q cannot select harness packs", selection.Profile)
		}
		return nil
	}
	pack, err := harness.Advanced(productVersion)
	if err != nil {
		return err
	}
	if len(selection.HarnessPacks) != 1 || selection.HarnessPacks[0] != packSelection(pack) {
		return fmt.Errorf("advanced bootstrap selection must contain the exact embedded harness pack")
	}
	return nil
}

func validateHarnessPackSelections(selection Selection) error {
	if selection.Profile != ProfileAdvanced {
		if len(selection.HarnessPacks) != 0 {
			return fmt.Errorf("profile %q cannot select harness packs", selection.Profile)
		}
		return nil
	}
	if len(selection.HarnessPacks) != 1 {
		return fmt.Errorf("advanced bootstrap selection must contain exactly one harness pack")
	}
	selected := selection.HarnessPacks[0]
	if selected.Name == "" || selected.Version == "" || !validSHA256(selected.Digest) {
		return fmt.Errorf("advanced bootstrap harness pack identity is invalid")
	}
	return nil
}

func harnessArtifacts(selection Selection, productVersion string) ([]desiredArtifact, error) {
	if len(selection.HarnessPacks) == 0 {
		return nil, nil
	}
	if err := validateHarnessPacks(selection, productVersion); err != nil {
		return nil, err
	}
	pack, err := harness.Advanced(productVersion)
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
