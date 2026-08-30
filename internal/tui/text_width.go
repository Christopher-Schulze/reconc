package tui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/width"
)

const truncationMarker = "..."

type displayClusterProperties struct {
	regionalIndicators int
	emojiModifier      bool
	emojiTag           bool
	keycap             bool
	textVariation      bool
	emojiVariation     bool
	joined             bool
}

// truncateTextCells applies Reconc's deterministic terminal-width contract.
// East Asian wide/fullwidth runes occupy two cells; other printable runes
// occupy one. Marks, variation selectors, emoji modifiers and join controls
// stay attached to their base cluster. Supported emoji presentation, keycap,
// flag and ZWJ clusters occupy two cells, while VS15 requests one text cell.
func truncateTextCells(text string, limit int) string {
	text = strings.ToValidUTF8(text, "\uFFFD")
	if limit <= 0 {
		return ""
	}
	if displayCells(text) <= limit {
		return text
	}
	markerCells := displayCells(truncationMarker)
	if limit <= markerCells {
		return takeDisplayCells(text, limit)
	}
	return takeDisplayCells(text, limit-markerCells) + truncationMarker
}

func displayCells(text string) int {
	cells := 0
	for len(text) > 0 {
		size, clusterCells := nextDisplayCluster(text)
		cells += clusterCells
		text = text[size:]
	}
	return cells
}

func takeDisplayCells(text string, limit int) string {
	used := 0
	end := 0
	for end < len(text) {
		size, cells := nextDisplayCluster(text[end:])
		if used+cells > limit {
			break
		}
		used += cells
		end += size
	}
	return text[:end]
}

func nextDisplayCluster(text string) (int, int) {
	base, size := utf8.DecodeRuneInString(text)
	clusterSize := size
	properties := displayClusterProperties{}
	if isRegionalIndicator(base) {
		properties.regionalIndicators = 1
	}

	for clusterSize < len(text) {
		next, nextSize := utf8.DecodeRuneInString(text[clusterSize:])
		switch {
		case isVariationSelector(next):
			properties.textVariation = properties.textVariation || next == '\ufe0e'
			properties.emojiVariation = properties.emojiVariation || next == '\ufe0f'
			clusterSize += nextSize
		case unicode.IsMark(next):
			properties.keycap = properties.keycap || next == '\u20e3'
			clusterSize += nextSize
		case isEmojiModifier(next):
			properties.emojiModifier = true
			clusterSize += nextSize
		case isEmojiTag(next):
			properties.emojiTag = true
			clusterSize += nextSize
		case properties.regionalIndicators == 1 && isRegionalIndicator(next):
			properties.regionalIndicators++
			clusterSize += nextSize
		case next == '\u200d':
			joinEnd := clusterSize + nextSize
			if joinEnd >= len(text) {
				return clusterSize, clusterCellWidth(base, properties)
			}
			_, joinedSize := utf8.DecodeRuneInString(text[joinEnd:])
			properties.joined = true
			clusterSize = joinEnd + joinedSize
		default:
			return clusterSize, clusterCellWidth(base, properties)
		}
	}
	return clusterSize, clusterCellWidth(base, properties)
}

func clusterCellWidth(base rune, properties displayClusterProperties) int {
	if unicode.IsControl(base) || unicode.IsMark(base) || isVariationSelector(base) || isEmojiModifier(base) || isEmojiTag(base) || base == '\u200d' {
		return 0
	}
	if properties.textVariation {
		return 1
	}
	if properties.emojiVariation || properties.emojiModifier || properties.emojiTag || properties.keycap || properties.joined || properties.regionalIndicators == 2 {
		return 2
	}
	kind := width.LookupRune(base).Kind()
	if kind == width.EastAsianWide || kind == width.EastAsianFullwidth {
		return 2
	}
	return 1
}

func isVariationSelector(r rune) bool {
	return r >= '\ufe00' && r <= '\ufe0f' || r >= '\U000e0100' && r <= '\U000e01ef'
}

func isEmojiModifier(r rune) bool {
	return r >= '\U0001f3fb' && r <= '\U0001f3ff'
}

func isRegionalIndicator(r rune) bool {
	return r >= '\U0001f1e6' && r <= '\U0001f1ff'
}

func isEmojiTag(r rune) bool {
	return r >= '\U000e0020' && r <= '\U000e007f'
}
