package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/schema"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "schema-assets:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: schema-assets list-release|json|verify-published")
	}
	if err := schema.ValidateRegistry(); err != nil {
		return fmt.Errorf("validate schema registry: %w", err)
	}
	contracts := schema.Contracts()
	root, err := findRepositoryRoot()
	if err != nil {
		return err
	}
	if err := verifyLocalContracts(root, contracts); err != nil {
		return err
	}
	switch args[0] {
	case "list-release":
		sort.Slice(contracts, func(left, right int) bool {
			return contracts[left].ReleaseAsset < contracts[right].ReleaseAsset
		})
		return writeReleaseAssets(stdout, contracts)
	case "json":
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(contracts)
	case "verify-published":
		if err := verifyPublished(context.Background(), publicationClient(), root, contracts); err != nil {
			return err
		}
		_, err = fmt.Fprintf(stdout, "verified %d published schemas\n", len(contracts))
		return err
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

const maxPublishedSchemaBytes = 4 << 20

func findRepositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("resolve working directory: %w", err)
	}
	for {
		module, moduleErr := os.Stat(filepath.Join(current, "go.mod"))
		schemas, schemasErr := os.Stat(filepath.Join(current, "schemas"))
		if moduleErr == nil && module.Mode().IsRegular() && schemasErr == nil && schemas.IsDir() {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("repository root containing go.mod and schemas was not found")
		}
		current = parent
	}
}

func verifyLocalContracts(root string, contracts []schema.Contract) error {
	for _, contract := range contracts {
		if _, err := readRegisteredSchema(root, contract); err != nil {
			return err
		}
	}
	return nil
}

func readRegisteredSchema(root string, contract schema.Contract) ([]byte, error) {
	local, err := boundedio.ReadRegularFile(filepath.Join(root, filepath.FromSlash(contract.LocalPath)), maxPublishedSchemaBytes)
	if err != nil {
		return nil, fmt.Errorf("read local schema %s: %w", contract.LocalPath, err)
	}
	digest := sha256.Sum256(local)
	if got := fmt.Sprintf("%x", digest); got != contract.SHA256 {
		return nil, fmt.Errorf("local schema %s digest %s does not match registry %s", contract.LocalPath, got, contract.SHA256)
	}
	if err := rejectDuplicateJSONNames(local); err != nil {
		return nil, fmt.Errorf("decode local schema %s: %w", contract.LocalPath, err)
	}
	var document struct {
		MetaSchema string `json:"$schema"`
		ID         string `json:"$id"`
	}
	if err := json.Unmarshal(local, &document); err != nil {
		return nil, fmt.Errorf("decode local schema %s: %w", contract.LocalPath, err)
	}
	if document.MetaSchema != "https://json-schema.org/draft/2020-12/schema" {
		return nil, fmt.Errorf("local schema %s uses unsupported meta-schema %q", contract.LocalPath, document.MetaSchema)
	}
	if document.ID != contract.DefaultURL {
		return nil, fmt.Errorf("local schema %s $id %q does not match registry %q", contract.LocalPath, document.ID, contract.DefaultURL)
	}
	return local, nil
}

func rejectDuplicateJSONNames(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := consumeJSONValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("schema contains more than one JSON value")
		}
		return err
	}
	return nil
}

func consumeJSONValue(decoder *json.Decoder, depth int) error {
	if depth > 256 {
		return errors.New("schema exceeds 256 JSON container levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]struct{}{}
		for decoder.More() {
			nameToken, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := nameToken.(string)
			if !ok {
				return errors.New("schema object name is not a string")
			}
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("schema contains duplicate object name %q", name)
			}
			seen[name] = struct{}{}
			if err := consumeJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := consumeJSONValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("schema contains unexpected JSON delimiter %q", delimiter)
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	want := json.Delim('}')
	if delimiter == '[' {
		want = ']'
	}
	if closing != want {
		return fmt.Errorf("schema JSON delimiter %q closes with %q", delimiter, closing)
	}
	return nil
}

func publicationClient() *http.Client {
	return &http.Client{
		Timeout: 20 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("schema publication redirects are forbidden")
		},
	}
}

func verifyPublished(ctx context.Context, client *http.Client, root string, contracts []schema.Contract) error {
	if client == nil {
		return errors.New("published schema HTTP client is nil")
	}
	for _, contract := range contracts {
		local, err := readRegisteredSchema(root, contract)
		if err != nil {
			return err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, contract.DefaultURL, nil)
		if err != nil {
			return fmt.Errorf("prepare published schema %s: %w", contract.DefaultURL, err)
		}
		request.Header.Set("User-Agent", "reconc-schema-publication-verifier")
		response, err := client.Do(request)
		if err != nil {
			return fmt.Errorf("fetch published schema %s: %w", contract.DefaultURL, err)
		}
		remote, readErr := io.ReadAll(io.LimitReader(response.Body, maxPublishedSchemaBytes+1))
		closeErr := response.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read published schema %s: %w", contract.DefaultURL, readErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close published schema %s: %w", contract.DefaultURL, closeErr)
		}
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("published schema %s returned HTTP %d", contract.DefaultURL, response.StatusCode)
		}
		if len(remote) > maxPublishedSchemaBytes {
			return fmt.Errorf("published schema %s exceeds %d bytes", contract.DefaultURL, maxPublishedSchemaBytes)
		}
		if !bytes.Equal(remote, local) {
			return fmt.Errorf("published schema %s bytes differ from %s", contract.DefaultURL, contract.LocalPath)
		}
	}
	return nil
}

func writeReleaseAssets(stdout io.Writer, contracts []schema.Contract) error {
	for _, contract := range contracts {
		if strings.ContainsAny(contract.ReleaseAsset+contract.LocalPath, "\t\r\n") {
			return fmt.Errorf("unsafe schema asset mapping for %q", contract.Artifact)
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\n", contract.ReleaseAsset, contract.LocalPath); err != nil {
			return err
		}
	}
	return nil
}
