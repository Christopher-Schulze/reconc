package atomicfile

// PublicationOutcome describes how far an atomic-file mutation progressed.
// A non-nil error can still carry a published outcome when a post-publication
// validation, cleanup, synchronization, or close step failed.
type PublicationOutcome uint8

const (
	PublicationNotPublished PublicationOutcome = iota
	PublicationPublishedUncertain
	PublicationDurablyPublished
)

// PublicationResult is returned by every mutating atomic-file operation.
// Changed is true for a replacement or a successful mode repair. Outcome is
// NotPublished for an unchanged target and for failures before publication.
type PublicationResult struct {
	Outcome PublicationOutcome
	Changed bool
}

func (result *PublicationResult) markPublished() {
	result.Changed = true
	result.Outcome = PublicationPublishedUncertain
}

func (result *PublicationResult) markDurable() {
	result.Changed = true
	result.Outcome = PublicationDurablyPublished
}

func (result *PublicationResult) markUncertainOnClose(closeErr error) error {
	if closeErr != nil && result.Outcome == PublicationDurablyPublished {
		result.Outcome = PublicationPublishedUncertain
	}
	return closeErr
}
