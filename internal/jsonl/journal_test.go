package jsonl

func writeAppendJournal(path string, journal appendJournal) error {
	return writeAppendJournalWithLayout(path, defaultLayout(path), journal)
}

func readAppendJournal(path string) (*appendJournal, error) {
	return readAppendJournalWithLayout(path, defaultLayout(path))
}

func rollbackAppendError(path string, journal *appendJournal, cause error) error {
	return rollbackAppendErrorWithLayout(path, defaultLayout(path), journal, cause)
}
