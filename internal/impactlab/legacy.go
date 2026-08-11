package impactlab

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"unicode/utf8"

	"reconc.dev/reconc/internal/runtime"
)

type legacyCase struct {
	ID                   string                  `json:"id"`
	Inputs               runtime.ExecutionInputs `json:"inputs"`
	RedactedEventClasses []EventClass            `json:"redacted_event_classes"`
	RedactionCount       int                     `json:"redaction_count"`
}

type legacyCompleteness struct {
	ObservedEventClasses []EventClass `json:"observed_event_classes"`
	CompleteEventClasses []EventClass `json:"complete_event_classes"`
	MissingEventClasses  []EventClass `json:"missing_event_classes"`
	RedactedEventClasses []EventClass `json:"redacted_event_classes"`
	RedactionCount       int          `json:"redaction_count"`
	CompleteReplay       bool         `json:"complete_replay"`
}

type legacyCorpus struct {
	FormatVersion string             `json:"format_version"`
	CorpusID      string             `json:"corpus_id"`
	Completeness  legacyCompleteness `json:"completeness"`
	Cases         []legacyCase       `json:"cases"`
}

// MigrateCorpusV1 validates one immutable format-1 corpus before returning its
// deterministic format-2 repository-case representation.
func MigrateCorpusV1(body []byte) (Corpus, error) {
	if len(body) > MaxCorpusBytes || !utf8.Valid(body) || !json.Valid(body) {
		return Corpus{}, fmt.Errorf("legacy impact corpus is oversized, invalid UTF-8, or invalid JSON")
	}
	if err := validateUniqueJSONKeys(body); err != nil {
		return Corpus{}, err
	}
	return migrateLegacyCorpus(body)
}

func migrateLegacyCorpus(body []byte) (Corpus, error) {
	if err := validateExactJSONFields(body, reflect.TypeOf(legacyCorpus{})); err != nil {
		return Corpus{}, err
	}
	var legacy legacyCorpus
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&legacy); err != nil {
		return Corpus{}, fmt.Errorf("decode legacy impact corpus: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Corpus{}, fmt.Errorf("legacy impact corpus must contain exactly one JSON object")
	}
	if err := validateLegacyCorpus(legacy); err != nil {
		return Corpus{}, err
	}
	cases := make([]Case, len(legacy.Cases))
	for index, old := range legacy.Cases {
		cases[index] = Case{ID: old.ID, Kind: CaseRepository, Repository: &RepositoryCase{
			Inputs: old.Inputs, RedactedEventClasses: old.RedactedEventClasses,
			RedactionCount: old.RedactionCount,
		}}
	}
	migrated := Corpus{FormatVersion: CorpusFormatVersion, Cases: cases}
	migrated.Completeness = buildCompleteness(cases, legacy.Completeness.CompleteEventClasses, emptyActionDimensions())
	identity, err := corpusIdentity(migrated)
	if err != nil {
		return Corpus{}, err
	}
	migrated.CorpusID = identity
	if err := validateCorpus(migrated); err != nil {
		return Corpus{}, fmt.Errorf("validate migrated impact corpus: %w", err)
	}
	return migrated, nil
}

func validateLegacyCorpus(corpus legacyCorpus) error {
	if corpus.FormatVersion != LegacyCorpusFormatVersion || corpus.CorpusID == "" ||
		len(corpus.Cases) == 0 || len(corpus.Cases) > maxCases || corpus.Cases == nil {
		return fmt.Errorf("unsupported or incomplete legacy impact corpus contract")
	}
	identity, err := legacyCorpusIdentity(corpus)
	if err != nil {
		return fmt.Errorf("encode legacy impact corpus identity: %w", err)
	}
	if corpus.CorpusID != identity {
		return fmt.Errorf("legacy impact corpus identity does not match its contents")
	}
	totalItems := 0
	cases := make([]Case, len(corpus.Cases))
	for index, old := range corpus.Cases {
		if !validCaseID(old.ID) || index > 0 && corpus.Cases[index-1].ID >= old.ID {
			return fmt.Errorf("legacy impact corpus case ids must be valid, unique, and sorted")
		}
		items, err := validateRepositoryCase(RepositoryCase{
			Inputs: old.Inputs, RedactedEventClasses: old.RedactedEventClasses,
			RedactionCount: old.RedactionCount,
		})
		if err != nil {
			return fmt.Errorf("legacy impact corpus case[%d]: %w", index, err)
		}
		totalItems += items
		cases[index] = Case{ID: old.ID, Kind: CaseRepository, Repository: &RepositoryCase{
			Inputs: old.Inputs, RedactedEventClasses: old.RedactedEventClasses,
			RedactionCount: old.RedactionCount,
		}}
	}
	if totalItems > maxTotalItems {
		return fmt.Errorf("legacy impact corpus exceeds %d evidence items", maxTotalItems)
	}
	want := buildCompleteness(cases, corpus.Completeness.CompleteEventClasses, emptyActionDimensions())
	got := corpus.Completeness
	if got.ObservedEventClasses == nil || got.CompleteEventClasses == nil ||
		got.MissingEventClasses == nil || got.RedactedEventClasses == nil ||
		!equalEventClasses(want.ObservedEventClasses, got.ObservedEventClasses) ||
		!equalEventClasses(want.CompleteEventClasses, got.CompleteEventClasses) ||
		!equalEventClasses(want.MissingEventClasses, got.MissingEventClasses) ||
		!equalEventClasses(want.RedactedEventClasses, got.RedactedEventClasses) ||
		want.RedactionCount != got.RedactionCount || want.CompleteReplay != got.CompleteReplay {
		return fmt.Errorf("legacy impact corpus completeness metadata is inconsistent")
	}
	return nil
}

func legacyCorpusIdentity(corpus legacyCorpus) (string, error) {
	corpus.CorpusID = ""
	body, err := json.Marshal(corpus)
	if err != nil {
		return "", fmt.Errorf("encode legacy impact corpus identity: %w", err)
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}
