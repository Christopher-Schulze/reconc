package runtime

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"

	"reconc.dev/reconc/internal/boundedio"
)

const (
	maxEvidenceSnapshots     = 1024
	maxEvidenceSnapshotBytes = 16 << 20
)

// errEvidenceSnapshotChanged is returned when one logical evaluation observes
// a different file identity or metadata after a snapshot was cached. Reusing
// bytes from the earlier identity would make a replacement file appear to be
// the originally inspected evidence, so callers fail closed instead.
var errEvidenceSnapshotChanged = errors.New("evidence file snapshot changed during evaluation")

type evidenceFileSnapshot struct {
	path          string
	identity      string
	info          os.FileInfo
	exists        bool
	content       string
	contentLoaded bool
	contentDigest [32]byte
	err           error
}

type evidenceSnapshotCache struct {
	entries map[string]evidenceFileSnapshot
	order   []string
	bytes   int64
}

func newEvidenceSnapshotCache() *evidenceSnapshotCache {
	return &evidenceSnapshotCache{
		entries: make(map[string]evidenceFileSnapshot),
		order:   make([]string, 0, maxEvidenceSnapshots),
	}
}

// snapshot returns one stable metadata/content view for path during the
// current evaluation. Every cache hit revalidates the path identity and
// metadata; only the expensive bounded read and string conversion are reused.
func (c *evidenceSnapshotCache) snapshot(path string, needContent bool) (evidenceFileSnapshot, error) {
	if c == nil {
		return readEvidenceSnapshot(path, needContent)
	}
	if cached, ok := c.entries[path]; ok {
		current, exists, err := statEvidencePath(path)
		if err != nil {
			return evidenceFileSnapshot{}, err
		}
		if !sameEvidenceIdentity(cached.info, current, cached.exists, exists) {
			return evidenceFileSnapshot{}, fmt.Errorf("%w: %s", errEvidenceSnapshotChanged, path)
		}
		if cached.err != nil {
			return cached, cached.err
		}
		if !needContent || cached.contentLoaded || !cached.exists {
			return cached, nil
		}
		loaded, err := readEvidenceSnapshot(path, true)
		if err != nil {
			cached.err = err
			c.entries[path] = cached
			return cached, err
		}
		if !sameEvidenceIdentity(cached.info, loaded.info, cached.exists, loaded.exists) {
			return evidenceFileSnapshot{}, fmt.Errorf("%w: %s", errEvidenceSnapshotChanged, path)
		}
		cached.content = loaded.content
		cached.contentLoaded = true
		cached.contentDigest = loaded.contentDigest
		c.store(path, cached)
		return cached, nil
	}

	loaded, err := readEvidenceSnapshot(path, needContent)
	if err != nil {
		// A missing file is a useful stable negative result. Other stat errors
		// have no identity to revalidate and are deliberately not cached.
		if !loaded.exists || loaded.info != nil {
			loaded.err = err
			c.store(path, loaded)
		}
		return loaded, err
	}
	c.store(path, loaded)
	return loaded, nil
}

func readEvidenceSnapshot(path string, needContent bool) (evidenceFileSnapshot, error) {
	info, exists, err := statEvidencePath(path)
	if err != nil {
		if os.IsNotExist(err) {
			return evidenceFileSnapshot{path: path, exists: false}, nil
		}
		return evidenceFileSnapshot{path: path}, err
	}
	snapshot := evidenceFileSnapshot{path: path, identity: evidenceIdentity(info), info: info, exists: exists}
	if !exists {
		return snapshot, nil
	}
	if !info.Mode().IsRegular() {
		return snapshot, nil
	}
	if !needContent {
		return snapshot, nil
	}
	body, err := boundedio.ReadFile(path, maxEvidenceFileBytes)
	if err != nil {
		return snapshot, err
	}
	after, afterExists, err := statEvidencePath(path)
	if err != nil {
		return snapshot, err
	}
	if !sameEvidenceIdentity(info, after, true, afterExists) {
		return snapshot, fmt.Errorf("%w: %s", errEvidenceSnapshotChanged, path)
	}
	snapshot.content = string(body)
	snapshot.contentLoaded = true
	snapshot.contentDigest = sha256.Sum256(body)
	return snapshot, nil
}

func evidenceIdentity(info os.FileInfo) string {
	if info == nil {
		return ""
	}
	return fmt.Sprintf("%T:%#v", info.Sys(), info.Sys())
}

func statEvidencePath(path string) (os.FileInfo, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return info, true, nil
}

func sameEvidenceIdentity(left, right os.FileInfo, leftExists, rightExists bool) bool {
	if leftExists != rightExists {
		return false
	}
	if !leftExists {
		return true
	}
	return left != nil && right != nil && os.SameFile(left, right) &&
		left.Mode() == right.Mode() && left.Size() == right.Size() &&
		left.ModTime().Equal(right.ModTime())
}

func (c *evidenceSnapshotCache) store(path string, snapshot evidenceFileSnapshot) {
	if previous, ok := c.entries[path]; ok {
		if c.bytes-int64(len(previous.content))+int64(len(snapshot.content)) > maxEvidenceSnapshotBytes {
			snapshot.content = ""
			snapshot.contentLoaded = false
		}
		c.bytes -= int64(len(previous.content))
		c.entries[path] = snapshot
		c.bytes += int64(len(snapshot.content))
		return
	}
	for len(c.order) >= maxEvidenceSnapshots || c.bytes+int64(len(snapshot.content)) > maxEvidenceSnapshotBytes {
		if len(c.order) == 0 {
			break
		}
		oldest := c.order[0]
		c.order = c.order[1:]
		if previous, ok := c.entries[oldest]; ok {
			c.bytes -= int64(len(previous.content))
			delete(c.entries, oldest)
		}
	}
	c.entries[path] = snapshot
	c.order = append(c.order, path)
	c.bytes += int64(len(snapshot.content))
}
