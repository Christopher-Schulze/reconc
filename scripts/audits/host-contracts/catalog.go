package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/hooks"
)

type sourceCatalog struct {
	Schema        string        `json:"schema"`
	FormatVersion int           `json:"format_version"`
	ReviewedAt    string        `json:"reviewed_at"`
	Hosts         []hostSources `json:"hosts"`
}

type hostSources struct {
	Host     string            `json:"host"`
	Fixtures []fixtureIdentity `json:"fixtures"`
	Sources  []sourceProbe     `json:"sources"`
}

type fixtureIdentity struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type sourceProbe struct {
	ID              string   `json:"id"`
	URL             string   `json:"url"`
	BaselineURL     string   `json:"baseline_url"`
	Normalization   string   `json:"normalization"`
	SHA256          string   `json:"sha256"`
	RequiredMarkers []string `json:"required_markers"`
}

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var sourceIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func loadCatalog(root string) (sourceCatalog, error) {
	var catalog sourceCatalog
	body, err := boundedio.ReadRegularFile(filepath.Join(root, "scripts/audits/host-contracts/sources.json"), maxSourceBytes)
	if err != nil {
		return catalog, err
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&catalog); err != nil {
		return catalog, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return catalog, errors.New("catalog has trailing JSON data")
	}
	if err := validateCatalog(root, catalog); err != nil {
		return catalog, err
	}
	sort.Slice(catalog.Hosts, func(i, j int) bool { return catalog.Hosts[i].Host < catalog.Hosts[j].Host })
	for index := range catalog.Hosts {
		host := &catalog.Hosts[index]
		sort.Slice(host.Fixtures, func(i, j int) bool { return host.Fixtures[i].Path < host.Fixtures[j].Path })
		sort.Slice(host.Sources, func(i, j int) bool { return host.Sources[i].ID < host.Sources[j].ID })
	}
	return catalog, nil
}

func validateCatalog(root string, catalog sourceCatalog) error {
	if catalog.Schema != "reconc-host-contract-sources" || catalog.FormatVersion != 1 {
		return errors.New("unsupported source catalog contract")
	}
	if _, err := time.Parse("2006-01-02", catalog.ReviewedAt); err != nil {
		return fmt.Errorf("reviewed_at: %w", err)
	}
	expected := make(map[string]bool)
	for _, platform := range hooks.Platforms() {
		expected[platform.Kind] = true
	}
	if len(catalog.Hosts) != len(expected) {
		return errors.New("source catalog must cover every registered host")
	}
	for _, host := range catalog.Hosts {
		if !expected[host.Host] {
			return fmt.Errorf("duplicate or unknown host %q", host.Host)
		}
		delete(expected, host.Host)
		if err := validateHostSources(root, host); err != nil {
			return fmt.Errorf("%s: %w", host.Host, err)
		}
	}
	return nil
}

func validateHostSources(root string, host hostSources) error {
	expected := []string{"internal/hooks/testdata/host-events/" + host.Host + ".json"}
	contract := "internal/hooks/testdata/contracts/" + host.Host + ".json"
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(contract))); err == nil {
		expected = append(expected, contract)
	} else if !os.IsNotExist(err) {
		return err
	}
	paths := make([]string, 0, len(host.Fixtures))
	for _, fixture := range host.Fixtures {
		if !digestPattern.MatchString(fixture.SHA256) {
			return errors.New("invalid fixture digest")
		}
		paths = append(paths, fixture.Path)
	}
	slices.Sort(expected)
	slices.Sort(paths)
	if !slices.Equal(paths, expected) {
		return errors.New("fixture inventory does not match saved host contracts")
	}
	if len(host.Sources) == 0 || len(host.Sources) > 6 {
		return errors.New("host requires 1-6 source probes")
	}
	seen := make(map[string]bool)
	for _, source := range host.Sources {
		if seen[source.ID] || !sourceIDPattern.MatchString(source.ID) {
			return errors.New("duplicate or invalid source ID")
		}
		seen[source.ID] = true
		if err := validateProbe(source); err != nil {
			return fmt.Errorf("%s: %w", source.ID, err)
		}
	}
	return nil
}

func validateProbe(source sourceProbe) error {
	if !digestPattern.MatchString(source.SHA256) {
		return errors.New("invalid source digest")
	}
	if err := validateSourceURL(source.URL); err != nil {
		return err
	}
	if err := validateSourceURL(source.BaselineURL); err != nil {
		return err
	}
	if err := validateSourceProvenance(source); err != nil {
		return err
	}
	if source.Normalization != "source" && source.Normalization != "markdown" && source.Normalization != "html" {
		return errors.New("unknown source normalization")
	}
	if len(source.RequiredMarkers) < 2 || len(source.RequiredMarkers) > 16 {
		return errors.New("source requires 2-16 contract markers")
	}
	for _, marker := range source.RequiredMarkers {
		if marker == "" || len(marker) > 256 {
			return errors.New("invalid contract marker")
		}
	}
	return nil
}

func validateSourceProvenance(source sourceProbe) error {
	current, err := url.Parse(source.URL)
	if err != nil {
		return err
	}
	baseline, err := url.Parse(source.BaselineURL)
	if err != nil {
		return err
	}
	if current.Host == "raw.githubusercontent.com" {
		currentParts := strings.Split(strings.TrimPrefix(current.Path, "/"), "/")
		baselineParts := strings.Split(strings.TrimPrefix(baseline.Path, "/"), "/")
		if len(currentParts) < 4 || len(baselineParts) != len(currentParts) || baseline.Host != current.Host ||
			currentParts[2] != "HEAD" || !revisionPattern.MatchString(baselineParts[2]) ||
			currentParts[0] != baselineParts[0] || currentParts[1] != baselineParts[1] ||
			!slices.Equal(currentParts[3:], baselineParts[3:]) {
			return errors.New("GitHub source requires matching HEAD and immutable commit paths")
		}
	} else if source.URL != source.BaselineURL {
		return errors.New("documentation baseline must identify the reviewed source URL")
	}
	return nil
}

var revisionPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)
