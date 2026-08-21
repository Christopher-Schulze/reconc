package mcpgateway

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
	"reconc.dev/reconc/internal/actioninspect"
)

var gatewayToolName = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,128}$`)
var gatewayIconSize = regexp.MustCompile(`^([1-9][0-9]{0,4})x([1-9][0-9]{0,4})$`)

const (
	maxIconDimension = 2048
	maxIconPixels    = 4 << 20
)

type catalogValidator struct {
	pageCount    int
	toolCount    int
	catalogBytes int
	contracts    []ToolContract
	seen         map[string]struct{}
	schemas      map[[sha256.Size]byte]*actioninspect.OutputSchema
	scanner      *actioninspect.TextScanner
}

func newCatalogValidator() (*catalogValidator, error) {
	scanner, err := actioninspect.NewTextScanner()
	if err != nil {
		return nil, fmt.Errorf("initialize tool metadata inspection: %w", err)
	}
	return &catalogValidator{
		contracts: make([]ToolContract, 0),
		seen:      make(map[string]struct{}),
		schemas:   make(map[[sha256.Size]byte]*actioninspect.OutputSchema),
		scanner:   scanner,
	}, nil
}

func (v *catalogValidator) addPage(ctx context.Context, page ToolPage) error {
	v.pageCount++
	if v.pageCount > MaxToolPages {
		return fmt.Errorf("downstream tool catalog page count is invalid")
	}
	if len(page.Tools) > MaxToolsPerPage {
		return fmt.Errorf("downstream tool page %d exceeds %d tools", v.pageCount, MaxToolsPerPage)
	}
	for _, raw := range page.Tools {
		if v.toolCount >= MaxTools || len(raw) > MaxCatalogBytes-v.catalogBytes {
			return fmt.Errorf("downstream tool catalog exceeds its boundary")
		}
		v.toolCount++
		v.catalogBytes += len(raw)
		contract, err := validateToolContractWithCache(ctx, raw, v.schemas, v.scanner)
		if err != nil {
			return fmt.Errorf("validate downstream tool %d: %w", v.toolCount, err)
		}
		if extra := len(contract.Canonical) - len(raw); extra > 0 {
			if extra > MaxCatalogBytes-v.catalogBytes {
				return fmt.Errorf("downstream tool catalog exceeds its boundary")
			}
			v.catalogBytes += extra
		}
		if _, duplicate := v.seen[contract.Name]; duplicate {
			return fmt.Errorf("downstream tool name %q is duplicated", contract.Name)
		}
		v.seen[contract.Name] = struct{}{}
		v.contracts = append(v.contracts, contract)
	}
	return nil
}

func (v *catalogValidator) finish() ([]ToolContract, error) {
	if v.pageCount == 0 {
		return nil, fmt.Errorf("downstream tool catalog page count is invalid")
	}
	sort.Slice(v.contracts, func(i, j int) bool { return v.contracts[i].Name < v.contracts[j].Name })
	return v.contracts, nil
}

func validateCatalog(ctx context.Context, pages []ToolPage) ([]ToolContract, error) {
	validator, err := newCatalogValidator()
	if err != nil {
		return nil, err
	}
	for _, page := range pages {
		if err := validator.addPage(ctx, page); err != nil {
			return nil, err
		}
	}
	return validator.finish()
}

func validateToolContract(ctx context.Context, raw []byte) (ToolContract, error) {
	scanner, err := actioninspect.NewTextScanner()
	if err != nil {
		return ToolContract{}, fmt.Errorf("initialize tool metadata inspection: %w", err)
	}
	return validateToolContractWithCache(ctx, raw, nil, scanner)
}

func validateToolContractWithCache(
	ctx context.Context,
	raw []byte,
	schemas map[[sha256.Size]byte]*actioninspect.OutputSchema,
	scanner *actioninspect.TextScanner,
) (ToolContract, error) {
	if len(raw) == 0 || len(raw) > MaxToolMetadataBytes {
		return ToolContract{}, fmt.Errorf("tool metadata must contain 1 to %d bytes", MaxToolMetadataBytes)
	}
	value, err := action.ParseObjectJSON(raw)
	if err != nil {
		return ToolContract{}, fmt.Errorf("decode strict tool metadata: %s", action.JSONErrorKindOf(err))
	}
	if err := exactToolFields(value); err != nil {
		return ToolContract{}, err
	}
	if err := validateToolMetadataFields(value); err != nil {
		return ToolContract{}, err
	}
	value, err = normalizeToolAnnotations(value)
	if err != nil {
		return ToolContract{}, err
	}
	nameValue, ok := value.Lookup("name")
	if !ok {
		return ToolContract{}, fmt.Errorf("tool name is required")
	}
	name, ok := nameValue.Text()
	if !ok || !gatewayToolName.MatchString(name) || len(name) > action.MaxGatewayToolNameBytes {
		return ToolContract{}, fmt.Errorf("tool name is outside the gateway contract")
	}
	inputValue, ok := value.Lookup("inputSchema")
	if !ok || inputValue.Kind() != action.ValueObject {
		return ToolContract{}, fmt.Errorf("tool inputSchema must be an object")
	}
	inputBody, err := inputValue.MarshalJSON()
	if err != nil {
		return ToolContract{}, fmt.Errorf("canonicalize input schema: %w", err)
	}
	inputSchema, err := compileToolSchema(inputBody, schemas)
	if err != nil {
		return ToolContract{}, fmt.Errorf("compile input schema: %w", err)
	}
	if err := requireObjectInputSchema(inputValue); err != nil {
		return ToolContract{}, err
	}
	var outputSchema *actioninspect.OutputSchema
	if outputValue, exists := value.Lookup("outputSchema"); exists {
		if outputValue.Kind() != action.ValueObject {
			return ToolContract{}, fmt.Errorf("tool outputSchema must be an object")
		}
		outputBody, encodeErr := outputValue.MarshalJSON()
		if encodeErr != nil {
			return ToolContract{}, fmt.Errorf("canonicalize output schema: %w", encodeErr)
		}
		outputSchema, err = compileToolSchema(outputBody, schemas)
		if err != nil {
			return ToolContract{}, fmt.Errorf("compile output schema: %w", err)
		}
	}
	if err := validateToolIcons(value); err != nil {
		return ToolContract{}, err
	}
	if err := inspectToolText(ctx, scanner, value); err != nil {
		return ToolContract{}, err
	}
	canonical, err := value.MarshalJSON()
	if err != nil {
		return ToolContract{}, fmt.Errorf("canonicalize tool metadata: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return ToolContract{
		Name: name, Canonical: canonical,
		ContractDigest: "sha256:" + hex.EncodeToString(digest[:]),
		InputSchema:    inputSchema, OutputSchema: outputSchema,
	}, nil
}

func compileToolSchema(
	body []byte,
	cache map[[sha256.Size]byte]*actioninspect.OutputSchema,
) (*actioninspect.OutputSchema, error) {
	digest := sha256.Sum256(body)
	if schema := cache[digest]; schema != nil {
		return schema, nil
	}
	schema, err := actioninspect.CompileOutputSchema(body)
	if err != nil {
		return nil, err
	}
	if cache != nil {
		cache[digest] = schema
	}
	return schema, nil
}

func normalizeToolAnnotations(tool action.Value) (action.Value, error) {
	annotations, exists := tool.Lookup("annotations")
	if !exists {
		return tool, nil
	}
	members, _ := annotations.Members()
	present := make(map[string]struct{}, len(members))
	for _, member := range members {
		present[member.Name] = struct{}{}
	}
	for _, name := range []string{"idempotentHint", "readOnlyHint"} {
		if _, exists := present[name]; !exists {
			members = append(members, action.Member{Name: name, Value: action.Boolean(false)})
		}
	}
	normalized, err := action.Object(members)
	if err != nil {
		return action.Value{}, fmt.Errorf("normalize tool annotations: %w", err)
	}
	toolMembers, _ := tool.Members()
	for index := range toolMembers {
		if toolMembers[index].Name == "annotations" {
			toolMembers[index].Value = normalized
			break
		}
	}
	return action.Object(toolMembers)
}

func validateToolMetadataFields(tool action.Value) error {
	for _, field := range []struct {
		name  string
		limit int
	}{
		{name: "title", limit: MaxToolTitleBytes},
		{name: "description", limit: MaxToolDescriptionBytes},
	} {
		value, exists := tool.Lookup(field.name)
		if !exists {
			continue
		}
		text, ok := value.Text()
		if !ok || len(text) > field.limit {
			return fmt.Errorf("tool %s must be a string of at most %d bytes", field.name, field.limit)
		}
	}
	if metadata, exists := tool.Lookup("_meta"); exists {
		members, ok := metadata.Members()
		if !ok || len(members) != 0 {
			return fmt.Errorf("tool _meta extensions are unsupported")
		}
	}
	annotations, exists := tool.Lookup("annotations")
	if !exists {
		return nil
	}
	members, ok := annotations.Members()
	if !ok {
		return fmt.Errorf("tool annotations must be an object")
	}
	for _, member := range members {
		switch member.Name {
		case "destructiveHint", "idempotentHint", "openWorldHint", "readOnlyHint":
			if _, ok := member.Value.Bool(); !ok {
				return fmt.Errorf("tool annotation %s must be boolean", member.Name)
			}
		case "title":
			text, ok := member.Value.Text()
			if !ok || len(text) > MaxToolTitleBytes {
				return fmt.Errorf("tool annotation title must be a bounded string")
			}
		default:
			return fmt.Errorf("tool annotation field %q is unsupported", member.Name)
		}
	}
	return nil
}

func exactToolFields(value action.Value) error {
	members, _ := value.Members()
	allowed := map[string]struct{}{
		"_meta": {}, "annotations": {}, "description": {}, "icons": {},
		"inputSchema": {}, "name": {}, "outputSchema": {}, "title": {},
	}
	for _, member := range members {
		if _, ok := allowed[member.Name]; !ok {
			return fmt.Errorf("tool metadata field %q is unsupported", member.Name)
		}
	}
	return nil
}

func requireObjectInputSchema(value action.Value) error {
	typeValue, ok := value.Lookup("type")
	if !ok {
		return fmt.Errorf("tool inputSchema must declare type object")
	}
	typeName, ok := typeValue.Text()
	if !ok || typeName != "object" {
		return fmt.Errorf("tool inputSchema must declare type object")
	}
	return nil
}

func validateToolIcons(tool action.Value) error {
	icons, exists := tool.Lookup("icons")
	if !exists {
		return nil
	}
	items, ok := icons.Items()
	if !ok || len(items) > 32 {
		return fmt.Errorf("tool icons must be a bounded array")
	}
	for _, icon := range items {
		members, ok := icon.Members()
		if !ok {
			return fmt.Errorf("tool icon must be an object")
		}
		for _, member := range members {
			switch member.Name {
			case "src", "mimeType", "sizes", "theme":
			default:
				return fmt.Errorf("tool icon field %q is unsupported", member.Name)
			}
		}
		source, exists := icon.Lookup("src")
		text, isText := source.Text()
		if !exists || !isText {
			return fmt.Errorf("tool icon URI is required")
		}
		sourceMIME, dataURI, width, height, err := validateIconURI(text)
		if err != nil {
			return err
		}
		declaredMIME := ""
		if mimeValue, present := icon.Lookup("mimeType"); present {
			declaredMIME, isText = mimeValue.Text()
			if !isText || !safeRasterMIME(declaredMIME) {
				return fmt.Errorf("tool icon MIME type is unsupported")
			}
		}
		if !dataURI && declaredMIME == "" {
			return fmt.Errorf("tool HTTPS icon requires a safe raster MIME type")
		}
		if dataURI && declaredMIME != "" && canonicalRasterMIME(declaredMIME) != canonicalRasterMIME(sourceMIME) {
			return fmt.Errorf("tool icon MIME type contradicts its data URI")
		}
		if sizes, present := icon.Lookup("sizes"); present {
			if err := validateIconSizes(sizes); err != nil {
				return err
			}
			if dataURI && !iconSizesExact(sizes, width, height) {
				return fmt.Errorf("tool data icon dimensions contradict its declared sizes")
			}
		}
		if theme, present := icon.Lookup("theme"); present {
			themeName, ok := theme.Text()
			if !ok || themeName != "light" && themeName != "dark" {
				return fmt.Errorf("tool icon theme is unsupported")
			}
		}
	}
	return nil
}

func validateIconURI(value string) (string, bool, int, int, error) {
	if value == "" || len(value) > MaxIconURIBytes || !utf8.ValidString(value) {
		return "", false, 0, 0, fmt.Errorf("tool icon URI is outside its byte boundary")
	}
	if !strings.HasPrefix(strings.ToLower(value), "data:") {
		return "", false, 0, 0, fmt.Errorf("tool icon must be a self-contained data raster")
	}
	header, encoded, found := strings.Cut(value, ",")
	lowerHeader := strings.ToLower(header)
	if !found || !strings.HasSuffix(lowerHeader, ";base64") {
		return "", false, 0, 0, fmt.Errorf("tool data icon must be base64 encoded")
	}
	mimeType := strings.TrimSuffix(lowerHeader, ";base64")
	mimeType = strings.TrimPrefix(mimeType, "data:")
	if !safeRasterMIME(mimeType) {
		return "", false, 0, 0, fmt.Errorf("tool data icon MIME type is unsupported")
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil || len(decoded) == 0 || len(decoded) > MaxIconPayloadBytes {
		return "", false, 0, 0, fmt.Errorf("tool data icon payload is invalid")
	}
	width, height, ok := rasterDimensions(mimeType, decoded)
	if !ok || width <= 0 || height <= 0 || width > maxIconDimension || height > maxIconDimension {
		return "", false, 0, 0, fmt.Errorf("tool data icon bytes or dimensions contradict its MIME type")
	}
	return mimeType, true, width, height, nil
}

func safeRasterMIME(value string) bool {
	switch strings.ToLower(value) {
	case "image/png", "image/jpeg", "image/jpg":
		return true
	default:
		return false
	}
}

func canonicalRasterMIME(value string) string {
	if strings.EqualFold(value, "image/jpg") {
		return "image/jpeg"
	}
	return strings.ToLower(value)
}

func matchesRasterMagic(mimeType string, body []byte) bool {
	switch canonicalRasterMIME(mimeType) {
	case "image/png":
		return len(body) >= 8 && bytes.Equal(body[:8], []byte("\x89PNG\r\n\x1a\n"))
	case "image/jpeg":
		return len(body) >= 3 && bytes.Equal(body[:3], []byte{0xff, 0xd8, 0xff})
	default:
		return false
	}
}

func rasterDimensions(mimeType string, body []byte) (int, int, bool) {
	switch canonicalRasterMIME(mimeType) {
	case "image/png", "image/jpeg":
		if !matchesRasterMagic(mimeType, body) {
			return 0, 0, false
		}
		configuration, format, err := image.DecodeConfig(bytes.NewReader(body))
		if err != nil || canonicalRasterMIME("image/"+format) != canonicalRasterMIME(mimeType) {
			return 0, 0, false
		}
		if configuration.Width <= 0 || configuration.Height <= 0 ||
			configuration.Width > maxIconDimension || configuration.Height > maxIconDimension ||
			uint64(configuration.Width)*uint64(configuration.Height) > maxIconPixels {
			return 0, 0, false
		}
		decoded, decodedFormat, err := image.Decode(bytes.NewReader(body))
		if err != nil || decodedFormat != format ||
			decoded.Bounds().Dx() != configuration.Width || decoded.Bounds().Dy() != configuration.Height {
			return 0, 0, false
		}
		return configuration.Width, configuration.Height, true
	default:
		return 0, 0, false
	}
}

func validateIconSizes(value action.Value) error {
	items, ok := value.Items()
	if !ok || len(items) == 0 || len(items) > 16 {
		return fmt.Errorf("tool icon sizes must be a bounded non-empty array")
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		size, ok := item.Text()
		matches := gatewayIconSize.FindStringSubmatch(size)
		if !ok || matches == nil {
			return fmt.Errorf("tool icon size is invalid")
		}
		width, widthErr := strconv.Atoi(matches[1])
		height, heightErr := strconv.Atoi(matches[2])
		if widthErr != nil || heightErr != nil || width > maxIconDimension || height > maxIconDimension {
			return fmt.Errorf("tool icon dimensions exceed their boundary")
		}
		if _, duplicate := seen[size]; duplicate {
			return fmt.Errorf("tool icon size is duplicated")
		}
		seen[size] = struct{}{}
	}
	return nil
}

func iconSizesExact(value action.Value, width, height int) bool {
	items, _ := value.Items()
	if len(items) != 1 {
		return false
	}
	want := strconv.Itoa(width) + "x" + strconv.Itoa(height)
	size, _ := items[0].Text()
	return size == want
}

func inspectToolText(
	ctx context.Context,
	scanner *actioninspect.TextScanner,
	value action.Value,
) error {
	stringsToScan := make([]string, 0)
	collectToolStrings(value, &stringsToScan)
	for _, text := range stringsToScan {
		if text == "" {
			continue
		}
		categories, scanErr := scanner.UntrustedInstructionCategories(ctx, text, uint64(len(text)))
		if scanErr != nil {
			return fmt.Errorf("inspect untrusted tool metadata: %w", scanErr)
		}
		if len(categories) > 0 {
			return fmt.Errorf("untrusted tool metadata matched %s", categories[0])
		}
	}
	return nil
}

func collectToolStrings(value action.Value, output *[]string) {
	switch value.Kind() {
	case action.ValueString:
		text, _ := value.Text()
		*output = append(*output, text)
	case action.ValueArray:
		items, _ := value.Items()
		for _, item := range items {
			collectToolStrings(item, output)
		}
	case action.ValueObject:
		members, _ := value.Members()
		for _, member := range members {
			if member.Name == "src" {
				text, _ := member.Value.Text()
				if strings.HasPrefix(text, "https://") {
					*output = append(*output, text)
				}
				continue
			}
			*output = append(*output, member.Name)
			collectToolStrings(member.Value, output)
		}
	}
}

func toolMap(contracts []ToolContract) map[string]ToolContract {
	values := make(map[string]ToolContract, len(contracts))
	for _, contract := range contracts {
		contract.Canonical = append(json.RawMessage(nil), contract.Canonical...)
		values[contract.Name] = contract
	}
	return values
}
