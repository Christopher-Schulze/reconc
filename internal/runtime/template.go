package runtime

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Template variables let when_paths patterns CAPTURE path segments
// that get substituted into other rule fields (required_files paths,
// evidence file paths, commands, claims). This is the W25 feature
// that makes evidence rules scale across tasks/modules without
// enumerating every value.
//
// Pattern syntax:
//
//	docs/todo/{task_id}.md          {task_id} captures one segment
//	src/{module}/main.go            {module} captures one segment
//	**/{leaf}                       {leaf} captures the trailing segment
//
// Restrictions for v1:
//   - {var} captures a SINGLE path segment (no slashes); exactly the
//     same scope as `*` in glob terms
//   - Variable names match [A-Za-z_][A-Za-z0-9_]*
//   - Each variable may appear AT MOST once in a pattern (no
//     back-references; if you need the same segment twice, repeat
//     yourself rather than introduce capture-binding semantics)
//
// Multi-segment captures (`{var:**}` syntax) are deferred to a future
// version; the design is forward-compatible.

var templateVarRegex = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// HasTemplateVars reports whether pattern contains any {var}
// placeholders. Used by the evaluator to skip the template-matching
// path entirely when no captures are needed (fast path for the
// non-templated majority of rules).
func HasTemplateVars(pattern string) bool {
	return templateVarRegex.MatchString(pattern)
}

// PatternHasAnyTemplateVar reports whether ANY pattern in the slice
// contains a template variable.
func PatternHasAnyTemplateVar(patterns []string) bool {
	for _, p := range patterns {
		if HasTemplateVars(p) {
			return true
		}
	}
	return false
}

// MatchTemplate matches path against pattern, returning the captured
// variables on success.
//
//   - If pattern has NO template vars, returns (nil, ok, err) where ok
//     is the result of regular MatchPath.
//   - If pattern HAS template vars, compiles to a regex with named
//     groups, matches, and returns (captures, true, nil) on hit.
//
// Captures map names to captured values; empty map on a non-template
// pattern that matched.
func MatchTemplate(pattern, path string) (map[string]string, bool, error) {
	if !HasTemplateVars(pattern) {
		ok, err := MatchPath(pattern, path)
		if err != nil {
			return nil, false, err
		}
		return map[string]string{}, ok, nil
	}
	path = filepath.ToSlash(path)
	masked := templateVarRegex.ReplaceAllString(strings.TrimSpace(pattern), "*")
	ok, err := MatchPath(masked, path)
	if err != nil || !ok {
		return nil, ok, err
	}

	regex, names, err := compileTemplatePattern(pattern)
	if err != nil {
		return nil, false, err
	}
	match := regex.FindStringSubmatch(path)
	if match == nil {
		return nil, false, fmt.Errorf("template matcher diverged from validated glob semantics for pattern %q", pattern)
	}
	captures := make(map[string]string, len(names))
	for i, name := range names {
		// match[0] is the full match; match[i+1] is group i (1-indexed).
		captures[name] = match[i+1]
	}
	bound := templateVarRegex.ReplaceAllStringFunc(strings.TrimSpace(pattern), func(placeholder string) string {
		return escapeGlobLiteral(captures[placeholder[1:len(placeholder)-1]])
	})
	boundOK, err := MatchPath(bound, path)
	if err != nil {
		return nil, false, err
	}
	if !boundOK {
		return nil, false, fmt.Errorf("template captures diverged from validated glob semantics for pattern %q", pattern)
	}
	return captures, true, nil
}

// MatchTemplateAny tries each pattern in order. Returns the first hit
// with its captures + the matched pattern string.
func MatchTemplateAny(patterns []string, path string) (matched string, captures map[string]string, ok bool, err error) {
	for _, pat := range patterns {
		caps, hit, err := MatchTemplate(pat, path)
		if err != nil {
			return "", nil, false, err
		}
		if hit {
			return pat, caps, true, nil
		}
	}
	return "", nil, false, nil
}

// SubstituteTemplate replaces every {var} in s with captures[var].
// If a referenced variable is not in captures, the placeholder is
// left intact and an error is returned (so callers can surface the
// configuration mistake clearly).
func SubstituteTemplate(s string, captures map[string]string) (string, error) {
	var missing []string
	out := templateVarRegex.ReplaceAllStringFunc(s, func(placeholder string) string {
		// Strip the surrounding braces.
		name := placeholder[1 : len(placeholder)-1]
		if val, ok := captures[name]; ok {
			return val
		}
		missing = append(missing, name)
		return placeholder
	})
	if len(missing) > 0 {
		return out, fmt.Errorf("unresolved template variables: %s", strings.Join(missing, ", "))
	}
	return out, nil
}

// SubstituteTemplateInList applies SubstituteTemplate to every entry
// in a string slice. Returns a new slice; original is untouched.
func SubstituteTemplateInList(items []string, captures map[string]string) ([]string, error) {
	out := make([]string, len(items))
	for i, s := range items {
		sub, err := SubstituteTemplate(s, captures)
		if err != nil {
			return nil, err
		}
		out[i] = sub
	}
	return out, nil
}

// compileTemplatePattern translates the same glob grammar validated by
// doublestar into a capture regex. MatchTemplate also checks the masked and
// capture-bound forms with MatchPath, so this translator can never silently
// broaden a security-relevant match.
func compileTemplatePattern(pattern string) (*regexp.Regexp, []string, error) {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	names := []string{}
	seen := map[string]struct{}{}
	var buf strings.Builder
	buf.WriteString("^")
	if err := appendTemplateGlobRegex(&buf, pattern, &names, seen); err != nil {
		return nil, nil, fmt.Errorf("compile template pattern %q: %w", pattern, err)
	}
	buf.WriteString("$")
	re, err := regexp.Compile(buf.String())
	if err != nil {
		return nil, nil, fmt.Errorf("compile template pattern %q: %w", pattern, err)
	}
	return re, names, nil
}

func appendTemplateGlobRegex(buf *strings.Builder, pattern string, names *[]string, seen map[string]struct{}) error {
	for i := 0; i < len(pattern); {
		if loc := templateVarRegex.FindStringSubmatchIndex(pattern[i:]); loc != nil && loc[0] == 0 {
			name := pattern[i+loc[2] : i+loc[3]]
			if _, duplicate := seen[name]; duplicate {
				return fmt.Errorf("template variable %q appears twice", name)
			}
			seen[name] = struct{}{}
			*names = append(*names, name)
			buf.WriteString("([^/]+)")
			i += loc[1]
			continue
		}

		switch pattern[i] {
		case '/':
			switch {
			case strings.HasPrefix(pattern[i:], "/**/") && i+4 == len(pattern):
				buf.WriteString("(?:/(?:[^/]+/)*)?")
				i += 4
			case strings.HasPrefix(pattern[i:], "/**/"):
				buf.WriteString("/(?:[^/]+/)*")
				i += 4
			case strings.HasPrefix(pattern[i:], "/**") && i+3 == len(pattern):
				buf.WriteString("(?:/.*)?")
				i += 3
			default:
				buf.WriteByte('/')
				i++
			}
		case '*':
			switch {
			case strings.HasPrefix(pattern[i:], "**/") && i == 0:
				buf.WriteString("(?:[^/]+/)*")
				i += 3
			case strings.HasPrefix(pattern[i:], "**") && i+2 == len(pattern) && i == 0:
				buf.WriteString(".*")
				i += 2
			case strings.HasPrefix(pattern[i:], "**"):
				buf.WriteString("[^/]*")
				i += 2
			default:
				buf.WriteString("[^/]*")
				i++
			}
		case '?':
			buf.WriteString("[^/]")
			i++
		case '[':
			class, next, err := templateCharacterClass(pattern, i)
			if err != nil {
				return err
			}
			buf.WriteString(class)
			i = next
		case '{':
			alternatives, next, err := templateAlternatives(pattern, i)
			if err != nil {
				return err
			}
			buf.WriteString("(?:")
			for index, alternative := range alternatives {
				if index > 0 {
					buf.WriteByte('|')
				}
				if err := appendTemplateGlobRegex(buf, alternative, names, seen); err != nil {
					return err
				}
			}
			buf.WriteByte(')')
			i = next
		case '\\':
			if i+1 >= len(pattern) {
				return fmt.Errorf("dangling escape")
			}
			_, size := utf8.DecodeRuneInString(pattern[i+1:])
			buf.WriteString(regexp.QuoteMeta(pattern[i+1 : i+1+size]))
			i += 1 + size
		default:
			_, size := utf8.DecodeRuneInString(pattern[i:])
			buf.WriteString(regexp.QuoteMeta(pattern[i : i+size]))
			i += size
		}
	}
	return nil
}

func templateCharacterClass(pattern string, start int) (string, int, error) {
	end := start + 1
	escaped := false
	for ; end < len(pattern); end++ {
		if escaped {
			escaped = false
			continue
		}
		if pattern[end] == '\\' {
			escaped = true
			continue
		}
		if pattern[end] == ']' {
			break
		}
	}
	if end >= len(pattern) || end == start+1 {
		return "", 0, fmt.Errorf("invalid character class")
	}
	content := pattern[start+1 : end]
	if content[0] == '!' {
		content = "^" + content[1:]
	}
	return "[" + content + "]", end + 1, nil
}

func templateAlternatives(pattern string, start int) ([]string, int, error) {
	depth := 0
	classDepth := 0
	escaped := false
	partStart := start + 1
	parts := []string{}
	for i := start + 1; i < len(pattern); i++ {
		ch := pattern[i]
		if escaped {
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '[' {
			classDepth++
			continue
		}
		if ch == ']' && classDepth > 0 {
			classDepth--
			continue
		}
		if classDepth > 0 {
			continue
		}
		switch ch {
		case '{':
			depth++
		case '}':
			if depth == 0 {
				parts = append(parts, pattern[partStart:i])
				return parts, i + 1, nil
			}
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, pattern[partStart:i])
				partStart = i + 1
			}
		}
	}
	return nil, 0, fmt.Errorf("unterminated alternative")
}

func escapeGlobLiteral(value string) string {
	var escaped strings.Builder
	for _, r := range value {
		if strings.ContainsRune(`*?[]{}\\`, r) {
			escaped.WriteByte('\\')
		}
		escaped.WriteRune(r)
	}
	return escaped.String()
}
