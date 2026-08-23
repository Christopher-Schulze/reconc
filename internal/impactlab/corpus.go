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
	"reflect"
	"sort"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/actioninspect"
	"reconc.dev/reconc/internal/boundedio"
	"reconc.dev/reconc/internal/runtime"
)

// NewCorpus normalizes and sanitizes explicit evidence using the production
// runtime input contract, then binds the result to a deterministic identity.
func NewCorpus(repoRoot string, cases []Case, complete []EventClass) (Corpus, error) {
	return NewCorpusWithActionCoverage(repoRoot, cases, complete, emptyActionDimensions())
}

// NewCorpusWithActionCoverage adds explicit action coverage requirements. A
// requirement is complete only when the sanitized cases actually represent it.
func NewCorpusWithActionCoverage(repoRoot string, cases []Case, complete []EventClass, required ActionDimensions) (Corpus, error) {
	if len(cases) == 0 || len(cases) > maxCases {
		return Corpus{}, fmt.Errorf("impact corpus must contain 1..%d cases", maxCases)
	}
	for _, class := range complete {
		if !eventClassValid(class) {
			return Corpus{}, fmt.Errorf("unsupported impact event class %q", class)
		}
	}
	var err error
	required, err = normalizeActionDimensions(required)
	if err != nil {
		return Corpus{}, err
	}
	scanner, err := actionPrivacyScanner(cases)
	if err != nil {
		return Corpus{}, err
	}
	sanitized := make([]Case, 0, len(cases))
	for _, replayCase := range cases {
		cleaned, err := sanitizeCase(repoRoot, scanner, replayCase)
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
	corpus.Completeness = buildCompleteness(sanitized, canonicalEventClasses(complete), required)
	corpus.CorpusID, err = corpusIdentity(corpus)
	if err != nil {
		return Corpus{}, err
	}
	if err := validateCorpus(corpus); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

// NewRepositoryCase constructs one unsanitized repository fixture for
// NewCorpus. The constructor prevents an ambiguous zero discriminant.
func NewRepositoryCase(id string, inputs runtime.ExecutionInputs) Case {
	return Case{ID: id, Kind: CaseRepository, Repository: &RepositoryCase{Inputs: inputs}}
}

// MergeCorpora combines fixtures independent of argument order. A class is
// complete only when every source corpus declares complete coverage for it.
func MergeCorpora(corpora []Corpus) (Corpus, error) {
	if len(corpora) == 0 {
		return Corpus{}, fmt.Errorf("at least one impact corpus is required")
	}
	cases := []Case{}
	complete := AllEventClasses()
	required := emptyActionDimensions()
	for _, corpus := range corpora {
		if err := validateCorpus(corpus); err != nil {
			return Corpus{}, err
		}
		cases = append(cases, corpus.Cases...)
		complete = eventClassIntersection(complete, corpus.Completeness.CompleteEventClasses)
		required = mergeActionDimensions(required, corpus.Completeness.Action.Required)
	}
	if len(cases) > maxCases {
		return Corpus{}, fmt.Errorf("merged impact corpus exceeds %d cases", maxCases)
	}
	sort.Slice(cases, func(left, right int) bool { return cases[left].ID < cases[right].ID })
	if err := rejectDuplicateCaseIDs(cases); err != nil {
		return Corpus{}, err
	}
	merged := Corpus{FormatVersion: CorpusFormatVersion, Cases: cases}
	merged.Completeness = buildCompleteness(cases, complete, required)
	var err error
	merged.CorpusID, err = corpusIdentity(merged)
	if err != nil {
		return Corpus{}, err
	}
	return merged, validateCorpus(merged)
}

// DecodeCorpusFile reads one bounded regular non-symlink corpus.
func DecodeCorpusFile(filePath string) (Corpus, error) {
	if info, err := os.Lstat(filePath); err != nil {
		return Corpus{}, err
	} else if !info.Mode().IsRegular() {
		return Corpus{}, fmt.Errorf("impact corpus %s must be a regular file and not a symlink", filePath)
	}
	body, _, err := boundedio.ReadRegularFileSnapshot(filePath, MaxCorpusBytes)
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
	var header struct {
		FormatVersion string `json:"format_version"`
	}
	if err := json.Unmarshal(body, &header); err != nil {
		return Corpus{}, fmt.Errorf("decode impact corpus header: %w", err)
	}
	if header.FormatVersion == LegacyCorpusFormatVersion {
		return migrateLegacyCorpus(body)
	}
	if header.FormatVersion != CorpusFormatVersion {
		return Corpus{}, fmt.Errorf("unsupported impact corpus format %q", header.FormatVersion)
	}
	if err := validateExactJSONFields(body, reflect.TypeOf(Corpus{})); err != nil {
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
	if err := validateCorpusContract(corpus); err != nil {
		return nil, err
	}
	return marshalCorpusJSON(corpus)
}

func marshalCorpusJSON(corpus Corpus) ([]byte, error) {
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
	if err := validateCorpusContract(corpus); err != nil {
		return err
	}
	_, err := marshalCorpusJSON(corpus)
	return err
}

func validateCorpusContract(corpus Corpus) error {
	if corpus.FormatVersion != CorpusFormatVersion || corpus.CorpusID == "" {
		return fmt.Errorf("unsupported or incomplete impact corpus contract")
	}
	if len(corpus.Cases) == 0 || len(corpus.Cases) > maxCases {
		return fmt.Errorf("impact corpus must contain 1..%d cases", maxCases)
	}
	if corpus.Cases == nil || corpus.Completeness.ObservedEventClasses == nil ||
		corpus.Completeness.CompleteEventClasses == nil || corpus.Completeness.MissingEventClasses == nil ||
		corpus.Completeness.RedactedEventClasses == nil ||
		!actionDimensionsNonNil(corpus.Completeness.Action.Observed) ||
		!actionDimensionsNonNil(corpus.Completeness.Action.Required) ||
		!actionDimensionsNonNil(corpus.Completeness.Action.Missing) {
		return fmt.Errorf("impact corpus required collections must not be null")
	}
	identity, err := corpusIdentity(corpus)
	if err != nil {
		return fmt.Errorf("encode impact corpus identity: %w", err)
	}
	if corpus.CorpusID != identity {
		return fmt.Errorf("impact corpus identity does not match its contents")
	}
	if err := validateCases(corpus.Cases); err != nil {
		return err
	}
	if err := validateCompleteness(corpus); err != nil {
		return err
	}
	return nil
}

func validateCases(cases []Case) error {
	scanner, err := actionPrivacyScanner(cases)
	if err != nil {
		return err
	}
	totalItems := 0
	for index, replayCase := range cases {
		if !validCaseID(replayCase.ID) || !replayCase.Kind.Valid() {
			return fmt.Errorf("impact corpus case[%d] has invalid identity or kind", index)
		}
		if index > 0 && cases[index-1].ID >= replayCase.ID {
			return fmt.Errorf("impact corpus case ids must be unique and lexically sorted")
		}
		items, err := validateCase(scanner, replayCase)
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

func validateCase(scanner *actioninspect.TextScanner, replayCase Case) (int, error) {
	switch replayCase.Kind {
	case CaseRepository:
		if replayCase.Repository == nil || replayCase.Action != nil {
			return 0, fmt.Errorf("repository case must contain only repository evidence")
		}
		return validateRepositoryCase(*replayCase.Repository)
	case CaseActionPre, CaseActionPost:
		if replayCase.Action == nil || replayCase.Repository != nil {
			return 0, fmt.Errorf("action case must contain only action evidence")
		}
		return validateActionCaseWithScanner(scanner, replayCase.Kind, *replayCase.Action)
	default:
		return 0, fmt.Errorf("unsupported impact case kind %q", replayCase.Kind)
	}
}

func actionPrivacyScanner(cases []Case) (*actioninspect.TextScanner, error) {
	for _, replayCase := range cases {
		if replayCase.Kind != CaseActionPre && replayCase.Kind != CaseActionPost {
			continue
		}
		scanner, err := actioninspect.NewTextScanner()
		if err != nil {
			return nil, fmt.Errorf("prepare action privacy scanner: %w", err)
		}
		return scanner, nil
	}
	return nil, nil
}

func validateRepositoryCase(replayCase RepositoryCase) (int, error) {
	inputs := replayCase.Inputs
	if inputs.ReadPaths == nil || inputs.WritePaths == nil || inputs.Commands == nil ||
		inputs.Claims == nil || inputs.CommandResults == nil || replayCase.RedactedEventClasses == nil ||
		replayCase.RedactionCount < 0 {
		return 0, fmt.Errorf("repository case contains a null collection or invalid redaction count")
	}
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
	if value == "" || len(value) > maxCaseIDBytes || unsafeActionMetadata(value) {
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
	dimensions := []struct {
		name  string
		value ActionDimensions
	}{
		{name: "observed", value: corpus.Completeness.Action.Observed},
		{name: "required", value: corpus.Completeness.Action.Required},
		{name: "missing", value: corpus.Completeness.Action.Missing},
	}
	for _, dimension := range dimensions {
		normalized, err := normalizeActionDimensions(dimension.value)
		if err != nil || !equalActionDimensions(normalized, dimension.value) {
			return fmt.Errorf("impact corpus %s action coverage is invalid or non-canonical", dimension.name)
		}
	}
	want := buildCompleteness(corpus.Cases, corpus.Completeness.CompleteEventClasses, corpus.Completeness.Action.Required)
	if !equalEventClasses(want.ObservedEventClasses, corpus.Completeness.ObservedEventClasses) ||
		!equalEventClasses(want.CompleteEventClasses, corpus.Completeness.CompleteEventClasses) ||
		!equalEventClasses(want.MissingEventClasses, corpus.Completeness.MissingEventClasses) ||
		!equalEventClasses(want.RedactedEventClasses, corpus.Completeness.RedactedEventClasses) ||
		want.RedactionCount != corpus.Completeness.RedactionCount ||
		!equalActionCoverage(want.Action, corpus.Completeness.Action) ||
		want.CompleteReplay != corpus.Completeness.CompleteReplay {
		return fmt.Errorf("impact corpus completeness metadata is inconsistent")
	}
	return nil
}

func caseIdentity(replayCase Case) (string, error) {
	body, err := json.Marshal(replayCase)
	if err != nil {
		return "", fmt.Errorf("encode impact case identity: %w", err)
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func corpusIdentity(corpus Corpus) (string, error) {
	corpus.CorpusID = ""
	body, err := json.Marshal(corpus)
	if err != nil {
		return "", fmt.Errorf("encode impact corpus identity: %w", err)
	}
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
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
		return fmt.Errorf("impact JSON must contain an object")
	}
	if err := validateJSONContainer(decoder, '{'); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("impact JSON must contain exactly one value")
	}
	return nil
}

var rawMessageType = reflect.TypeOf(json.RawMessage{})

func validateExactJSONFields(body []byte, target reflect.Type) error {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := validateExactJSONValue(decoder, target, "root"); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return fmt.Errorf("impact JSON must contain exactly one value")
	}
	return nil
}

func validateExactJSONValue(decoder *json.Decoder, target reflect.Type, location string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if target == rawMessageType {
		return consumeJSONToken(decoder, token)
	}
	delimiter, nested := token.(json.Delim)
	switch target.Kind() {
	case reflect.Struct:
		return validateExactJSONObject(decoder, target, location, delimiter, nested)
	case reflect.Slice, reflect.Array:
		return validateExactJSONArray(decoder, target.Elem(), location, delimiter, nested)
	case reflect.Map:
		return validateExactJSONMap(decoder, target, location, delimiter, nested)
	case reflect.Interface:
		return consumeJSONToken(decoder, token)
	default:
		if nested {
			return fmt.Errorf("impact JSON %s has an invalid container value", location)
		}
		if !exactJSONScalarType(token, target.Kind()) {
			return fmt.Errorf("impact JSON %s has an invalid scalar type", location)
		}
		return nil
	}
}

func validateExactJSONObject(decoder *json.Decoder, target reflect.Type, location string, delimiter json.Delim, nested bool) error {
	if !nested || delimiter != '{' {
		return fmt.Errorf("impact JSON %s must be an object", location)
	}
	fields := exactJSONStructFields(target)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		fieldType, known := fields[name]
		if !ok || !known {
			return fmt.Errorf("impact JSON %s contains an unknown or incorrectly cased field", location)
		}
		if err := validateExactJSONValue(decoder, fieldType, location+"."+name); err != nil {
			return err
		}
	}
	return consumeJSONClose(decoder, '}')
}

func validateExactJSONArray(decoder *json.Decoder, element reflect.Type, location string, delimiter json.Delim, nested bool) error {
	if !nested || delimiter != '[' {
		return fmt.Errorf("impact JSON %s must be an array", location)
	}
	for decoder.More() {
		if err := validateExactJSONValue(decoder, element, location+"[]"); err != nil {
			return err
		}
	}
	return consumeJSONClose(decoder, ']')
}

func validateExactJSONMap(decoder *json.Decoder, target reflect.Type, location string, delimiter json.Delim, nested bool) error {
	if !nested || delimiter != '{' || target.Key().Kind() != reflect.String {
		return fmt.Errorf("impact JSON %s must be a string-keyed object", location)
	}
	for decoder.More() {
		if _, err := decoder.Token(); err != nil {
			return err
		}
		if err := validateExactJSONValue(decoder, target.Elem(), location+"{}"); err != nil {
			return err
		}
	}
	return consumeJSONClose(decoder, '}')
}

func exactJSONScalarType(token json.Token, kind reflect.Kind) bool {
	switch kind {
	case reflect.String:
		_, ok := token.(string)
		return ok
	case reflect.Bool:
		_, ok := token.(bool)
		return ok
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		_, ok := token.(json.Number)
		return ok
	default:
		return token != nil
	}
}

func exactJSONStructFields(target reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, target.NumField())
	for index := 0; index < target.NumField(); index++ {
		field := target.Field(index)
		if field.PkgPath != "" {
			continue
		}
		tag := field.Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if field.Anonymous && name == "" {
			embedded := field.Type
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				for embeddedName, embeddedType := range exactJSONStructFields(embedded) {
					fields[embeddedName] = embeddedType
				}
				continue
			}
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

func consumeJSONToken(decoder *json.Decoder, token json.Token) error {
	delimiter, nested := token.(json.Delim)
	if !nested {
		return nil
	}
	for decoder.More() {
		if delimiter == '{' {
			if _, err := decoder.Token(); err != nil {
				return err
			}
		}
		value, err := decoder.Token()
		if err != nil {
			return err
		}
		if err := consumeJSONToken(decoder, value); err != nil {
			return err
		}
	}
	close := json.Delim(']')
	if delimiter == '{' {
		close = '}'
	}
	return consumeJSONClose(decoder, close)
}

func consumeJSONClose(decoder *json.Decoder, expected json.Delim) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != expected {
		return fmt.Errorf("impact JSON container is not closed correctly")
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
