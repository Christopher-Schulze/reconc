package impactlab

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"unicode/utf8"

	"reconc.dev/reconc/internal/boundedio"
)

// NewCorpus normalizes and sanitizes explicit evidence using the production
// runtime input contract, then binds the result to a deterministic identity.
func NewCorpus(repoRoot string, cases []Case, complete []EventClass) (Corpus, error) {
	if len(cases) == 0 || len(cases) > maxCases {
		return Corpus{}, fmt.Errorf("impact corpus must contain 1..%d cases", maxCases)
	}
	for _, class := range complete {
		if !eventClassValid(class) {
			return Corpus{}, fmt.Errorf("unsupported impact event class %q", class)
		}
	}
	sanitized := make([]Case, 0, len(cases))
	for _, replayCase := range cases {
		cleaned, err := sanitizeCase(repoRoot, replayCase)
		if err != nil {
			return Corpus{}, err
		}
		sanitized = append(sanitized, cleaned)
	}
	sort.Slice(sanitized, func(left, right int) bool { return sanitized[left].ID < sanitized[right].ID })
	if err := rejectDuplicateCaseIDs(sanitized); err != nil {
		return Corpus{}, err
	}
	corpus := Corpus{FormatVersion: CorpusFormatVersion, Cases: sanitized}
	corpus.Completeness = buildCompleteness(sanitized, canonicalEventClasses(complete))
	corpus.CorpusID = corpusIdentity(corpus)
	if err := validateCorpus(corpus); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

// MergeCorpora combines fixtures independent of argument order. A class is
// complete only when every source corpus declares complete coverage for it.
func MergeCorpora(corpora []Corpus) (Corpus, error) {
	if len(corpora) == 0 {
		return Corpus{}, fmt.Errorf("at least one impact corpus is required")
	}
	cases := []Case{}
	complete := AllEventClasses()
	for _, corpus := range corpora {
		if err := validateCorpus(corpus); err != nil {
			return Corpus{}, err
		}
		cases = append(cases, corpus.Cases...)
		complete = eventClassIntersection(complete, corpus.Completeness.CompleteEventClasses)
	}
	if len(cases) > maxCases {
		return Corpus{}, fmt.Errorf("merged impact corpus exceeds %d cases", maxCases)
	}
	sort.Slice(cases, func(left, right int) bool { return cases[left].ID < cases[right].ID })
	if err := rejectDuplicateCaseIDs(cases); err != nil {
		return Corpus{}, err
	}
	merged := Corpus{FormatVersion: CorpusFormatVersion, Cases: cases}
	merged.Completeness = buildCompleteness(cases, complete)
	merged.CorpusID = corpusIdentity(merged)
	return merged, validateCorpus(merged)
}

// DecodeCorpusFile reads one bounded regular non-symlink corpus.
func DecodeCorpusFile(filePath string) (Corpus, error) {
	info, err := os.Lstat(filePath)
	if err != nil {
		return Corpus{}, err
	}
	if !info.Mode().IsRegular() {
		return Corpus{}, fmt.Errorf("impact corpus %s must be a regular file and not a symlink", filePath)
	}
	body, err := boundedio.ReadFile(filePath, MaxCorpusBytes)
	if err != nil {
		return Corpus{}, err
	}
	return DecodeCorpus(body)
}

// DecodeCorpus strictly decodes one versioned corpus object.
func DecodeCorpus(body []byte) (Corpus, error) {
	if len(body) > MaxCorpusBytes || !utf8.Valid(body) {
		return Corpus{}, fmt.Errorf("impact corpus is oversized or invalid UTF-8")
	}
	if err := validateUniqueJSONKeys(body); err != nil {
		return Corpus{}, err
	}
	var corpus Corpus
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&corpus); err != nil {
		return Corpus{}, fmt.Errorf("decode impact corpus: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Corpus{}, fmt.Errorf("impact corpus must contain exactly one JSON object")
	}
	if err := validateCorpus(corpus); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

// MarshalCorpus returns deterministic indented JSON with a trailing newline.
func MarshalCorpus(corpus Corpus) ([]byte, error) {
	if err := validateCorpus(corpus); err != nil {
		return nil, err
	}
	body, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if len(body) > MaxCorpusBytes {
		return nil, fmt.Errorf("impact corpus exceeds %d bytes", MaxCorpusBytes)
	}
	return body, nil
}

func validateCorpus(corpus Corpus) error {
	if corpus.FormatVersion != CorpusFormatVersion || corpus.CorpusID == "" {
		return fmt.Errorf("unsupported or incomplete impact corpus contract")
	}
	if len(corpus.Cases) == 0 || len(corpus.Cases) > maxCases {
		return fmt.Errorf("impact corpus must contain 1..%d cases", maxCases)
	}
	if corpus.Cases == nil || corpus.Completeness.ObservedEventClasses == nil ||
		corpus.Completeness.CompleteEventClasses == nil || corpus.Completeness.MissingEventClasses == nil ||
		corpus.Completeness.RedactedEventClasses == nil {
		return fmt.Errorf("impact corpus required collections must not be null")
	}
	if corpus.CorpusID != corpusIdentity(corpus) {
		return fmt.Errorf("impact corpus identity does not match its contents")
	}
	if err := validateCases(corpus.Cases); err != nil {
		return err
	}
	return validateCompleteness(corpus)
}

func validateCases(cases []Case) error {
	totalItems := 0
	for index, replayCase := range cases {
		if !validCaseID(replayCase.ID) || replayCase.RedactionCount < 0 {
			return fmt.Errorf("impact corpus case[%d] has invalid identity or redaction count", index)
		}
		if index > 0 && cases[index-1].ID >= replayCase.ID {
			return fmt.Errorf("impact corpus case ids must be unique and lexically sorted")
		}
		if replayCase.Inputs.ReadPaths == nil || replayCase.Inputs.WritePaths == nil ||
			replayCase.Inputs.Commands == nil ||
			replayCase.Inputs.Claims == nil || replayCase.Inputs.CommandResults == nil ||
			replayCase.RedactedEventClasses == nil {
			return fmt.Errorf("impact corpus case[%d] contains a null collection", index)
		}
		items, err := validateCaseInputs(replayCase)
		if err != nil {
			return fmt.Errorf("impact corpus case[%d]: %w", index, err)
		}
		totalItems += items
	}
	if totalItems > maxTotalItems {
		return fmt.Errorf("impact corpus exceeds %d evidence items", maxTotalItems)
	}
	return rejectDuplicateCaseIDs(cases)
}

func validateCaseInputs(replayCase Case) (int, error) {
	inputs := replayCase.Inputs
	lengths := []int{len(inputs.ReadPaths), len(inputs.WritePaths), len(inputs.WriteEpochs), len(inputs.Commands), len(inputs.Claims), len(inputs.CommandResults)}
	for _, length := range lengths {
		if length > maxItemsPerField {
			return 0, fmt.Errorf("evidence field exceeds %d items", maxItemsPerField)
		}
	}
	if err := validatePrivateInputs(inputs); err != nil {
		return 0, err
	}
	for path := range inputs.WriteEpochs {
		if !validReplayPath(path) {
			return 0, fmt.Errorf("write epoch path %q is invalid", path)
		}
	}
	redacted := canonicalEventClasses(replayCase.RedactedEventClasses)
	if !equalEventClasses(redacted, replayCase.RedactedEventClasses) {
		return 0, fmt.Errorf("redacted event classes are not canonical")
	}
	return len(inputs.ReadPaths) + len(inputs.WritePaths) + len(inputs.Commands) +
		len(inputs.WriteEpochs) + len(inputs.Claims) + len(inputs.CommandResults), nil
}

func validCaseID(value string) bool {
	if value == "" || len(value) > maxCaseIDBytes {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || index > 0 && (character == '-' || character == '_' || character == '.') {
			continue
		}
		return false
	}
	return true
}

func validateCompleteness(corpus Corpus) error {
	want := buildCompleteness(corpus.Cases, corpus.Completeness.CompleteEventClasses)
	if !equalEventClasses(want.ObservedEventClasses, corpus.Completeness.ObservedEventClasses) ||
		!equalEventClasses(want.CompleteEventClasses, corpus.Completeness.CompleteEventClasses) ||
		!equalEventClasses(want.MissingEventClasses, corpus.Completeness.MissingEventClasses) ||
		!equalEventClasses(want.RedactedEventClasses, corpus.Completeness.RedactedEventClasses) ||
		want.RedactionCount != corpus.Completeness.RedactionCount ||
		want.CompleteReplay != corpus.Completeness.CompleteReplay {
		return fmt.Errorf("impact corpus completeness metadata is inconsistent")
	}
	return nil
}

func corpusIdentity(corpus Corpus) string {
	corpus.CorpusID = ""
	body, _ := json.Marshal(corpus)
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func rejectDuplicateCaseIDs(cases []Case) error {
	for index := 1; index < len(cases); index++ {
		if cases[index-1].ID == cases[index].ID {
			return fmt.Errorf("duplicate impact corpus case id %q", cases[index].ID)
		}
	}
	return nil
}

func validateUniqueJSONKeys(body []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("impact corpus must contain a JSON object")
	}
	if err := validateJSONContainer(decoder, '{'); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("impact corpus must contain exactly one JSON value")
	}
	return nil
}

func validateJSONContainer(decoder *json.Decoder, delimiter json.Delim) error {
	seen := map[string]bool{}
	for decoder.More() {
		if delimiter == '{' {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			text, ok := key.(string)
			if !ok || seen[text] {
				return fmt.Errorf("duplicate or invalid JSON object key %q", text)
			}
			seen[text] = true
		}
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if nested, ok := value.(json.Delim); ok {
			if nested != '{' && nested != '[' {
				return fmt.Errorf("unexpected JSON delimiter")
			}
			if err := validateJSONContainer(decoder, nested); err != nil {
				return err
			}
		}
	}
	_, err := decoder.Token()
	return err
}
