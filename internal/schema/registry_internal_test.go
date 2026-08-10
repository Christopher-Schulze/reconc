package schema

import (
	"strings"
	"testing"
)

func TestStaticRegistryIsValid(t *testing.T) {
	if err := ValidateRegistry(); err != nil {
		t.Fatal(err)
	}
}

func TestRegistryValidationRejectsAmbiguousOrUnsafeContracts(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]Contract)
		want   string
	}{
		{name: "duplicate artifact version", mutate: func(values []Contract) {
			values[1] = values[0]
		}, want: "artifact version"},
		{name: "duplicate local path", mutate: func(values []Contract) {
			values[1].LocalPath = values[0].LocalPath
			values[1].DefaultURL = taggedSchemaURL(values[1].IntroductionTag, values[1].LocalPath)
		}, want: "local path"},
		{name: "duplicate release asset", mutate: func(values []Contract) {
			values[1].ReleaseAsset = values[0].ReleaseAsset
		}, want: "release asset"},
		{name: "unordered contracts", mutate: func(values []Contract) {
			values[0], values[1] = values[1], values[0]
		}, want: "not uniquely ordered"},
		{name: "identity alias collision", mutate: func(values []Contract) {
			values[1].Aliases = append(values[1].Aliases, Alias{URL: values[0].DefaultURL, Reason: AliasMisboundReleaseTag})
		}, want: "schema identity"},
		{name: "two current versions", mutate: func(values []Contract) {
			for index := range values {
				if values[index].Artifact == PolicyLock && values[index].SchemaVersion == "3" {
					values[index].State = StateCurrent
				}
			}
		}, want: "2 current contracts"},
		{name: "no current version", mutate: func(values []Contract) {
			for index := range values {
				if values[index].Artifact == CompletionReport {
					values[index].State = StateLegacy
				}
			}
		}, want: "0 current contracts"},
		{name: "mutable default URL", mutate: func(values []Contract) {
			values[0].DefaultURL = "https://example.test/main/schema.json"
		}, want: "not immutable HTTPS"},
		{name: "bad enterprise path", mutate: func(values []Contract) {
			values[0].EnterprisePath = "/schemas/wrong/v1"
		}, want: "enterprise path"},
		{name: "bad introduction tag", mutate: func(values []Contract) {
			values[0].IntroductionTag = "v0.9.2"
		}, want: "introduction tag"},
		{name: "noncanonical introduction tag", mutate: func(values []Contract) {
			values[0].IntroductionTag = "reconc-v0.09.2"
		}, want: "introduction tag"},
		{name: "unsafe release asset", mutate: func(values []Contract) {
			values[0].ReleaseAsset = "schema name.json"
		}, want: "release asset"},
		{name: "bad digest", mutate: func(values []Contract) {
			values[0].SHA256 = "not-a-digest"
		}, want: "digest"},
		{name: "bad state", mutate: func(values []Contract) {
			values[0].State = State("unknown")
		}, want: "state"},
		{name: "duplicate format", mutate: func(values []Contract) {
			values[0].FormatVersions = []string{"1", "1"}
		}, want: "format versions"},
		{name: "unsorted formats", mutate: func(values []Contract) {
			values[0].FormatVersions = []string{"2", "1"}
		}, want: "format versions"},
		{name: "empty format", mutate: func(values []Contract) {
			values[0].FormatVersions = []string{""}
		}, want: "format versions"},
		{name: "noncanonical schema version", mutate: func(values []Contract) {
			values[0].SchemaVersion = "01"
		}, want: "schema version"},
		{name: "unsafe local path", mutate: func(values []Contract) {
			values[0].LocalPath = "schemas/v1/../schema.json"
		}, want: "local path"},
		{name: "unsafe alias", mutate: func(values []Contract) {
			values[0].Aliases = append(values[0].Aliases, Alias{URL: "http://example.test/schema", Reason: AliasMisboundReleaseTag})
		}, want: "alias"},
		{name: "bad alias reason", mutate: func(values []Contract) {
			values[0].Aliases = append(values[0].Aliases, Alias{URL: "https://example.test/schema", Reason: AliasReason("unknown")})
		}, want: "alias"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := Contracts()
			test.mutate(values)
			err := validateContracts(values)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateContracts() = %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestObservationValidationRejectsIncompleteOrAmbiguousEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func([]Observation) []Observation
		want   string
	}{
		{name: "missing observation", mutate: func(values []Observation) []Observation {
			return values[:len(values)-1]
		}, want: "want 22"},
		{name: "duplicate path", mutate: func(values []Observation) []Observation {
			values[1].LocalPath = values[0].LocalPath
			return values
		}, want: "duplicate"},
		{name: "bad claimed URL", mutate: func(values []Observation) []Observation {
			values[0].ClaimedURL = "http://example.test/schema"
			return values
		}, want: "invalid"},
		{name: "bad exact tag", mutate: func(values []Observation) []Observation {
			values[0].FirstExactTag = "v0.9.2"
			return values
		}, want: "invalid"},
		{name: "noncanonical exact tag", mutate: func(values []Observation) []Observation {
			values[0].FirstExactTag = "reconc-v0.09.2"
			return values
		}, want: "invalid"},
		{name: "bad digest", mutate: func(values []Observation) []Observation {
			values[0].SHA256 = "bad"
			return values
		}, want: "invalid"},
		{name: "bad compatibility", mutate: func(values []Observation) []Observation {
			values[0].Compatibility = Compatibility("unknown")
			return values
		}, want: "invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateObservations(test.mutate(Observations()))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateObservations() = %v, want error containing %q", err, test.want)
			}
		})
	}
}
