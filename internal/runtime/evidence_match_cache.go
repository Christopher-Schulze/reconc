package runtime

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"strings"
)

const (
	maxEvidenceMatchMemoEntries  = maxEvidenceSnapshots
	maxEvidenceMatchMemoBytes    = 1 << 20
	maxMatchContextMemoEntries   = 4096
	maxMatchContextMemoBytes     = 4 << 20
	initialEvaluationMemoEntries = 8
)

// evidenceMatchOptions is the complete semantic input for one evidence-file
// assertion after its template path has been substituted. Keeping this shape
// explicit prevents a future option from accidentally sharing a result that
// it should not share.
type evidenceMatchOptions struct {
	file           string
	mustExist      bool
	mustContain    []string
	mustNotContain string
	maxLineCount   int
	optional       bool
}

type evidenceMatchKey struct {
	path          string
	identity      string
	exists        bool
	size          int64
	modTimeNanos  int64
	mode          os.FileMode
	contentDigest [32]byte
	options       evidenceOptionsKey
}

type evidenceOptionsKey struct {
	file           string
	mustContain    string
	mustNotContain string
	maxLineCount   int
	mustExist      bool
	optional       bool
}

type evidenceMatchResult struct {
	reasons []string
	err     error
}

type evidenceMatchMemo struct {
	entries map[evidenceMatchKey]evidenceMatchResult
	order   []evidenceMatchKey
	bytes   int
}

func newEvidenceMatchMemo() *evidenceMatchMemo {
	return &evidenceMatchMemo{}
}

func (m *evidenceMatchMemo) match(path string, snapshot evidenceFileSnapshot, options evidenceMatchOptions) evidenceMatchResult {
	if m == nil {
		return evaluateEvidenceSnapshot(path, snapshot, options)
	}
	key := evidenceMatchKey{
		path:          path,
		identity:      snapshot.identity,
		exists:        snapshot.exists,
		contentDigest: snapshot.contentDigest,
		options:       comparableEvidenceOptions(options),
	}
	if snapshot.info != nil {
		key.size = snapshot.info.Size()
		key.modTimeNanos = snapshot.info.ModTime().UnixNano()
		key.mode = snapshot.info.Mode()
	}
	if cached, ok := m.entries[key]; ok {
		return cloneEvidenceMatchResult(cached)
	}
	result := evaluateEvidenceSnapshot(path, snapshot, options)
	m.store(key, result)
	return cloneEvidenceMatchResult(result)
}

func evaluateEvidenceSnapshot(path string, snapshot evidenceFileSnapshot, options evidenceMatchOptions) evidenceMatchResult {
	displayPath := options.file
	if displayPath == "" {
		displayPath = path
	}
	if !snapshot.exists {
		if options.optional {
			return evidenceMatchResult{}
		}
		if options.mustExist {
			return evidenceMatchResult{reasons: []string{displayPath + ": file does not exist"}}
		}
		if evidenceNeedsContent(options) {
			return evidenceMatchResult{reasons: []string{displayPath + ": file does not exist (cannot check content)"}}
		}
		return evidenceMatchResult{}
	}
	if snapshot.info == nil || !snapshot.info.Mode().IsRegular() {
		return evidenceMatchResult{reasons: []string{displayPath + ": not a regular file"}}
	}
	if !evidenceNeedsContent(options) {
		return evidenceMatchResult{}
	}

	reasons := make([]string, 0, len(options.mustContain)+2)
	for _, required := range options.mustContain {
		if !strings.Contains(snapshot.content, required) {
			reasons = append(reasons, displayPath+": missing required substring "+quote(required))
		}
	}
	if options.mustNotContain != "" && strings.Contains(snapshot.content, options.mustNotContain) {
		reasons = append(reasons, displayPath+": contains forbidden substring "+quote(options.mustNotContain))
	}
	if options.maxLineCount > 0 {
		lines := strings.Count(snapshot.content, "\n")
		if !strings.HasSuffix(snapshot.content, "\n") && len(snapshot.content) > 0 {
			lines++
		}
		if lines > options.maxLineCount {
			reasons = append(reasons, fmt.Sprintf("%s: %d lines > max %d", displayPath, lines, options.maxLineCount))
		}
	}
	return evidenceMatchResult{reasons: reasons}
}

func evidenceNeedsContent(options evidenceMatchOptions) bool {
	return len(options.mustContain) > 0 || options.mustNotContain != "" || options.maxLineCount > 0
}

func (m *evidenceMatchMemo) store(key evidenceMatchKey, result evidenceMatchResult) {
	entryBytes := evidenceMatchResultBytes(result)
	if entryBytes > maxEvidenceMatchMemoBytes {
		return
	}
	if m.entries == nil {
		m.entries = make(map[evidenceMatchKey]evidenceMatchResult, initialEvaluationMemoEntries)
		m.order = make([]evidenceMatchKey, 0, initialEvaluationMemoEntries)
	}
	for len(m.order) >= maxEvidenceMatchMemoEntries || m.bytes+entryBytes > maxEvidenceMatchMemoBytes {
		if len(m.order) == 0 {
			break
		}
		oldest := m.order[0]
		m.order = m.order[1:]
		if previous, ok := m.entries[oldest]; ok {
			m.bytes -= evidenceMatchResultBytes(previous)
			delete(m.entries, oldest)
		}
	}
	m.entries[key] = cloneEvidenceMatchResult(result)
	m.order = append(m.order, key)
	m.bytes += entryBytes
}

func evidenceMatchResultBytes(result evidenceMatchResult) int {
	bytes := 0
	for _, reason := range result.reasons {
		bytes += len(reason)
	}
	if result.err != nil {
		bytes += len(result.err.Error())
	}
	return bytes
}

func cloneEvidenceMatchResult(result evidenceMatchResult) evidenceMatchResult {
	result.reasons = append([]string(nil), result.reasons...)
	return result
}

type matchContextMemoKey struct {
	writes   [32]byte
	patterns [32]byte
}

type matchContextMemoEntry struct {
	contexts []matchContext
	err      error
	bytes    int
}

type matchContextMemo struct {
	entries map[matchContextMemoKey]matchContextMemoEntry
	order   []matchContextMemoKey
	bytes   int
}

func newMatchContextMemo() *matchContextMemo {
	return &matchContextMemo{}
}

func (m *matchContextMemo) collect(matchers *runtimeTemplateMatchers, writes, patterns []string) ([]matchContext, error) {
	if m == nil {
		return collectMatchContextsWithMatchers(matchers, writes, patterns)
	}
	key := matchContextMemoKey{writes: digestStrings(writes), patterns: digestStrings(patterns)}
	if cached, ok := m.entries[key]; ok {
		return cloneMatchContexts(cached.contexts), cached.err
	}
	contexts, err := collectMatchContextsWithMatchers(matchers, writes, patterns)
	entryBytes := matchContextMemoEntryBytes(key, contexts, err)
	if entryBytes > maxMatchContextMemoBytes {
		return contexts, err
	}
	if m.entries == nil {
		m.entries = make(map[matchContextMemoKey]matchContextMemoEntry, initialEvaluationMemoEntries)
		m.order = make([]matchContextMemoKey, 0, initialEvaluationMemoEntries)
	}
	for len(m.order) >= maxMatchContextMemoEntries || m.bytes+entryBytes > maxMatchContextMemoBytes {
		if len(m.order) == 0 {
			break
		}
		oldest := m.order[0]
		m.order = m.order[1:]
		if previous, ok := m.entries[oldest]; ok {
			m.bytes -= previous.bytes
			delete(m.entries, oldest)
		}
	}
	m.entries[key] = matchContextMemoEntry{contexts: cloneMatchContexts(contexts), err: err, bytes: entryBytes}
	m.order = append(m.order, key)
	m.bytes += entryBytes
	return cloneMatchContexts(contexts), err
}

func matchContextMemoEntryBytes(key matchContextMemoKey, contexts []matchContext, err error) int {
	// The budget charges every retained digest byte, slice slot, string byte,
	// capture key/value pair, map entry allowance, and cached error text. The
	// same context payload is charged twice because storage and defensive
	// return cloning can coexist during a hit.
	const (
		keyBytes          = 64
		contextSlotBytes  = 40
		captureEntryBytes = 48
	)
	bytes := keyBytes
	for _, context := range contexts {
		contextBytes := contextSlotBytes + len(context.path) + len(context.pattern)
		for name, value := range context.captures {
			contextBytes += captureEntryBytes + len(name) + len(value)
		}
		bytes += 2 * contextBytes
	}
	if err != nil {
		bytes += 16 + len(err.Error())
	}
	return bytes
}

func cloneMatchContexts(contexts []matchContext) []matchContext {
	if contexts == nil {
		return nil
	}
	cloned := make([]matchContext, len(contexts))
	for index, context := range contexts {
		cloned[index] = context
		cloned[index].captures = cloneStringMap(context.captures)
	}
	return cloned
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func comparableEvidenceOptions(options evidenceMatchOptions) evidenceOptionsKey {
	return evidenceOptionsKey{
		file: options.file, mustContain: lengthDelimitedStrings(options.mustContain),
		mustNotContain: options.mustNotContain, maxLineCount: options.maxLineCount,
		mustExist: options.mustExist, optional: options.optional,
	}
}

func lengthDelimitedStrings(values []string) string {
	var encoded strings.Builder
	for _, value := range values {
		encoded.WriteString(strconv.Itoa(len(value)))
		encoded.WriteByte(':')
		encoded.WriteString(value)
	}
	return encoded.String()
}

func digestStrings(values []string) [32]byte {
	hash := sha256.New()
	var length [8]byte
	for _, value := range values {
		binary.BigEndian.PutUint64(length[:], uint64(len(value)))
		hash.Write(length[:])
		hash.Write([]byte(value))
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest
}
