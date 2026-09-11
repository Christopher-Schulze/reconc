package buildprovenance

import (
	"runtime/debug"
	"testing"
)

func TestDevelopmentVersionUsesOnlyAvailableVCSIdentity(t *testing.T) {
	tests := []struct {
		name     string
		settings []debug.BuildSetting
		want     string
	}{
		{name: "no metadata", want: "dev"},
		{name: "commit", settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "0123456789abcdef"}}, want: "dev+0123456789ab"},
		{name: "dirty commit", settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "true"}, {Key: "vcs.revision", Value: "0123456789abcdef"}}, want: "dev+0123456789ab-dirty"},
		{name: "clean commit", settings: []debug.BuildSetting{{Key: "vcs.modified", Value: "false"}, {Key: "vcs.revision", Value: "0123456789abcdef"}}, want: "dev+0123456789ab"},
		{name: "unrelated module version", settings: []debug.BuildSetting{{Key: "module.version", Value: "v1.2.3"}}, want: "dev"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := developmentVersion(test.settings); got != test.want {
				t.Fatalf("developmentVersion() = %q, want %q", got, test.want)
			}
		})
	}
}
