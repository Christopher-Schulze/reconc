package hooks

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

type tomlSourceRange struct {
	start int
	end   int
}

type codexActivationBlockRange struct {
	span     tomlSourceRange
	restores []string
}

func (r tomlSourceRange) valid() bool {
	return r.start >= 0 && r.end > r.start
}

type tomlSectionBooleanValue struct {
	enabled       bool
	present       bool
	sectionExists bool
	sectionInsert int
	rootInsert    int
	expression    tomlSourceRange
	value         tomlSourceRange
	inlineTable   tomlSourceRange
	inlineEntries bool
}

func parseTOMLSectionBoolean(content, section, key string) (tomlSectionBooleanValue, error) {
	data := []byte(content)
	result := tomlSectionBooleanValue{sectionInsert: -1, rootInsert: len(data)}
	semantic, err := decodeTOMLSectionBoolean(data, section, key)
	if err != nil {
		return result, err
	}
	result.enabled = semantic.enabled
	result.present = semantic.present
	result.sectionExists = semantic.sectionExists
	location, err := locateTOMLSectionBoolean(data, section, key)
	if err != nil {
		return result, err
	}
	result.sectionInsert = location.sectionInsert
	result.rootInsert = location.rootInsert
	result.expression = location.expression
	result.value = location.value
	result.inlineTable = location.inlineTable
	result.inlineEntries = location.inlineEntries
	if result.present && (!result.expression.valid() || !result.value.valid()) {
		return result, fmt.Errorf("cannot locate %s.%s in parsed TOML", section, key)
	}
	return result, nil
}

type tomlBooleanSemantic struct {
	enabled       bool
	present       bool
	sectionExists bool
}

func decodeTOMLSectionBoolean(data []byte, section, key string) (tomlBooleanSemantic, error) {
	result := tomlBooleanSemantic{}
	var document map[string]interface{}
	if err := toml.Unmarshal(data, &document); err != nil {
		return result, fmt.Errorf("invalid TOML: %w", err)
	}
	if _, exists := document[key]; exists {
		return result, fmt.Errorf("%s is at the TOML root; expected [%s]", key, section)
	}
	rawSection, sectionExists := document[section]
	result.sectionExists = sectionExists
	if !sectionExists {
		return result, nil
	}
	sectionTable, ok := rawSection.(map[string]interface{})
	if !ok {
		return result, fmt.Errorf("%s must be a TOML table", section)
	}
	rawValue, exists := sectionTable[key]
	if !exists {
		return result, nil
	}
	enabled, ok := rawValue.(bool)
	if !ok {
		return result, fmt.Errorf("%s.%s must be a boolean", section, key)
	}
	result.enabled = enabled
	result.present = true
	return result, nil
}

type tomlSectionLocation struct {
	sectionInsert int
	rootInsert    int
	expression    tomlSourceRange
	value         tomlSourceRange
	inlineTable   tomlSourceRange
	inlineEntries bool
}

type parsedTOMLComment struct {
	text string
	line tomlSourceRange
}

func locateTOMLSectionBoolean(data []byte, section, key string) (tomlSectionLocation, error) {
	location := tomlSectionLocation{sectionInsert: -1, rootInsert: len(data)}
	parser := unstable.Parser{}
	parser.Reset(data)
	currentTable := []string(nil)
	for parser.NextExpression() {
		expression := parser.Expression()
		switch expression.Kind {
		case unstable.Table, unstable.ArrayTable:
			parts := tomlNodeKey(expression)
			line := tomlPhysicalLines(data, expression.Child().Raw)
			if location.rootInsert == len(data) {
				location.rootInsert = line.start
			}
			currentTable = parts
			if expression.Kind == unstable.Table && slices.Equal(parts, []string{section}) {
				location.sectionInsert = line.end
			}
		case unstable.KeyValue:
			locateTOMLKeyValue(&location, &parser, expression, currentTable, section, key)
		}
	}
	if err := parser.Error(); err != nil {
		return location, fmt.Errorf("inspect parsed TOML source: %w", err)
	}
	return location, nil
}

func locateTOMLKeyValue(location *tomlSectionLocation, parser *unstable.Parser, expression *unstable.Node, table []string, section, key string) {
	parts := append(slices.Clone(table), tomlNodeKey(expression)...)
	if slices.Equal(parts, []string{section, key}) {
		location.expression = tomlPhysicalLines(parser.Data(), expression.Raw)
		location.value = tomlRange(expression.Value().Raw)
		return
	}
	keyParts := tomlNodeKey(expression)
	value := expression.Value()
	if len(table) != 0 || !slices.Equal(keyParts, []string{section}) || value.Kind != unstable.InlineTable {
		return
	}
	location.expression = tomlPhysicalLines(parser.Data(), expression.Raw)
	location.inlineTable = tomlSourceRange{
		start: int(value.Raw.Offset),
		end:   int(expression.Raw.Offset + expression.Raw.Length),
	}
	location.inlineEntries = value.Child() != nil
	if target := findInlineTOMLValue(value, []string{key}); target != nil {
		location.value = tomlRange(target.Raw)
	}
}

func findInlineTOMLValue(table *unstable.Node, path []string) *unstable.Node {
	children := table.Children()
	for children.Next() {
		entry := children.Node()
		parts := tomlNodeKey(entry)
		if slices.Equal(parts, path) {
			return entry.Value()
		}
		if len(parts) < len(path) && slices.Equal(parts, path[:len(parts)]) && entry.Value().Kind == unstable.InlineTable {
			if value := findInlineTOMLValue(entry.Value(), path[len(parts):]); value != nil {
				return value
			}
		}
	}
	return nil
}

func tomlNodeKey(node *unstable.Node) []string {
	parts := []string{}
	keys := node.Key()
	for keys.Next() {
		parts = append(parts, string(keys.Node().Data))
	}
	return parts
}

func tomlRange(value unstable.Range) tomlSourceRange {
	return tomlSourceRange{start: int(value.Offset), end: int(value.Offset + value.Length)}
}

func tomlPhysicalLines(data []byte, value unstable.Range) tomlSourceRange {
	raw := tomlRange(value)
	start := bytes.LastIndexByte(data[:raw.start], '\n') + 1
	endOffset := bytes.IndexByte(data[raw.end:], '\n')
	if endOffset < 0 {
		return tomlSourceRange{start: start, end: len(data)}
	}
	return tomlSourceRange{start: start, end: raw.end + endOffset + 1}
}

func validCodexActivationRestore(data []byte) bool {
	parser := unstable.Parser{}
	parser.Reset(data)
	if !parser.NextExpression() {
		return false
	}
	expression := parser.Expression()
	if expression.Kind != unstable.KeyValue {
		return false
	}
	parts := tomlNodeKey(expression)
	valueKind := expression.Value().Kind
	if parser.NextExpression() || parser.Error() != nil {
		return false
	}
	if !slices.Equal(parts, []string{"hooks"}) && !slices.Equal(parts, []string{"features", "hooks"}) &&
		!(slices.Equal(parts, []string{"features"}) && valueKind == unstable.InlineTable) {
		return false
	}
	var document map[string]interface{}
	if err := toml.Unmarshal(data, &document); err != nil {
		return false
	}
	if value, exists := document["hooks"]; exists {
		enabled, ok := value.(bool)
		return ok && !enabled
	}
	features, ok := document["features"].(map[string]interface{})
	if !ok {
		return false
	}
	value, exists := features["hooks"]
	if !exists {
		return true
	}
	enabled, ok := value.(bool)
	return ok && !enabled
}

func findCodexActivationBlock(data []byte) (codexActivationBlockRange, bool, error) {
	comments, err := parseTOMLComments(data)
	if err != nil {
		return codexActivationBlockRange{}, false, err
	}
	starts := commentLinesEqual(comments, CodexActivationBlockStart)
	ends := commentLinesEqual(comments, CodexActivationBlockEnd)
	if len(starts) == 0 && len(ends) == 0 {
		return codexActivationBlockRange{}, false, nil
	}
	if len(starts) != 1 || len(ends) != 1 || ends[0].start <= starts[0].start {
		return codexActivationBlockRange{}, false, fmt.Errorf("invalid reconc Codex activation block markers")
	}
	block := codexActivationBlockRange{span: tomlSourceRange{start: starts[0].start, end: ends[0].end}}
	for _, comment := range comments {
		if comment.line.start <= starts[0].start || comment.line.end > ends[0].start ||
			!strings.HasPrefix(comment.text, codexActivationRestorePrefix) {
			continue
		}
		block.restores = append(block.restores, strings.TrimPrefix(comment.text, codexActivationRestorePrefix))
	}
	return block, true, nil
}

func parseTOMLComments(data []byte) ([]parsedTOMLComment, error) {
	comments := []parsedTOMLComment{}
	parser := unstable.Parser{KeepComments: true}
	parser.Reset(data)
	for parser.NextExpression() {
		for node := parser.Expression(); node != nil; node = node.Next() {
			if node.Kind != unstable.Comment {
				continue
			}
			line := tomlPhysicalLines(data, node.Raw)
			raw := tomlRange(node.Raw)
			if len(bytes.TrimSpace(data[line.start:raw.start])) != 0 || len(bytes.TrimSpace(data[raw.end:line.end])) != 0 {
				continue
			}
			comments = append(comments, parsedTOMLComment{text: strings.TrimSpace(string(node.Data)), line: line})
		}
	}
	if err := parser.Error(); err != nil {
		return nil, fmt.Errorf("inspect Codex activation markers: %w", err)
	}
	return comments, nil
}

func commentLinesEqual(comments []parsedTOMLComment, marker string) []tomlSourceRange {
	lines := []tomlSourceRange{}
	for _, comment := range comments {
		if comment.text == marker {
			lines = append(lines, comment.line)
		}
	}
	return lines
}
