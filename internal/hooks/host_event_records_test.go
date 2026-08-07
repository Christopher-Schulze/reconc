package hooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// hostEventRecord is the transcribed event vocabulary of one host, taken from
// that host's own published reference or source. It exists so the registry is
// checked against the host instead of against itself: before these records, a
// test that asserted "Codex has no SessionEnd" read that claim back out of the
// registry that made it, and the claim was wrong for two years of Codex
// releases.
type hostEventRecord struct {
	Host           string `json:"host"`
	DisplayName    string `json:"display_name"`
	HostVersion    string `json:"host_version"`
	SourceURL      string `json:"source_url"`
	SourceRevision string `json:"source_revision"`
	CaptureDate    string `json:"capture_date"`
	Scope          string `json:"scope"`
	Events         []struct {
		Name   string `json:"name"`
		Bound  bool   `json:"bound"`
		Reason string `json:"reason"`
	} `json:"events"`
}

var hostEventCaptureDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

func readHostEventRecord(t *testing.T, kind string) hostEventRecord {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "host-events", kind+".json"))
	if err != nil {
		t.Fatalf("read host event record: %v", err)
	}
	var record hostEventRecord
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("parse host event record for %s: %v", kind, err)
	}
	return record
}

// TestEveryPlatformHasATraceableHostEventRecord keeps the records usable as
// evidence. A record without a reachable source and a capture date is an
// assertion, not a transcription.
func TestEveryPlatformHasATraceableHostEventRecord(t *testing.T) {
	for _, platform := range Platforms() {
		t.Run(platform.Kind, func(t *testing.T) {
			record := readHostEventRecord(t, platform.Kind)
			if record.Host != platform.Kind {
				t.Errorf("record host = %q, want %q", record.Host, platform.Kind)
			}
			if record.DisplayName != platform.DisplayName {
				t.Errorf("record display name = %q, want %q", record.DisplayName, platform.DisplayName)
			}
			if !strings.HasPrefix(record.SourceURL, "https://") {
				t.Errorf("record source must be a reachable https location, got %q", record.SourceURL)
			}
			if !hostEventCaptureDate.MatchString(record.CaptureDate) {
				t.Errorf("record capture date = %q, want YYYY-MM-DD", record.CaptureDate)
			}
			if strings.TrimSpace(record.Scope) == "" {
				t.Error("record must state which part of the host vocabulary it covers")
			}
			if record.SourceRevision != "" {
				if len(record.SourceRevision) != 40 || strings.ContainsAny(record.SourceRevision, "ghijklmnopqrstuvwxyzGHIJKLMNOPQRSTUVWXYZ") {
					t.Errorf("record source revision = %q, want a full commit hash", record.SourceRevision)
				}
				if !strings.Contains(record.SourceURL, record.SourceRevision) {
					t.Error("a pinned revision must be the one the source URL addresses")
				}
			}
			if len(record.Events) == 0 {
				t.Error("record lists no events")
			}
			seen := map[string]bool{}
			for _, event := range record.Events {
				if strings.TrimSpace(event.Name) == "" {
					t.Error("record lists an unnamed event")
				}
				if seen[event.Name] {
					t.Errorf("record lists %q twice", event.Name)
				}
				seen[event.Name] = true
			}
		})
	}
}

// TestRegistryBindingsMatchTheHostEventRecords is the check that would have
// caught this release's gaps. It fails in both directions: a route the host
// does not publish, and an event the host publishes that the registry drops
// without saying why.
func TestRegistryBindingsMatchTheHostEventRecords(t *testing.T) {
	for _, platform := range Platforms() {
		t.Run(platform.Kind, func(t *testing.T) {
			record := readHostEventRecord(t, platform.Kind)
			published := map[string]bool{}
			claimedBound := map[string]bool{}
			for _, event := range record.Events {
				published[event.Name] = true
				if event.Bound {
					claimedBound[event.Name] = true
					continue
				}
				// An unbound event is a decision. Without a reason it is
				// indistinguishable from an oversight, which is exactly how the
				// gaps in this release survived.
				if strings.TrimSpace(event.Reason) == "" {
					t.Errorf("%s publishes %q and the registry leaves it unbound without a recorded reason", platform.Kind, event.Name)
				}
			}

			registryBound := map[string]bool{}
			for _, capability := range platform.Capabilities {
				if capability.Support == SupportUnsupported {
					continue
				}
				for _, binding := range capability.Bindings {
					if binding.NativeEvent == "" {
						continue
					}
					registryBound[binding.NativeEvent] = true
					if !published[binding.NativeEvent] {
						t.Errorf("registry binds %q, which the %s record does not list as published", binding.NativeEvent, platform.Kind)
					}
				}
			}

			for name := range claimedBound {
				if !registryBound[name] {
					t.Errorf("the %s record claims %q is bound, but the registry does not bind it", platform.Kind, name)
				}
			}

			missing := []string{}
			for _, event := range record.Events {
				if event.Bound && !registryBound[event.Name] {
					missing = append(missing, event.Name)
				}
			}
			sort.Strings(missing)
			if len(missing) > 0 {
				t.Errorf("%s record and registry disagree on: %s", platform.Kind, strings.Join(missing, ", "))
			}
		})
	}
}
