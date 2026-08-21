package action

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/bmatcuk/doublestar/v4"
)

const compiledGlobBranchOverhead = 16

type globTokenKind uint8

const (
	globLiteral globTokenKind = iota
	globQuestion
	globStar
	globDirectories
	globRemainder
	globCharacterClass
)

type globRange struct {
	low  rune
	high rune
}

type globClass struct {
	negated  bool
	literals []rune
	ranges   []globRange
}

type globToken struct {
	kind     globTokenKind
	rawStart int
	literal  rune
	class    globClass
}

type globProgram struct {
	pattern string
	tokens  []globToken
	zero    []bool
}

type globExpansion struct {
	pattern       string
	zeroOverrides map[int]bool
	starOverrides map[int]globStarShape
	segmentResets map[int]bool
}

type globStarShape struct {
	kind  globTokenKind
	width int
}

func compileGlob(pattern string) (*CompiledGlob, error) {
	if !utf8.ValidString(pattern) || len(pattern) > MaxPatternBytes || !doublestar.ValidatePattern(pattern) {
		return nil, fmt.Errorf("glob requires a valid pattern of at most %d UTF-8 bytes", MaxPatternBytes)
	}
	expanded, err := expandGlobAlternatives(pattern)
	if err != nil {
		return nil, err
	}
	programs := make([]globProgram, 0, len(expanded))
	logicalBytes := len(pattern)
	for _, candidate := range expanded {
		program, programBytes, compileErr := compileGlobProgram(candidate)
		if compileErr != nil {
			return nil, compileErr
		}
		logicalBytes += programBytes
		if logicalBytes > MaxCompiledPlanBytes {
			return nil, fmt.Errorf("compiled glob requires %d logical bytes; maximum action plan size is %d", logicalBytes, MaxCompiledPlanBytes)
		}
		programs = append(programs, program)
	}
	return &CompiledGlob{pattern: pattern, programs: programs, logicalBytes: logicalBytes}, nil
}

// CompileGlob builds an immutable glob program from validated doublestar
// syntax. The returned matcher is safe for concurrent read-only Match calls.
// Callers should compile policy-owned patterns once and retain the program
// rather than reparsing the same pattern for every candidate value.
func CompileGlob(pattern string) (*CompiledGlob, error) {
	return compileGlob(pattern)
}

// LogicalBytes reports the bounded admission cost of the immutable matcher
// program. It lets higher-level plans cap aggregate compiled matcher memory
// without exposing the program's internal token layout.
func (g *CompiledGlob) LogicalBytes() int {
	if g == nil {
		return 0
	}
	return g.logicalBytes
}

func expandGlobAlternatives(pattern string) ([]globExpansion, error) {
	initial := globExpansion{
		pattern: pattern, zeroOverrides: map[int]bool{}, starOverrides: map[int]globStarShape{}, segmentResets: map[int]bool{},
	}
	queue := []globExpansion{initial}
	seen := map[string]bool{globExpansionKey(initial): true}
	logicalBytes := globBranchCost(initial)
	finished := make([]globExpansion, 0, 1)
	for index := 0; index < len(queue); index++ {
		candidate := queue[index]
		open, close, found := firstGlobAlternative(candidate.pattern)
		if !found {
			finished = append(finished, candidate)
			continue
		}
		logicalBytes -= globBranchCost(candidate)
		captureGlobPrefixState(&candidate, open)
		alternatives := splitGlobAlternatives(candidate.pattern[open+1 : close])
		for _, alternative := range alternatives {
			expanded := replaceGlobAlternative(candidate, open, close, alternative)
			key := globExpansionKey(expanded)
			if seen[key] {
				continue
			}
			seen[key] = true
			logicalBytes += globBranchCost(expanded)
			if logicalBytes > MaxCompiledPlanBytes {
				return nil, fmt.Errorf("compiled glob alternatives exceed the %d-byte action plan admission limit", MaxCompiledPlanBytes)
			}
			queue = append(queue, expanded)
		}
	}
	sort.Slice(finished, func(i, j int) bool { return globExpansionKey(finished[i]) < globExpansionKey(finished[j]) })
	return finished, nil
}

func captureGlobPrefixState(candidate *globExpansion, through int) {
	for _, position := range globTokenBoundaries(candidate, through) {
		if _, present := candidate.zeroOverrides[position]; present {
			continue
		}
		candidate.zeroOverrides[position] = doublestar.MatchUnvalidated(candidate.pattern[position:], "")
	}
}

func replaceGlobAlternative(candidate globExpansion, open int, close int, alternative string) globExpansion {
	delta := len(alternative) - (close - open + 1)
	out := globExpansion{
		pattern:       candidate.pattern[:open] + alternative + candidate.pattern[close+1:],
		zeroOverrides: make(map[int]bool, len(candidate.zeroOverrides)),
		starOverrides: make(map[int]globStarShape, len(candidate.starOverrides)),
		segmentResets: make(map[int]bool, len(candidate.segmentResets)+1),
	}
	for position, zero := range candidate.zeroOverrides {
		switch {
		case position <= open:
			out.zeroOverrides[position] = zero
		case position > close:
			out.zeroOverrides[position+delta] = zero
		}
	}
	for position, shape := range candidate.starOverrides {
		switch {
		case position < open:
			out.starOverrides[position] = shape
		case position > close:
			out.starOverrides[position+delta] = shape
		}
	}
	for position := range candidate.segmentResets {
		switch {
		case position <= open:
			out.segmentResets[position] = true
		case position > close:
			out.segmentResets[position+delta] = true
		}
	}
	out.segmentResets[open] = true
	return out
}

func globTokenBoundaries(candidate *globExpansion, through int) []int {
	pattern := candidate.pattern
	boundaries := make([]int, 0, through+1)
	startOfSegment := true
	for index := 0; index < through; {
		if candidate.segmentResets[index] {
			startOfSegment = true
		}
		boundaries = append(boundaries, index)
		switch pattern[index] {
		case '*':
			if shape, present := candidate.starOverrides[index]; present {
				index += shape.width
				startOfSegment = shape.kind == globDirectories
				continue
			}
			shape := globStarShape{kind: globStar, width: 1}
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				shape.width = 2
				if startOfSegment && index+2 == len(pattern) {
					shape.kind = globRemainder
				} else if startOfSegment && index+2 < len(pattern) && pattern[index+2] == '/' {
					shape.kind = globDirectories
					shape.width = 3
				}
			}
			candidate.starOverrides[index] = shape
			index += shape.width
			startOfSegment = shape.kind == globDirectories
		case '[':
			_, next, _ := compileGlobClass(pattern, index)
			index = next
			startOfSegment = false
		case '\\':
			character, width := utf8.DecodeRuneInString(pattern[index+1:])
			index += width + 1
			startOfSegment = character == '/'
		default:
			character, width := utf8.DecodeRuneInString(pattern[index:])
			index += width
			startOfSegment = character == '/'
		}
	}
	return append(boundaries, through)
}

func globExpansionKey(candidate globExpansion) string {
	positions := make([]int, 0, len(candidate.zeroOverrides))
	for position := range candidate.zeroOverrides {
		positions = append(positions, position)
	}
	sort.Ints(positions)
	var key strings.Builder
	key.WriteString(candidate.pattern)
	key.WriteByte(0)
	for _, position := range positions {
		key.WriteString(strconv.Itoa(position))
		if candidate.zeroOverrides[position] {
			key.WriteString(":1;")
		} else {
			key.WriteString(":0;")
		}
	}
	starPositions := make([]int, 0, len(candidate.starOverrides))
	for position := range candidate.starOverrides {
		starPositions = append(starPositions, position)
	}
	sort.Ints(starPositions)
	key.WriteByte(0)
	for _, position := range starPositions {
		shape := candidate.starOverrides[position]
		key.WriteString(strconv.Itoa(position))
		key.WriteByte(':')
		key.WriteString(strconv.Itoa(int(shape.kind)))
		key.WriteByte(':')
		key.WriteString(strconv.Itoa(shape.width))
		key.WriteByte(';')
	}
	resetPositions := make([]int, 0, len(candidate.segmentResets))
	for position := range candidate.segmentResets {
		resetPositions = append(resetPositions, position)
	}
	sort.Ints(resetPositions)
	key.WriteByte(0)
	for _, position := range resetPositions {
		key.WriteString(strconv.Itoa(position))
		key.WriteByte(';')
	}
	return key.String()
}

func firstGlobAlternative(pattern string) (int, int, bool) {
	inClass := false
	escaped := false
	for index := 0; index < len(pattern); index++ {
		character := pattern[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if inClass {
			if character == ']' {
				inClass = false
			}
			continue
		}
		if character == '[' {
			inClass = true
			continue
		}
		if character != '{' {
			continue
		}
		depth := 1
		innerClass := false
		innerEscaped := false
		for close := index + 1; close < len(pattern); close++ {
			next := pattern[close]
			if innerEscaped {
				innerEscaped = false
				continue
			}
			if next == '\\' {
				innerEscaped = true
				continue
			}
			if innerClass {
				if next == ']' {
					innerClass = false
				}
				continue
			}
			if next == '[' {
				innerClass = true
				continue
			}
			switch next {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return index, close, true
				}
			}
		}
		return 0, 0, false
	}
	return 0, 0, false
}

func splitGlobAlternatives(body string) []string {
	parts := make([]string, 0, 2)
	start := 0
	depth := 0
	inClass := false
	escaped := false
	for index := 0; index < len(body); index++ {
		character := body[index]
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if inClass {
			if character == ']' {
				inClass = false
			}
			continue
		}
		if character == '[' {
			inClass = true
			continue
		}
		switch character {
		case '{':
			depth++
		case '}':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, body[start:index])
				start = index + 1
			}
		}
	}
	return append(parts, body[start:])
}

func compileGlobProgram(expansion globExpansion) (globProgram, int, error) {
	pattern := expansion.pattern
	tokens := make([]globToken, 0, len(pattern))
	logicalBytes := len(pattern) + len(expansion.zeroOverrides)*2
	startOfSegment := true
	for index := 0; index < len(pattern); {
		if expansion.segmentResets[index] {
			startOfSegment = true
		}
		rawStart := index
		switch pattern[index] {
		case '*':
			if shape, present := expansion.starOverrides[index]; present {
				tokens = append(tokens, globToken{kind: shape.kind, rawStart: rawStart})
				index += shape.width
				startOfSegment = shape.kind == globDirectories
				continue
			}
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				if startOfSegment && index+2 == len(pattern) {
					tokens = append(tokens, globToken{kind: globRemainder, rawStart: rawStart})
					index += 2
					startOfSegment = false
					continue
				}
				if startOfSegment && index+2 < len(pattern) && pattern[index+2] == '/' {
					tokens = append(tokens, globToken{kind: globDirectories, rawStart: rawStart})
					index += 3
					startOfSegment = true
					continue
				}
				index += 2
			} else {
				index++
			}
			tokens = append(tokens, globToken{kind: globStar, rawStart: rawStart})
			startOfSegment = false
		case '?':
			tokens = append(tokens, globToken{kind: globQuestion, rawStart: rawStart})
			index++
			startOfSegment = false
		case '[':
			class, next, err := compileGlobClass(pattern, index)
			if err != nil {
				return globProgram{}, 0, err
			}
			tokens = append(tokens, globToken{kind: globCharacterClass, rawStart: rawStart, class: class})
			logicalBytes += len(class.literals)*4 + len(class.ranges)*8
			index = next
			startOfSegment = false
		case '\\':
			character, width := utf8.DecodeRuneInString(pattern[index+1:])
			tokens = append(tokens, globToken{kind: globLiteral, rawStart: rawStart, literal: character})
			index += width + 1
			startOfSegment = character == '/'
		default:
			character, width := utf8.DecodeRuneInString(pattern[index:])
			tokens = append(tokens, globToken{kind: globLiteral, rawStart: rawStart, literal: character})
			index += width
			startOfSegment = character == '/'
		}
	}
	logicalBytes += len(tokens) * 32
	zero := make([]bool, len(tokens)+1)
	for index := range tokens {
		if override, present := expansion.zeroOverrides[tokens[index].rawStart]; present {
			zero[index] = override
		} else {
			zero[index] = doublestar.MatchUnvalidated(pattern[tokens[index].rawStart:], "")
		}
	}
	if override, present := expansion.zeroOverrides[len(pattern)]; present {
		zero[len(tokens)] = override
	} else {
		zero[len(tokens)] = true
	}
	logicalBytes += len(zero)
	return globProgram{pattern: pattern, tokens: tokens, zero: zero}, logicalBytes, nil
}

func compileGlobClass(pattern string, start int) (globClass, int, error) {
	index := start + 1
	negated := false
	if pattern[index] == '!' || pattern[index] == '^' {
		negated = true
		index++
	}
	ranges := make([]globRange, 0, 4)
	literals := make([]rune, 0, 4)
	last := utf8.MaxRune
	for index < len(pattern) && pattern[index] != ']' {
		character, width := utf8.DecodeRuneInString(pattern[index:])
		index += width
		if last < utf8.MaxRune && character == '-' && index < len(pattern) && pattern[index] != ']' {
			if pattern[index] == '\\' {
				index++
			}
			upper, upperWidth := utf8.DecodeRuneInString(pattern[index:])
			index += upperWidth
			if last <= upper {
				ranges = append(ranges, globRange{low: last, high: upper})
			}
			last = utf8.MaxRune
			continue
		}
		if character == '\\' {
			character, width = utf8.DecodeRuneInString(pattern[index:])
			index += width
		}
		literals = append(literals, character)
		last = character
	}
	if index >= len(pattern) || pattern[index] != ']' {
		return globClass{}, 0, fmt.Errorf("glob character class is not closed")
	}
	return globClass{negated: negated, literals: literals, ranges: ranges}, index + 1, nil
}

func (g *CompiledGlob) Match(value string) bool {
	if g == nil {
		return false
	}
	for index := range g.programs {
		if g.programs[index].match(value) {
			return true
		}
	}
	return false
}

func (p globProgram) match(value string) bool {
	patternIndex := 0
	nameIndex := 0
	directoryPatternBacktrack := -1
	directoryNameBacktrack := -1
	starPatternBacktrack := -1
	starNameBacktrack := -1

match:
	for nameIndex < len(value) {
		if patternIndex < len(p.tokens) {
			token := p.tokens[patternIndex]
			switch token.kind {
			case globRemainder:
				return true
			case globDirectories:
				patternIndex++
				directoryPatternBacktrack = patternIndex
				directoryNameBacktrack = nameIndex
				starPatternBacktrack = -1
				starNameBacktrack = -1
				continue
			case globStar:
				patternIndex++
				starPatternBacktrack = patternIndex
				starNameBacktrack = nameIndex
				continue
			case globQuestion:
				character, width := utf8.DecodeRuneInString(value[nameIndex:])
				if character != '/' {
					patternIndex++
					nameIndex += width
					continue
				}
			case globCharacterClass:
				character, width := utf8.DecodeRuneInString(value[nameIndex:])
				if token.class.match(character) {
					patternIndex++
					nameIndex += width
					continue
				}
			case globLiteral:
				character, width := utf8.DecodeRuneInString(value[nameIndex:])
				if token.literal == character {
					patternIndex++
					nameIndex += width
					continue
				}
			}
		}

		if starPatternBacktrack >= 0 {
			character, width := utf8.DecodeRuneInString(value[starNameBacktrack:])
			if character != '/' {
				starNameBacktrack += width
				patternIndex = starPatternBacktrack
				nameIndex = starNameBacktrack
				continue
			}
		}

		if directoryPatternBacktrack >= 0 {
			nameIndex = directoryNameBacktrack
			for nameIndex < len(value) {
				character, width := utf8.DecodeRuneInString(value[nameIndex:])
				nameIndex += width
				if character == '/' {
					directoryNameBacktrack = nameIndex
					patternIndex = directoryPatternBacktrack
					continue match
				}
			}
		}
		return false
	}
	return p.zeroLength(patternIndex)
}

func (p globProgram) zeroLength(tokenIndex int) bool {
	return tokenIndex >= 0 && tokenIndex < len(p.zero) && p.zero[tokenIndex]
}

func (c globClass) match(character rune) bool {
	matched := false
	for _, literal := range c.literals {
		if literal == character {
			matched = true
			break
		}
	}
	if !matched {
		for _, item := range c.ranges {
			if item.low <= character && character <= item.high {
				matched = true
				break
			}
		}
	}
	return matched != c.negated
}

func (g *CompiledGlob) clone() *CompiledGlob {
	if g == nil {
		return nil
	}
	out := &CompiledGlob{pattern: g.pattern, logicalBytes: g.logicalBytes, programs: make([]globProgram, len(g.programs))}
	for index := range g.programs {
		out.programs[index] = globProgram{
			pattern: g.programs[index].pattern,
			tokens:  make([]globToken, len(g.programs[index].tokens)),
			zero:    append([]bool(nil), g.programs[index].zero...),
		}
		copy(out.programs[index].tokens, g.programs[index].tokens)
		for tokenIndex := range out.programs[index].tokens {
			class := &out.programs[index].tokens[tokenIndex].class
			class.literals = append([]rune(nil), class.literals...)
			class.ranges = append([]globRange(nil), class.ranges...)
		}
	}
	return out
}

func globBranchCost(candidate globExpansion) int {
	return len(candidate.pattern) + compiledGlobBranchOverhead + len(candidate.zeroOverrides)*16 + len(candidate.starOverrides)*16 + len(candidate.segmentResets)*8
}
