package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDevelopmentInventoryRetainsSourceIdentity(t *testing.T) {
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range []string{"dev", "dev+0123456789ab", "dev+0123456789ab-dirty"} {
		t.Run(version, func(t *testing.T) {
			options := commandOptions{root: root, outputDir: t.TempDir(), version: version, commit: testCommit, epoch: "1700000000"}
			inventory, err := collectInventory(context.Background(), options)
			if err != nil {
				t.Fatal(err)
			}
			roots := 0
			for _, module := range inventory.Modules {
				if module.Root {
					roots++
					if module.Version != version {
						t.Fatalf("root %s identity = %q, want %q", module.Path, module.Version, version)
					}
				}
			}
			if roots != 2 {
				t.Fatalf("root modules = %d, want 2", roots)
			}
			spdx, cyclonedx, err := renderDocuments(inventory)
			if err != nil {
				t.Fatal(err)
			}
			if err := writeDocuments(options.outputDir, version, spdx, cyclonedx); err != nil {
				t.Fatal(err)
			}
			if err := verifyDocuments(options.outputDir, version, spdx, cyclonedx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
