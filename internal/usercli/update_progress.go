package usercli

import (
	"fmt"
	"io"
)

// UpdatePhase names one observable phase of `reconc update`, in execution order.
type UpdatePhase string

const (
	UpdatePhaseInstallation UpdatePhase = "installation"
	UpdatePhaseRelease      UpdatePhase = "release"
	UpdatePhaseSkill        UpdatePhase = "skill"
	UpdatePhaseDownload     UpdatePhase = "download"
	UpdatePhaseProvenance   UpdatePhase = "provenance"
	UpdatePhaseSmokeTest    UpdatePhase = "smoke-test"
	UpdatePhasePublish      UpdatePhase = "publish"
)

// UpdateEventKind distinguishes a phase start, byte progress, and success.
// A phase that started without a pass event ended with the returned report's
// refusal or failure.
type UpdateEventKind string

const (
	UpdateEventStart    UpdateEventKind = "start"
	UpdateEventProgress UpdateEventKind = "progress"
	UpdateEventPass     UpdateEventKind = "pass"
)

// UpdateEvent reports update progress to an optional observer. Done and Total
// count bytes; Total is also set on the download pass event.
type UpdateEvent struct {
	Phase  UpdatePhase
	Kind   UpdateEventKind
	Detail string
	Done   int64
	Total  int64
}

type updateObserver func(UpdateEvent)

func (observe updateObserver) start(phase UpdatePhase) {
	if observe != nil {
		observe(UpdateEvent{Phase: phase, Kind: UpdateEventStart})
	}
}

func (observe updateObserver) pass(phase UpdatePhase, detail string) {
	if observe != nil {
		observe(UpdateEvent{Phase: phase, Kind: UpdateEventPass, Detail: detail})
	}
}

// passBytes reports success of a phase that transferred total bytes.
func (observe updateObserver) passBytes(phase UpdatePhase, detail string, total int64) {
	if observe != nil {
		observe(UpdateEvent{Phase: phase, Kind: UpdateEventPass, Detail: detail, Total: total})
	}
}

// progressReader reports the bytes read so far for one phase.
func (observe updateObserver) progressReader(phase UpdatePhase, source io.Reader, total int64) io.Reader {
	if observe == nil {
		return source
	}
	return &observedReader{reader: source, observe: observe, phase: phase, total: total}
}

type observedReader struct {
	reader  io.Reader
	observe updateObserver
	phase   UpdatePhase
	done    int64
	total   int64
}

func (reader *observedReader) Read(buffer []byte) (int, error) {
	count, err := reader.reader.Read(buffer)
	if count > 0 {
		reader.done += int64(count)
		reader.observe(UpdateEvent{Phase: reader.phase, Kind: UpdateEventProgress, Done: reader.done, Total: reader.total})
	}
	return count, err
}

func installationPhaseDetail(diagnostic *GlobalDiagnostic) string {
	if diagnostic.BinaryPath == nil {
		return string(*diagnostic.Owner)
	}
	return string(*diagnostic.Owner) + " · " + *diagnostic.BinaryPath
}

func releasePhaseDetail(release selectedRelease) string {
	if release.localDir != "" {
		return fmt.Sprintf("%s from %s · manifest and SHA256SUMS verified", release.manifest.Tag, release.localDir)
	}
	return release.manifest.Tag + " · manifest and SHA256SUMS verified"
}

// Describe returns the short human wording of a skill update state.
func (state SkillUpdateState) Describe() string {
	switch state {
	case SkillCurrent:
		return "current"
	case SkillStaleOwned:
		return "update available"
	case SkillMissing:
		return "not installed"
	case SkillUnmanaged:
		return "unmanaged directory"
	case SkillModifiedOwned:
		return "modified locally"
	case SkillUnavailable:
		return "not shipped by the selected release"
	default:
		return string(state)
	}
}

func provenancePhaseDetail(state ProvenanceState) string {
	if state == ProvenanceGitHubVerified {
		return "GitHub build-provenance attestation verified"
	}
	return "custom verifier passed; checksum and embedded identity verified"
}
