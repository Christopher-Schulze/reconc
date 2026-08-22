package actioninspect

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"reconc.dev/reconc/internal/action"
)

func DecodeMCPToolResult(raw []byte, protocolVersion string) (*MCPToolResult, error) {
	if protocolVersion != ProtocolCurrent && protocolVersion != ProtocolLegacy {
		return nil, fmt.Errorf("%w: protocol version is unsupported", ErrMalformedResult)
	}
	root, err := action.ParseObjectJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrMalformedResult, action.JSONErrorKindOf(err))
	}
	if err := exactObject(root, "_meta", "resultType", "content", "structuredContent", "isError"); err != nil {
		return nil, err
	}
	result := &MCPToolResult{Root: root, ResultType: "complete"}
	if err := decodeResultHeader(result, root, protocolVersion); err != nil {
		return nil, err
	}
	content, ok := root.Lookup("content")
	if !ok {
		return nil, malformed("content is required")
	}
	itemCount, ok := content.ArrayLen()
	if !ok || itemCount > MaxMCPContentBlocks {
		return nil, malformed("content must be a bounded array")
	}
	result.Content = make([]ContentBlock, itemCount)
	for index := 0; index < itemCount; index++ {
		item, _ := content.ArrayItem(index)
		block, err := decodeContentBlock(item, index)
		if err != nil {
			result.Release()
			return nil, err
		}
		result.Content[index] = block
	}
	result.AnnotationFields = collectAnnotationFields(content)
	result.MetadataPointers = collectMetadataPointers(root, content)
	return result, nil
}

func collectAnnotationFields(content action.Value) []string {
	seen := make(map[string]struct{})
	length, _ := content.ArrayLen()
	for index := 0; index < length; index++ {
		item, _ := content.ArrayItem(index)
		annotations, ok := item.Lookup("annotations")
		if !ok {
			continue
		}
		memberCount, _ := annotations.ObjectLen()
		for memberIndex := 0; memberIndex < memberCount; memberIndex++ {
			member, _ := annotations.ObjectMember(memberIndex)
			seen[member.Name] = struct{}{}
		}
	}
	fields := make([]string, 0, len(seen))
	for field := range seen {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

func collectMetadataPointers(root action.Value, content action.Value) []string {
	length, _ := content.ArrayLen()
	pointers := make([]string, 0, length+1)
	if _, ok := root.Lookup("_meta"); ok {
		pointers = append(pointers, "/_meta")
	}
	for index := 0; index < length; index++ {
		item, _ := content.ArrayItem(index)
		base := "/content/" + strconv.Itoa(index)
		if _, ok := item.Lookup("_meta"); ok {
			pointers = append(pointers, base+"/_meta")
		}
		resource, ok := item.Lookup("resource")
		if !ok {
			continue
		}
		if _, ok := resource.Lookup("_meta"); ok {
			pointers = append(pointers, base+"/resource/_meta")
		}
	}
	sort.Strings(pointers)
	return pointers
}

func decodeResultHeader(result *MCPToolResult, root action.Value, protocolVersion string) error {
	if meta, ok := root.Lookup("_meta"); ok {
		if err := validateMeta(meta); err != nil {
			return err
		}
	}
	if value, ok := root.Lookup("resultType"); ok {
		resultType, isString := value.Text()
		if !isString || resultType == "" {
			return malformed("resultType must be a non-empty string")
		}
		result.ResultType = resultType
	} else if protocolVersion == ProtocolCurrent {
		return malformed("resultType is required by the negotiated protocol")
	}
	if result.ResultType != "complete" {
		return fmt.Errorf("%w: resultType is not complete", ErrUnsupportedResultType)
	}
	if value, ok := root.Lookup("structuredContent"); ok {
		result.StructuredContent = value
		result.HasStructuredContent = true
	}
	if value, ok := root.Lookup("isError"); ok {
		isError, isBool := value.Bool()
		if !isBool {
			return malformed("isError must be boolean")
		}
		result.IsError = isError
	}
	return nil
}

func decodeContentBlock(value action.Value, index int) (ContentBlock, error) {
	typeValue, ok := value.Lookup("type")
	if !ok {
		return ContentBlock{}, malformed("content block type is required")
	}
	typeName, ok := typeValue.Text()
	if !ok || typeName == "" {
		return ContentBlock{}, malformed("content block type must be a non-empty string")
	}
	pointer := "/content/" + strconv.Itoa(index)
	switch typeName {
	case "text":
		return decodeTextBlock(value, pointer)
	case "image":
		return decodeBinaryBlock(value, pointer, action.ContentImage, "data")
	case "audio":
		return decodeBinaryBlock(value, pointer, action.ContentAudio, "data")
	case "resource":
		return decodeEmbeddedResource(value, pointer)
	case "resource_link":
		return decodeResourceLink(value, pointer)
	default:
		body, err := value.MarshalJSON()
		if err != nil {
			return ContentBlock{}, malformed("unknown content cannot be canonicalized")
		}
		return ContentBlock{Type: action.ContentUnknown, Pointer: pointer, CoveragePointer: pointer, Binary: body}, nil
	}
}

func decodeTextBlock(value action.Value, pointer string) (ContentBlock, error) {
	if err := exactObject(value, "type", "text", "annotations", "_meta"); err != nil {
		return ContentBlock{}, err
	}
	text, err := requiredString(value, "text")
	if err != nil {
		return ContentBlock{}, err
	}
	if err := validateOptionalBlockMetadata(value); err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{
		Type: action.ContentText, Pointer: pointer + "/text", CoveragePointer: pointer + "/text", Text: text,
	}, nil
}

func decodeBinaryBlock(
	value action.Value,
	pointer string,
	contentType action.ContentType,
	field string,
) (ContentBlock, error) {
	if err := exactObject(value, "type", field, "mimeType", "annotations", "_meta"); err != nil {
		return ContentBlock{}, err
	}
	encoded, err := requiredString(value, field)
	if err != nil {
		return ContentBlock{}, err
	}
	mimeType, err := requiredString(value, "mimeType")
	if err != nil || !validMIMEType(mimeType) {
		return ContentBlock{}, malformed("binary content mimeType is invalid")
	}
	decoded, err := decodeBoundedBase64(encoded)
	if err != nil {
		return ContentBlock{}, err
	}
	if err := validateOptionalBlockMetadata(value); err != nil {
		zeroBytes(decoded)
		return ContentBlock{}, err
	}
	return ContentBlock{
		Type: contentType, Pointer: pointer + "/" + field, CoveragePointer: pointer,
		Binary: decoded, MIMEType: mimeType,
	}, nil
}

func decodeEmbeddedResource(value action.Value, pointer string) (ContentBlock, error) {
	if err := exactObject(value, "type", "resource", "annotations", "_meta"); err != nil {
		return ContentBlock{}, err
	}
	if err := validateOptionalBlockMetadata(value); err != nil {
		return ContentBlock{}, err
	}
	resource, ok := value.Lookup("resource")
	if !ok {
		return ContentBlock{}, malformed("embedded resource is required")
	}
	return decodeResourceContents(resource, pointer+"/resource")
}

func decodeResourceContents(value action.Value, pointer string) (ContentBlock, error) {
	if err := exactObject(value, "uri", "mimeType", "_meta", "text", "blob"); err != nil {
		return ContentBlock{}, err
	}
	uri, err := requiredString(value, "uri")
	if err != nil || !validURI(uri) {
		return ContentBlock{}, malformed("embedded resource uri is invalid")
	}
	if err := validateOptionalMeta(value); err != nil {
		return ContentBlock{}, err
	}
	mimeType, err := optionalMIMEType(value)
	if err != nil {
		return ContentBlock{}, err
	}
	textValue, hasText := value.Lookup("text")
	blobValue, hasBlob := value.Lookup("blob")
	if hasText == hasBlob {
		return ContentBlock{}, malformed("embedded resource must contain exactly one of text or blob")
	}
	if hasText {
		text, ok := textValue.Text()
		if !ok {
			return ContentBlock{}, malformed("embedded resource text must be a string")
		}
		return ContentBlock{
			Type: action.ContentResourceText, Pointer: pointer + "/text", CoveragePointer: pointer,
			Text: text, MIMEType: mimeType,
		}, nil
	}
	encoded, ok := blobValue.Text()
	if !ok {
		return ContentBlock{}, malformed("embedded resource blob must be a string")
	}
	decoded, err := decodeBoundedBase64(encoded)
	if err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{
		Type: action.ContentResourceBlob, Pointer: pointer + "/blob", CoveragePointer: pointer,
		Binary: decoded, MIMEType: mimeType,
	}, nil
}

func decodeResourceLink(value action.Value, pointer string) (ContentBlock, error) {
	if err := exactObject(value, "type", "uri", "name", "title", "description", "mimeType", "annotations", "size", "_meta", "icons"); err != nil {
		return ContentBlock{}, err
	}
	uri, err := requiredString(value, "uri")
	if err != nil || !validURI(uri) {
		return ContentBlock{}, malformed("resource link uri is invalid")
	}
	name, err := requiredString(value, "name")
	if err != nil || strings.TrimSpace(name) == "" {
		return ContentBlock{}, malformed("resource link name is invalid")
	}
	if err := validateResourceLinkFields(value); err != nil {
		return ContentBlock{}, err
	}
	return ContentBlock{Type: action.ContentResourceLink, Pointer: pointer, CoveragePointer: pointer}, nil
}

func validateResourceLinkFields(value action.Value) error {
	for _, field := range []string{"title", "description"} {
		if item, ok := value.Lookup(field); ok {
			if _, stringValue := item.Text(); !stringValue {
				return malformed("resource link text metadata is invalid")
			}
		}
	}
	if _, err := optionalMIMEType(value); err != nil {
		return err
	}
	if size, ok := value.Lookup("size"); ok && !nonnegativeInteger(size) {
		return malformed("resource link size must be a non-negative integer")
	}
	if icons, ok := value.Lookup("icons"); ok {
		if err := validateIcons(icons); err != nil {
			return err
		}
	}
	return validateOptionalBlockMetadata(value)
}

func validateIcons(value action.Value) error {
	length, ok := value.ArrayLen()
	if !ok || length > action.MaxListValues {
		return malformed("resource link icons must be a bounded array")
	}
	for index := 0; index < length; index++ {
		icon, _ := value.ArrayItem(index)
		if err := exactObject(icon, "src", "mimeType", "sizes", "theme"); err != nil {
			return err
		}
		src, err := requiredString(icon, "src")
		if err != nil || !validURI(src) {
			return malformed("resource icon src is invalid")
		}
		if err := validateIconFields(icon); err != nil {
			return err
		}
	}
	return nil
}

func validateIconFields(icon action.Value) error {
	if _, err := optionalMIMEType(icon); err != nil {
		return err
	}
	if theme, ok := icon.Lookup("theme"); ok {
		value, isString := theme.Text()
		if !isString || value != "light" && value != "dark" {
			return malformed("resource icon theme is invalid")
		}
	}
	if sizes, ok := icon.Lookup("sizes"); ok {
		length, isArray := sizes.ArrayLen()
		if !isArray || length > action.MaxListValues {
			return malformed("resource icon sizes are invalid")
		}
		for index := 0; index < length; index++ {
			item, _ := sizes.ArrayItem(index)
			if value, isString := item.Text(); !isString || !validIconSize(value) {
				return malformed("resource icon size is invalid")
			}
		}
	}
	return nil
}

func validateOptionalBlockMetadata(value action.Value) error {
	if annotations, ok := value.Lookup("annotations"); ok {
		if err := validateAnnotations(annotations); err != nil {
			return err
		}
	}
	return validateOptionalMeta(value)
}

func validateOptionalMeta(value action.Value) error {
	if meta, ok := value.Lookup("_meta"); ok {
		return validateMeta(meta)
	}
	return nil
}

func validateMeta(value action.Value) error {
	if value.Kind() != action.ValueObject {
		return malformed("_meta must be an object")
	}
	body, err := value.MarshalJSON()
	if err != nil || len(body) > MaxMCPMetadataBytes {
		return malformed("_meta exceeds its boundary")
	}
	return nil
}

func validateAnnotations(value action.Value) error {
	if err := exactObject(value, "audience", "priority", "lastModified"); err != nil {
		return err
	}
	if audience, ok := value.Lookup("audience"); ok {
		if err := validateAudience(audience); err != nil {
			return err
		}
	}
	if priority, ok := value.Lookup("priority"); ok {
		if !decimalBetweenZeroAndOne(priority) {
			return malformed("annotation priority is outside 0..1")
		}
	}
	if modified, ok := value.Lookup("lastModified"); ok {
		stamp, isString := modified.Text()
		if !isString {
			return malformed("annotation lastModified must be a string")
		}
		if _, err := time.Parse(time.RFC3339, stamp); err != nil {
			return malformed("annotation lastModified is not RFC 3339")
		}
	}
	return nil
}

func validateAudience(value action.Value) error {
	length, ok := value.ArrayLen()
	if !ok || length > 2 {
		return malformed("annotation audience is invalid")
	}
	seen := make(map[string]struct{}, length)
	for index := 0; index < length; index++ {
		item, _ := value.ArrayItem(index)
		role, isString := item.Text()
		if !isString || role != "user" && role != "assistant" {
			return malformed("annotation audience role is invalid")
		}
		if _, duplicate := seen[role]; duplicate {
			return malformed("annotation audience role is duplicated")
		}
		seen[role] = struct{}{}
	}
	return nil
}

func exactObject(value action.Value, allowed ...string) error {
	length, ok := value.ObjectLen()
	if !ok {
		return malformed("content value must be an object")
	}
	known := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		known[name] = struct{}{}
	}
	for index := 0; index < length; index++ {
		member, _ := value.ObjectMember(index)
		if _, ok := known[member.Name]; !ok {
			return malformed("content object contains an unknown field")
		}
	}
	return nil
}

func requiredString(value action.Value, field string) (string, error) {
	item, ok := value.Lookup(field)
	if !ok {
		return "", malformed(field + " is required")
	}
	text, ok := item.Text()
	if !ok {
		return "", malformed(field + " must be a string")
	}
	return text, nil
}

func optionalMIMEType(value action.Value) (string, error) {
	item, ok := value.Lookup("mimeType")
	if !ok {
		return "", nil
	}
	mimeType, isString := item.Text()
	if !isString || !validMIMEType(mimeType) {
		return "", malformed("mimeType is invalid")
	}
	return mimeType, nil
}

func decodeBoundedBase64(value string) ([]byte, error) {
	if len(value) > base64.StdEncoding.EncodedLen(MaxMCPBinaryDecodedBytes) {
		return nil, malformed("binary content exceeds its decoded byte boundary")
	}
	if strings.ContainsAny(value, "\r\n") {
		return nil, malformed("binary content is not canonical bounded base64")
	}
	decoded := make([]byte, base64.StdEncoding.DecodedLen(len(value)))
	written, err := base64.StdEncoding.Strict().Decode(decoded, []byte(value))
	if err != nil || written > MaxMCPBinaryDecodedBytes {
		zeroBytes(decoded)
		return nil, malformed("binary content is not canonical bounded base64")
	}
	return decoded[:written], nil
}

func decimalBetweenZeroAndOne(value action.Value) bool {
	decimal, ok := value.Decimal()
	if !ok {
		return false
	}
	zero, _ := action.ParseDecimal("0")
	one, _ := action.ParseDecimal("1")
	return decimal.Compare(zero) >= 0 && decimal.Compare(one) <= 0
}

func validURI(value string) bool {
	if value == "" || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme != ""
}

func validMIMEType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return false
	}
	parts := strings.Split(mediaType, "/")
	return len(parts) == 2 && validMIMEToken(parts[0]) && validMIMEToken(parts[1])
}

func validMIMEToken(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character <= 0x20 || character >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?=", character) {
			return false
		}
	}
	return true
}

func validIconSize(value string) bool {
	if value == "any" {
		return true
	}
	width, height, ok := strings.Cut(value, "x")
	if !ok || width == "" || height == "" {
		return false
	}
	parsedWidth, widthErr := strconv.ParseUint(width, 10, 31)
	parsedHeight, heightErr := strconv.ParseUint(height, 10, 31)
	return widthErr == nil && heightErr == nil && parsedWidth > 0 && parsedHeight > 0
}

func nonnegativeInteger(value action.Value) bool {
	decimal, ok := value.Decimal()
	if !ok {
		return false
	}
	zero, _ := action.ParseDecimal("0")
	return decimal.Compare(zero) >= 0 && !strings.Contains(decimal.String(), "e-")
}

func malformed(message string) error {
	return fmt.Errorf("%w: %s", ErrMalformedResult, message)
}

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

func IsMalformedResult(err error) bool {
	return errors.Is(err, ErrMalformedResult)
}
