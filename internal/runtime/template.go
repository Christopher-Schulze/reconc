package runtime

import (
	"fmt"
	"regexp"
	"strings"
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

	regex, names, err := compileTemplatePattern(pattern)
	if err != nil {
		return nil, false, err
	}
	match := regex.FindStringSubmatch(path)
	if match == nil {
		return nil, false, nil
	}
	captures := make(map[string]string, len(names))
	for i, name := range names {
		// match[0] is the full match; match[i+1] is group i (1-indexed).
		captures[name] = match[i+1]
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

// compileTemplatePattern converts a template pattern (with {var}
// placeholders + globstar wildcards) into a regex with named groups
// and the list of group names in declaration order.
//
// Conversion rules (applied in order):
//
//	{name} -> ([^/]+)                          (capture, single segment)
//	**     -> .*                               (multi-segment glob)
//	*      -> [^/]*                            (single-segment glob)
//	?      -> [^/]                             (single non-slash char)
//	.      -> \.                               (escaped dot)
//	other  -> regex-escaped literal
//
// Return: compiled regex (anchored ^...$), ordered slice of capture
// names, possibly an error if a name appears twice.
func compileTemplatePattern(pattern string) (*regexp.Regexp, []string, error) {
	var (
		names []string
		seen  = map[string]struct{}{}
		buf   strings.Builder
	)
	buf.WriteString("^")

	i := 0
	for i < len(pattern) {
		ch := pattern[i]
		// Try to match a {name} placeholder at position i.
		if ch == '{' {
			if loc := templateVarRegex.FindStringSubmatchIndex(pattern[i:]); loc != nil && loc[0] == 0 {
				name := pattern[i+loc[2] : i+loc[3]]
				if _, dup := seen[name]; dup {
					return nil, nil, fmt.Errorf("template variable %q appears twice in pattern %q", name, pattern)
				}
				seen[name] = struct{}{}
				names = append(names, name)
				buf.WriteString("([^/]+)")
				i += loc[1]
				continue
			}
		}
		// Glob wildcards.
		switch ch {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				buf.WriteString(".*")
				i += 2
				continue
			}
			buf.WriteString("[^/]*")
			i++
			continue
		case '?':
			buf.WriteString("[^/]")
			i++
			continue
		case '.', '+', '(', ')', '^', '$', '|', '\\', '[', ']':
			buf.WriteByte('\\')
			buf.WriteByte(ch)
			i++
			continue
		default:
			buf.WriteByte(ch)
			i++
		}
	}
	buf.WriteString("$")

	re, err := regexp.Compile(buf.String())
	if err != nil {
		return nil, nil, fmt.Errorf("compile template pattern %q: %w", pattern, err)
	}
	return re, names, nil
}
