// Command host-contracts compares saved host fixtures and official upstream
// sources. Network access is explicit and never part of the product runtime.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"reconc.dev/reconc/internal/boundedio"
)

type comparison struct {
	Host           string   `json:"host"`
	Surface        string   `json:"surface"`
	Kind           string   `json:"kind"`
	Status         string   `json:"status"`
	URL            string   `json:"url,omitempty"`
	BaselineURL    string   `json:"baseline_url,omitempty"`
	ExpectedSHA256 string   `json:"expected_sha256"`
	ActualSHA256   string   `json:"actual_sha256,omitempty"`
	MissingMarkers []string `json:"missing_markers,omitempty"`
	Error          string   `json:"error,omitempty"`
}

type report struct {
	Schema         string       `json:"schema"`
	FormatVersion  int          `json:"format_version"`
	Mode           string       `json:"mode"`
	Complete       bool         `json:"complete"`
	ReviewRequired bool         `json:"review_required"`
	SourcesChecked int          `json:"sources_checked"`
	Results        []comparison `json:"results"`
}

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	cancel()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("host-contracts", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "Reconc source root")
	upstream := flags.Bool("check-upstream", false, "fetch official sources without executing them")
	if err := flags.Parse(args); err != nil {
		return 1
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: host-contracts [--root PATH] [--check-upstream]")
		return 1
	}
	catalog, err := loadCatalog(*root)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	result := compareCatalog(ctx, *root, catalog, *upstream)
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !result.Complete {
		return 1
	}
	if result.ReviewRequired {
		return 2
	}
	return 0
}

func compareCatalog(ctx context.Context, root string, catalog sourceCatalog, upstream bool) report {
	result := report{Schema: "reconc-host-contract-report", FormatVersion: 1, Mode: "offline", Complete: true, Results: []comparison{}}
	if upstream {
		result.Mode = "upstream"
	}
	client := sourceClient()
	for _, host := range catalog.Hosts {
		for _, fixture := range host.Fixtures {
			row := comparison{Host: host.Host, Surface: fixture.Path, Kind: "fixture", ExpectedSHA256: fixture.SHA256}
			body, err := boundedio.ReadRegularFile(filepath.Join(root, filepath.FromSlash(fixture.Path)), maxSourceBytes)
			completeComparison(&row, body, err)
			result.add(row)
		}
		if !upstream {
			continue
		}
		for _, source := range host.Sources {
			row := compareSource(ctx, client, host.Host, source)
			result.SourcesChecked++
			result.add(row)
		}
	}
	return result
}

func (r *report) add(row comparison) {
	r.Complete = r.Complete && row.Status != "unavailable"
	r.ReviewRequired = r.ReviewRequired || row.Status == "changed"
	r.Results = append(r.Results, row)
}

func completeComparison(row *comparison, body []byte, err error) {
	if err != nil {
		row.Status, row.Error = "unavailable", err.Error()
		return
	}
	row.ActualSHA256 = digest(body)
	row.Status = "unchanged"
	if row.ActualSHA256 != row.ExpectedSHA256 {
		row.Status = "changed"
	}
}
