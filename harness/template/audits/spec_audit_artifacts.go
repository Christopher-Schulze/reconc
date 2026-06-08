package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type specAuditRange struct {
	Start int
	End   int
	Text  string
}

type specAuditAtom struct {
	ID                     string
	SpecLines              []specAuditRange
	ResearchRefs           []string
	SpecStatus             string
	ResearchStatus         string
	ImplementationStatus   string
	TestStatus             string
	QualityBarStatus       string
	ImplementationEvidence string
	Verdict                string
	GapIDs                 []string
}

type specAuditResearchFloor struct {
	ID            string
	SourceRef     string
	LinkedAtomIDs []string
	Decision      string
	GapIDs        []string
}

func auditSpecAuditArtifacts(root string) []string {
	if !exists(filepath.Join(root, "docs/spec-audit/state.md")) {
		return nil
	}
	active, activeFailures := specAuditWorkflowActive(root)
	if len(activeFailures) > 0 {
		return activeFailures
	}
	if !active {
		return nil
	}

	var failures []string
	specBytes, err := os.ReadFile(filepath.Join(root, "docs/spec.md"))
	if err != nil {
		return []string{fmt.Sprintf("read docs/spec.md: %v", err)}
	}
	specLineCount := lineCount(string(specBytes))

	stateBytes, err := os.ReadFile(filepath.Join(root, "docs/spec-audit/state.md"))
	if err != nil {
		return []string{fmt.Sprintf("read docs/spec-audit/state.md: %v", err)}
	}
	state := string(stateBytes)
	stateLineCount, ok := parseStateInt(state, "Spec Line Count")
	if !ok {
		failures = append(failures, "docs/spec-audit/state.md: missing Spec Line Count")
	} else if stateLineCount != specLineCount {
		failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md: Spec Line Count %d does not match docs/spec.md line count %d", stateLineCount, specLineCount))
	}

	atoms, atomFailures := readSpecAuditAtoms(root, specLineCount)
	failures = append(failures, atomFailures...)
	researchFloors, researchFailures := readSpecAuditResearchFloors(root)
	failures = append(failures, researchFailures...)
	gaps, gapFailures := readSpecAuditGaps(root)
	failures = append(failures, gapFailures...)

	completedRanges, completedFailures := readCompletedSpecAuditRanges(state, specLineCount)
	failures = append(failures, completedFailures...)
	failures = append(failures, auditSpecAuditClaims(root, state, specLineCount)...)
	failures = append(failures, auditSpecAuditRangeArtifacts(root, completedRanges, atoms)...)
	failures = append(failures, auditSpecAuditAtomCoverage(completedRanges, atoms, gaps)...)
	failures = append(failures, auditSpecAuditResearchCoverage(atoms, researchFloors, gaps)...)
	failures = append(failures, auditSpecAuditEvidenceTargets(root, atoms, researchFloors)...)
	failures = append(failures, auditSpecAuditStateProgress(state, completedRanges, atoms)...)
	return failures
}

func specAuditWorkflowActive(root string) (bool, []string) {
	stateBytes, err := os.ReadFile(filepath.Join(root, "docs/spec-audit/state.md"))
	if err != nil {
		return false, []string{fmt.Sprintf("read docs/spec-audit/state.md: %v", err)}
	}
	state := string(stateBytes)
	status := strings.ToUpper(strings.TrimSpace(parseStateValue(state, "Status")))
	if status != "" && status != "NOT_STARTED" && status != "PENDING" {
		return true, nil
	}
	for _, heading := range []string{"## Active Claims", "## Completed Ranges", "## Blocked Ranges"} {
		if len(markdownTableRowsInSection(state, heading)) > 0 {
			return true, nil
		}
	}
	if hasSpecAuditFiles(filepath.Join(root, "docs/spec-audit/claims")) || hasSpecAuditFiles(filepath.Join(root, "docs/spec-audit/ranges")) {
		return true, nil
	}
	if hasChangedSpecAuditEvidence(root) {
		return true, nil
	}
	return currentTaskIsSpecAuditRange(root)
}

func currentTaskIsSpecAuditRange(root string) (bool, []string) {
	tasksBytes, err := os.ReadFile(filepath.Join(root, "docs/tasks.md"))
	if err != nil {
		return false, nil
	}
	index, failures := parseTaskIndex(string(tasksBytes))
	if len(failures) > 0 || index.currentTarget == "" {
		return false, nil
	}
	detailBytes, err := os.ReadFile(filepath.Join(root, "docs", filepath.FromSlash(index.currentTarget)))
	if err != nil {
		return false, nil
	}
	_, _, _, _, touchSurfaces, _, _, specLinesRaw, _, completionClaim := parseSchedulingFields(extractMarkdownSection(string(detailBytes), "## Scheduling"))
	if !strings.Contains(specLinesRaw, "docs/spec.md:L") {
		return false, nil
	}
	if strings.Contains(strings.ToLower(completionClaim), "audit") || touchSurfacesContain(touchSurfaces, "docs/spec-audit") {
		return true, nil
	}
	return false, nil
}

func hasChangedSpecAuditEvidence(root string) bool {
	files, failures := collectGitDiffFiles(root)
	if len(failures) > 0 {
		return false
	}
	for path := range files {
		clean := filepath.ToSlash(path)
		if strings.HasPrefix(clean, "docs/spec-audit/claims/") ||
			strings.HasPrefix(clean, "docs/spec-audit/ranges/") ||
			clean == "docs/spec-audit/spec-atoms.md" ||
			clean == "docs/spec-audit/research-floor.md" ||
			clean == "docs/spec-audit/gaps.md" {
			return true
		}
	}
	return false
}

func hasSpecAuditFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "README.md" || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if strings.HasSuffix(entry.Name(), ".md") {
			return true
		}
	}
	return false
}

func touchSurfacesContain(surfaces []string, prefix string) bool {
	for _, surface := range surfaces {
		if strings.HasPrefix(normalizeTouchSurface(surface), prefix) {
			return true
		}
	}
	return false
}

func readSpecAuditAtoms(root string, specLineCount int) (map[string]specAuditAtom, []string) {
	path := filepath.Join(root, "docs/spec-audit/spec-atoms.md")
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, []string{fmt.Sprintf("read docs/spec-audit/spec-atoms.md: %v", err)}
	}
	table, failures := markdownTableInSection(string(contentBytes), "## Atoms")
	atoms := map[string]specAuditAtom{}
	for rowNo, row := range table {
		atom := specAuditAtom{
			ID:                     row["Atom ID"],
			ResearchRefs:           parseSpecAuditList(row["Research Refs"]),
			SpecStatus:             row["Spec Status"],
			ResearchStatus:         row["Research Status"],
			ImplementationStatus:   row["Implementation Status"],
			TestStatus:             row["Test Status"],
			QualityBarStatus:       row["Quality Bar Status"],
			ImplementationEvidence: row["Implementation Evidence"],
			Verdict:                row["Verdict"],
			GapIDs:                 parseSpecAuditList(row["Gap IDs"]),
		}
		if atom.ID == "" {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md row %d: Atom ID is empty", rowNo+1))
			continue
		}
		if !regexp.MustCompile(`^ATOM-L[0-9]{4}-[0-9]{2,}$`).MatchString(atom.ID) {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md row %d: invalid Atom ID %q", rowNo+1, atom.ID))
		}
		ranges, rangeFailures := parseSpecAuditRanges(row["Spec Lines"], specLineCount)
		if len(rangeFailures) > 0 {
			for _, failure := range rangeFailures {
				failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md row %d atom %s: %s", rowNo+1, atom.ID, failure))
			}
		}
		atom.SpecLines = ranges
		if existing, ok := atoms[atom.ID]; ok {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md: duplicate Atom ID %s also covers %s", atom.ID, formatSpecAuditRanges(existing.SpecLines)))
		}
		failures = append(failures, auditSpecAuditAtomRow(rowNo+1, atom)...)
		atoms[atom.ID] = atom
	}
	return atoms, failures
}

func auditSpecAuditAtomRow(rowNo int, atom specAuditAtom) []string {
	var failures []string
	statuses := map[string]string{
		"Spec Status":           atom.SpecStatus,
		"Research Status":       atom.ResearchStatus,
		"Implementation Status": atom.ImplementationStatus,
		"Test Status":           atom.TestStatus,
		"Quality Bar Status":    atom.QualityBarStatus,
	}
	for name, value := range statuses {
		if strings.TrimSpace(value) == "" || strings.EqualFold(value, "pending") {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md row %d atom %s: %s must be explicit, not empty/pending", rowNo, atom.ID, name))
		}
	}
	verdict := strings.TrimSpace(atom.Verdict)
	if !validSpecAuditVerdict(verdict) {
		failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md row %d atom %s: invalid Verdict %q", rowNo, atom.ID, verdict))
		return failures
	}
	if verdict == "MATCH" || verdict == "EXCEEDS" {
		for name, value := range statuses {
			if strings.TrimSpace(value) != "PASS" {
				failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md row %d atom %s: %s must be PASS for %s verdict", rowNo, atom.ID, name, verdict))
			}
		}
		if emptyAuditEvidence(atom.ImplementationEvidence) {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md row %d atom %s: %s verdict requires implementation evidence", rowNo, atom.ID, verdict))
		}
	}
	if !passingSpecAuditVerdict(verdict) && len(atom.GapIDs) == 0 {
		failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md row %d atom %s: non-passing verdict %s requires Gap IDs", rowNo, atom.ID, verdict))
	}
	for _, gapID := range atom.GapIDs {
		if !regexp.MustCompile(`^GAP-L[0-9]{4}-[0-9]{2,}$`).MatchString(gapID) {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md row %d atom %s: invalid Gap ID %q", rowNo, atom.ID, gapID))
		}
	}
	return failures
}

func readSpecAuditResearchFloors(root string) (map[string]specAuditResearchFloor, []string) {
	contentBytes, err := os.ReadFile(filepath.Join(root, "docs/spec-audit/research-floor.md"))
	if err != nil {
		return nil, []string{fmt.Sprintf("read docs/spec-audit/research-floor.md: %v", err)}
	}
	table, failures := markdownTableInSection(string(contentBytes), "## Research Floors")
	floors := map[string]specAuditResearchFloor{}
	for rowNo, row := range table {
		floor := specAuditResearchFloor{
			ID:            row["Research Floor ID"],
			SourceRef:     row["Source Ref"],
			LinkedAtomIDs: parseSpecAuditList(row["Linked Atom IDs"]),
			Decision:      row["Carry-Over Decision"],
			GapIDs:        parseSpecAuditList(row["Gap IDs"]),
		}
		if floor.ID == "" {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/research-floor.md row %d: Research Floor ID is empty", rowNo+1))
			continue
		}
		if !regexp.MustCompile(`^RF-L[0-9]{4}-[0-9]{2,}$`).MatchString(floor.ID) {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/research-floor.md row %d: invalid Research Floor ID %q", rowNo+1, floor.ID))
		}
		if !strings.HasPrefix(floor.SourceRef, "research/") {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/research-floor.md row %d floor %s: Source Ref must start with research/", rowNo+1, floor.ID))
		}
		if len(floor.LinkedAtomIDs) == 0 {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/research-floor.md row %d floor %s: Linked Atom IDs is empty", rowNo+1, floor.ID))
		}
		if !validCarryDecision(floor.Decision) {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/research-floor.md row %d floor %s: invalid Carry-Over Decision %q", rowNo+1, floor.ID, floor.Decision))
		}
		if floor.Decision == "GAP" && len(floor.GapIDs) == 0 {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/research-floor.md row %d floor %s: GAP carry-over decision requires Gap IDs", rowNo+1, floor.ID))
		}
		floors[floor.ID] = floor
	}
	return floors, failures
}

func readSpecAuditGaps(root string) (map[string]string, []string) {
	contentBytes, err := os.ReadFile(filepath.Join(root, "docs/spec-audit/gaps.md"))
	if err != nil {
		return nil, []string{fmt.Sprintf("read docs/spec-audit/gaps.md: %v", err)}
	}
	gaps := map[string]string{}
	re := regexp.MustCompile(`^### (GAP-L[0-9]{4}-[0-9]{2,}):`)
	lines := strings.Split(string(contentBytes), "\n")
	for i := 0; i < len(lines); i++ {
		match := re.FindStringSubmatch(lines[i])
		if match == nil {
			continue
		}
		id := match[1]
		var body []string
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], "### GAP-") || strings.HasPrefix(lines[j], "## ") {
				break
			}
			body = append(body, lines[j])
		}
		gaps[id] = strings.Join(body, "\n")
	}
	return gaps, nil
}

func readCompletedSpecAuditRanges(state string, specLineCount int) ([]specAuditRange, []string) {
	rows := markdownTableRowsInSection(state, "## Completed Ranges")
	var ranges []specAuditRange
	var failures []string
	for rowNo, row := range rows {
		specRange := row["Spec Range"]
		parsed, rangeFailures := parseSpecAuditRanges(specRange, specLineCount)
		if len(rangeFailures) > 0 || len(parsed) != 1 {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md Completed Ranges row %d: invalid Spec Range %q", rowNo+1, specRange))
			continue
		}
		if row["Completed By"] == "" || row["Completed By"] == "-" {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md Completed Ranges row %d %s: Completed By is empty", rowNo+1, specRange))
		}
		if row["Completed At"] == "" || row["Completed At"] == "-" {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md Completed Ranges row %d %s: Completed At is empty", rowNo+1, specRange))
		}
		for _, countColumn := range []string{"Atom Count", "Research Refs Read", "Gap Count"} {
			if _, err := strconv.Atoi(strings.TrimSpace(row[countColumn])); err != nil {
				failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md Completed Ranges row %d %s: %s must be numeric", rowNo+1, specRange, countColumn))
			}
		}
		ranges = append(ranges, parsed[0])
	}
	return ranges, failures
}

func auditSpecAuditClaims(root string, state string, specLineCount int) []string {
	var failures []string
	var claimed []specAuditRange
	for rowNo, row := range markdownTableRowsInSection(state, "## Active Claims") {
		claimID := row["Claim ID"]
		if !regexp.MustCompile(`^CLAIM-[0-9]{8}T[0-9]{6}Z-[A-Za-z0-9_.-]+-[A-Za-z0-9_.-]+-L[0-9]{4}-L[0-9]{4}$`).MatchString(claimID) {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md Active Claims row %d: invalid Claim ID %q", rowNo+1, claimID))
		}
		ranges, rangeFailures := parseSpecAuditRanges(row["Spec Range"], specLineCount)
		if len(rangeFailures) > 0 || len(ranges) != 1 {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md Active Claims row %d: invalid Spec Range %q", rowNo+1, row["Spec Range"]))
			continue
		}
		if rangeOverlapsAny(ranges[0], claimed) {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md Active Claims row %d: overlapping active claim range %s", rowNo+1, ranges[0].Text))
		}
		claimed = append(claimed, ranges[0])
		if !validClaimStatus(row["Status"]) {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md Active Claims row %d %s: invalid Status %q", rowNo+1, claimID, row["Status"]))
		}
		if !validClaimPhase(row["Phase"]) {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md Active Claims row %d %s: invalid Phase %q", rowNo+1, claimID, row["Phase"]))
		}
		claimPath := filepath.Join(root, "docs/spec-audit/claims", claimID+".md")
		if !exists(claimPath) {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md Active Claims row %d %s: missing claim file docs/spec-audit/claims/%s.md", rowNo+1, claimID, claimID))
		}
		if !exists(filepath.Join(root, "docs/spec-audit/ranges", specAuditRangeArtifactName(ranges[0]))) {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md Active Claims row %d %s: missing range artifact docs/spec-audit/ranges/%s", rowNo+1, claimID, specAuditRangeArtifactName(ranges[0])))
		}
	}
	return failures
}

func auditSpecAuditRangeArtifacts(root string, completedRanges []specAuditRange, atoms map[string]specAuditAtom) []string {
	var failures []string
	for _, auditRange := range completedRanges {
		artifactName := specAuditRangeArtifactName(auditRange)
		artifactPath := filepath.Join(root, "docs/spec-audit/ranges", artifactName)
		contentBytes, err := os.ReadFile(artifactPath)
		if err != nil {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/ranges/%s: completed range artifact missing or unreadable: %v", artifactName, err))
			continue
		}
		content := string(contentBytes)
		for _, token := range []string{"Range Reality Check", "Atom Table", "Spec Lines Read", "Implementation Evidence", "Gaps"} {
			if !strings.Contains(content, token) {
				failures = append(failures, fmt.Sprintf("docs/spec-audit/ranges/%s: missing required range artifact token %q", artifactName, token))
			}
		}
		for _, atom := range atomsForRange(auditRange, atoms) {
			if !strings.Contains(content, atom.ID) {
				failures = append(failures, fmt.Sprintf("docs/spec-audit/ranges/%s: missing detailed evidence for atom %s", artifactName, atom.ID))
			}
		}
	}
	return failures
}

func auditSpecAuditAtomCoverage(completedRanges []specAuditRange, atoms map[string]specAuditAtom, gaps map[string]string) []string {
	var failures []string
	for _, auditRange := range completedRanges {
		atomsInRange := atomsForRange(auditRange, atoms)
		if len(atomsInRange) == 0 {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md: completed range %s has no atom rows", auditRange.Text))
			continue
		}
		for line := auditRange.Start; line <= auditRange.End; line++ {
			if !lineCoveredByAtoms(line, atomsInRange) {
				failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md: completed range %s missing atom coverage for docs/spec.md:L%d", auditRange.Text, line))
			}
		}
		for _, atom := range atomsInRange {
			expectedPrefix := fmt.Sprintf("ATOM-L%04d-", auditRange.Start)
			if !strings.HasPrefix(atom.ID, expectedPrefix) {
				failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md atom %s: completed range %s requires range-derived prefix %s", atom.ID, auditRange.Text, expectedPrefix))
			}
			for _, gapID := range atom.GapIDs {
				record, ok := gaps[gapID]
				if !ok {
					failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md atom %s: Gap ID %s missing from docs/spec-audit/gaps.md", atom.ID, gapID))
					continue
				}
				failures = append(failures, auditSpecAuditGapRecord(gapID, record)...)
			}
		}
	}
	return failures
}

func auditSpecAuditResearchCoverage(atoms map[string]specAuditAtom, floors map[string]specAuditResearchFloor, gaps map[string]string) []string {
	var failures []string
	for _, atom := range atoms {
		for _, ref := range atom.ResearchRefs {
			if ref == "" || ref == "none" || ref == "-" {
				continue
			}
			if !strings.HasPrefix(ref, "research/") {
				failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md atom %s: Research Refs contains invalid ref %q", atom.ID, ref))
				continue
			}
			if !researchRefCoveredByFloor(ref, atom.ID, floors) {
				failures = append(failures, fmt.Sprintf("docs/spec-audit/research-floor.md: missing research floor row for atom %s ref %s", atom.ID, ref))
			}
		}
	}
	for _, floor := range floors {
		for _, atomID := range floor.LinkedAtomIDs {
			if _, ok := atoms[atomID]; !ok {
				failures = append(failures, fmt.Sprintf("docs/spec-audit/research-floor.md floor %s: linked atom %s does not exist", floor.ID, atomID))
			}
		}
		for _, gapID := range floor.GapIDs {
			if _, ok := gaps[gapID]; !ok {
				failures = append(failures, fmt.Sprintf("docs/spec-audit/research-floor.md floor %s: Gap ID %s missing from docs/spec-audit/gaps.md", floor.ID, gapID))
			}
		}
	}
	return failures
}

func auditSpecAuditEvidenceTargets(root string, atoms map[string]specAuditAtom, floors map[string]specAuditResearchFloor) []string {
	var failures []string
	for _, atom := range atoms {
		if atom.Verdict != "MATCH" && atom.Verdict != "EXCEEDS" {
			continue
		}
		refs := parseSpecAuditFileLineRefs(atom.ImplementationEvidence)
		if len(refs) == 0 {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/spec-atoms.md atom %s: passing verdict requires repo-relative file:Lx implementation evidence", atom.ID))
			continue
		}
		for _, ref := range refs {
			if failure := auditRepoFileLineRef(root, ref, "implementation evidence for atom "+atom.ID); failure != "" {
				failures = append(failures, failure)
			}
		}
	}
	for _, floor := range floors {
		if floor.SourceRef == "" {
			continue
		}
		ref := specAuditFileLineRef{Path: strings.Split(floor.SourceRef, ":L")[0]}
		if strings.Contains(floor.SourceRef, ":L") {
			refs := parseSpecAuditFileLineRefs(floor.SourceRef)
			if len(refs) > 0 {
				ref = refs[0]
			}
		}
		if !strings.HasPrefix(ref.Path, "research/") {
			continue
		}
		if failure := auditRepoFileLineRef(root, ref, "research floor "+floor.ID); failure != "" {
			failures = append(failures, failure)
		}
	}
	return failures
}

func auditSpecAuditStateProgress(state string, completedRanges []specAuditRange, atoms map[string]specAuditAtom) []string {
	var failures []string
	lastRaw := parseStateValue(state, "Last Fully Verified Line")
	lastLine := parseSpecAuditLineNumber(lastRaw)
	if lastLine == 0 {
		if len(completedRanges) > 0 {
			failures = append(failures, "docs/spec-audit/state.md: Last Fully Verified Line must advance after completed ranges")
		}
		return failures
	}
	contiguous := contiguousCompletedSpecLine(completedRanges, atoms)
	if lastLine > contiguous {
		failures = append(failures, fmt.Sprintf("docs/spec-audit/state.md: Last Fully Verified Line %d exceeds contiguous completed coverage %d", lastLine, contiguous))
	}
	return failures
}

func auditSpecAuditGapRecord(gapID string, record string) []string {
	var failures []string
	required := []string{
		"Severity", "Atom IDs", "Spec evidence", "Current code evidence", "Exact missing detail",
		"Why current code is insufficient", "Minimum spec/research parity required", "Target adaptation",
		"Required tests", "Acceptance criteria", "Close condition",
	}
	for _, field := range required {
		value := parseColonField(record, "- "+field)
		if value == "" || value == "-" || strings.EqualFold(value, "pending") {
			failures = append(failures, fmt.Sprintf("docs/spec-audit/gaps.md %s: field %q is empty", gapID, field))
		}
	}
	return failures
}

func parseSpecAuditRanges(value string, specLineCount int) ([]specAuditRange, []string) {
	var ranges []specAuditRange
	var failures []string
	for _, ref := range parseCSVFields(value) {
		start, end, ok := parseSpecLineRef(ref)
		if !ok {
			failures = append(failures, fmt.Sprintf("invalid spec line ref %q", ref))
			continue
		}
		if start < 1 || end > specLineCount {
			failures = append(failures, fmt.Sprintf("spec line ref %q outside docs/spec.md:L1-L%d", ref, specLineCount))
			continue
		}
		ranges = append(ranges, specAuditRange{Start: start, End: end, Text: fmt.Sprintf("docs/spec.md:L%d-L%d", start, end)})
	}
	if len(ranges) == 0 {
		failures = append(failures, "missing spec line ref")
	}
	return ranges, failures
}

func parseSpecAuditList(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || value == "-" || strings.EqualFold(value, "pending") {
		return nil
	}
	value = strings.ReplaceAll(value, "<br>", ",")
	value = strings.ReplaceAll(value, ";", ",")
	return parseCSVFields(value)
}

func markdownTableInSection(content string, heading string) ([]map[string]string, []string) {
	section := extractMarkdownSection(content, heading)
	if strings.TrimSpace(section) == "" {
		return nil, []string{fmt.Sprintf("%s missing or empty", heading)}
	}
	lines := strings.Split(section, "\n")
	var headers []string
	var rows []map[string]string
	var failures []string
	for _, line := range lines {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := splitMarkdownTableLine(line)
		if len(cells) == 0 || markdownSeparatorRow(cells) {
			continue
		}
		if headers == nil {
			headers = cells
			continue
		}
		if len(cells) != len(headers) {
			failures = append(failures, fmt.Sprintf("%s table row has %d cells, expected %d", heading, len(cells), len(headers)))
			continue
		}
		row := map[string]string{}
		for i, header := range headers {
			row[header] = cells[i]
		}
		rows = append(rows, row)
	}
	if headers == nil {
		failures = append(failures, fmt.Sprintf("%s table missing header", heading))
	}
	return rows, failures
}

func markdownTableRowsInSection(content string, heading string) []map[string]string {
	rows, _ := markdownTableInSection(content, heading)
	return rows
}

func splitMarkdownTableLine(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	cells := make([]string, 0, len(parts))
	for _, part := range parts {
		cells = append(cells, strings.TrimSpace(part))
	}
	return cells
}

func markdownSeparatorRow(cells []string) bool {
	for _, cell := range cells {
		trimmed := strings.TrimSpace(cell)
		if trimmed == "" {
			return false
		}
		for _, ch := range trimmed {
			if ch != '-' && ch != ':' {
				return false
			}
		}
	}
	return true
}

func extractMarkdownSection(content string, heading string) string {
	lines := strings.Split(content, "\n")
	var body []string
	inSection := false
	level := markdownHeadingLevel(heading)
	for _, line := range lines {
		if strings.TrimSpace(line) == heading {
			inSection = true
			continue
		}
		if inSection && markdownHeadingLevel(line) > 0 && markdownHeadingLevel(line) <= level {
			break
		}
		if inSection {
			body = append(body, line)
		}
	}
	return strings.Join(body, "\n")
}

func markdownHeadingLevel(line string) int {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "#") {
		return 0
	}
	level := 0
	for _, ch := range trimmed {
		if ch != '#' {
			break
		}
		level++
	}
	if level == 0 || level > 6 || len(trimmed) <= level || trimmed[level] != ' ' {
		return 0
	}
	return level
}

func parseStateInt(content string, field string) (int, bool) {
	value := parseStateValue(content, field)
	if value == "" {
		return 0, false
	}
	n, err := strconv.Atoi(value)
	return n, err == nil
}

func parseStateValue(content string, field string) string {
	return parseColonField(content, "- "+field)
}

func parseColonField(content string, prefix string) string {
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, prefix+":") {
			return strings.TrimSpace(strings.TrimPrefix(trimmed, prefix+":"))
		}
	}
	return ""
}

func validSpecAuditVerdict(verdict string) bool {
	switch verdict {
	case "EXCEEDS", "MATCH", "PARTIAL", "MISSING", "WRONG_SHAPE", "PARALLEL_SYSTEM", "WEAK_TESTS", "UNVERIFIED", "NO_CODE_SURFACE":
		return true
	default:
		return false
	}
}

func passingSpecAuditVerdict(verdict string) bool {
	return verdict == "EXCEEDS" || verdict == "MATCH" || verdict == "NO_CODE_SURFACE"
}

func validCarryDecision(decision string) bool {
	switch decision {
	case "CARRY", "IMPROVE", "PROVEN_IRRELEVANT", "GAP":
		return true
	default:
		return false
	}
}

func validClaimStatus(status string) bool {
	switch status {
	case "ACTIVE", "COMPLETED", "PARTIAL", "BLOCKED", "STALE":
		return true
	default:
		return false
	}
}

func validClaimPhase(phase string) bool {
	switch phase {
	case "CLAIMED", "ATOMIZING", "RESEARCH", "OWNER_DISCOVERY", "CODE_MAPPING", "GAP_WRITING", "RANGE_REALITY_CHECK", "COMPLETED":
		return true
	default:
		return false
	}
}

func emptyAuditEvidence(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == "" || trimmed == "-" || strings.EqualFold(trimmed, "pending") || strings.EqualFold(trimmed, "NONE")
}

func atomsForRange(auditRange specAuditRange, atoms map[string]specAuditAtom) []specAuditAtom {
	var inRange []specAuditAtom
	for _, atom := range atoms {
		for _, atomRange := range atom.SpecLines {
			if rangesOverlap(auditRange, atomRange) {
				inRange = append(inRange, atom)
				break
			}
		}
	}
	return inRange
}

func lineCoveredByAtoms(line int, atoms []specAuditAtom) bool {
	for _, atom := range atoms {
		for _, atomRange := range atom.SpecLines {
			if line >= atomRange.Start && line <= atomRange.End {
				return true
			}
		}
	}
	return false
}

func researchRefCoveredByFloor(ref string, atomID string, floors map[string]specAuditResearchFloor) bool {
	for _, floor := range floors {
		if !strings.HasPrefix(floor.SourceRef, ref) {
			continue
		}
		for _, linked := range floor.LinkedAtomIDs {
			if linked == atomID {
				return true
			}
		}
	}
	return false
}

func rangeOverlapsAny(auditRange specAuditRange, ranges []specAuditRange) bool {
	for _, existing := range ranges {
		if rangesOverlap(auditRange, existing) {
			return true
		}
	}
	return false
}

func rangesOverlap(left specAuditRange, right specAuditRange) bool {
	return left.Start <= right.End && right.Start <= left.End
}

func specAuditRangeArtifactName(auditRange specAuditRange) string {
	return fmt.Sprintf("L%04d-L%04d.md", auditRange.Start, auditRange.End)
}

func formatSpecAuditRanges(ranges []specAuditRange) string {
	parts := make([]string, 0, len(ranges))
	for _, auditRange := range ranges {
		parts = append(parts, auditRange.Text)
	}
	return strings.Join(parts, ", ")
}

func contiguousCompletedSpecLine(completedRanges []specAuditRange, atoms map[string]specAuditAtom) int {
	line := 1
	for {
		advanced := false
		for _, auditRange := range completedRanges {
			if auditRange.Start == line {
				atomsInRange := atomsForRange(auditRange, atoms)
				complete := true
				for current := auditRange.Start; current <= auditRange.End; current++ {
					if !lineCoveredByAtoms(current, atomsInRange) {
						complete = false
						break
					}
				}
				if complete {
					line = auditRange.End + 1
					advanced = true
					break
				}
			}
		}
		if !advanced {
			return line - 1
		}
	}
}

func parseSpecAuditLineNumber(value string) int {
	value = strings.TrimSpace(value)
	if value == "" || value == "none" || value == "-" {
		return 0
	}
	re := regexp.MustCompile(`L?([0-9]+)$`)
	match := re.FindStringSubmatch(value)
	if match == nil {
		return 0
	}
	n, _ := strconv.Atoi(match[1])
	return n
}

type specAuditFileLineRef struct {
	Path string
	Line int
}

func parseSpecAuditFileLineRefs(value string) []specAuditFileLineRef {
	re := regexp.MustCompile(`([A-Za-z0-9_./-]+):L([0-9]+)(?:-L?[0-9]+)?`)
	matches := re.FindAllStringSubmatch(value, -1)
	refs := make([]specAuditFileLineRef, 0, len(matches))
	for _, match := range matches {
		line, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		refs = append(refs, specAuditFileLineRef{Path: match[1], Line: line})
	}
	return refs
}

func auditRepoFileLineRef(root string, ref specAuditFileLineRef, context string) string {
	clean := filepath.Clean(filepath.FromSlash(ref.Path))
	if ref.Path == "" || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return fmt.Sprintf("docs/spec-audit: %s uses invalid repo-relative evidence path %q", context, ref.Path)
	}
	path := filepath.Join(root, clean)
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf("docs/spec-audit: %s points to missing evidence path %s", context, ref.Path)
	}
	if info.IsDir() {
		return ""
	}
	if ref.Line <= 0 {
		return fmt.Sprintf("docs/spec-audit: %s uses invalid line in evidence path %s:L%d", context, ref.Path, ref.Line)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("docs/spec-audit: %s cannot read evidence path %s: %v", context, ref.Path, err)
	}
	if ref.Line > lineCount(string(content)) {
		return fmt.Sprintf("docs/spec-audit: %s points outside %s line count at L%d", context, ref.Path, ref.Line)
	}
	return ""
}
