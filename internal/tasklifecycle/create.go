package tasklifecycle

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxTaskTitleRunes = 200

// Create adds one queued TASK row and one grammar-correct detail file through
// the same recoverable transaction used by every other lifecycle mutation.
func Create(repoRoot, title, requestedID string) (MutationResult, error) {
	title, err := normalizeTaskTitle(title)
	if err != nil {
		return MutationResult{}, err
	}
	root, err := canonicalRepoRoot(repoRoot)
	if err != nil {
		return MutationResult{}, err
	}
	var created MutationResult
	err = withMutationLock(root, func() error {
		board, err := Load(root)
		if err != nil {
			return err
		}
		id, err := selectCreateID(board, requestedID)
		if err != nil {
			return err
		}
		slug := taskSlug(title)
		name := id
		filename := id + "-" + slug + ".md"
		if board.Profile == ProfileLogbook {
			name = "TASK-" + id + "-" + slug
			filename = name + ".md"
		}
		detailRel := filepath.ToSlash(filepath.Join(board.Config.DetailDir, filename))
		detailAbs := filepath.Join(root, filepath.FromSlash(detailRel))
		if _, err := os.Lstat(detailAbs); err == nil {
			return fmt.Errorf("TASK detail already exists: %s", detailRel)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect TASK detail %s: %w", detailRel, err)
		}
		overviewDir := filepath.Dir(filepath.Join(root, filepath.FromSlash(board.Config.OverviewPath)))
		target, err := filepath.Rel(overviewDir, detailAbs)
		if err != nil {
			return fmt.Errorf("resolve TASK detail target: %w", err)
		}
		target = filepath.ToSlash(target)
		row := fmt.Sprintf("- [ ] %s %s -> %s", id, title, target)
		if board.Profile == ProfileLogbook {
			row = fmt.Sprintf("- [ ] %s - %s -> %s", name, title, target)
		}
		overview, err := insertQueuedTaskRow(board, row)
		if err != nil {
			return err
		}
		detail := renderQueuedTaskDetail(board, id, name, title)
		files := []fileMutation{
			{Path: board.Config.OverviewPath, After: overview},
			{Path: detailRel, After: detail, Create: true},
		}
		if err := applyTransaction(root, "new", files, nil); err != nil {
			return err
		}
		verified, err := Load(root)
		if err != nil {
			return fmt.Errorf("verify created TASK: %w", err)
		}
		task, ok := verified.findTask(id)
		if !ok || task == nil || task.State != StateQueued || task.Path != target {
			return fmt.Errorf("verify created TASK %s: queued row or detail is missing", id)
		}
		created = MutationResult{Action: "new", TaskID: id, TaskPath: detailRel, State: StateQueued}
		return nil
	})
	return created, err
}

func normalizeTaskTitle(title string) (string, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return "", fmt.Errorf("TASK title is required")
	}
	if strings.ContainsAny(title, "\r\n") {
		return "", fmt.Errorf("TASK title must be one line")
	}
	if utf8.RuneCountInString(title) > maxTaskTitleRunes {
		return "", fmt.Errorf("TASK title exceeds %d characters", maxTaskTitleRunes)
	}
	return title, nil
}

func selectCreateID(board *Board, requested string) (string, error) {
	width := 3
	limit := 999
	if board.Profile == ProfileLogbook {
		width = 4
		limit = 9999
	}
	requested = strings.TrimSpace(requested)
	if requested != "" {
		if len(requested) != width {
			return "", fmt.Errorf("TASK ID must be exactly %d digits for %s", width, board.Profile)
		}
		value, err := strconv.Atoi(requested)
		if err != nil || value < 1 || value > limit {
			return "", fmt.Errorf("TASK ID must be a non-zero %d-digit number", width)
		}
		reserved, err := taskIDReserved(board, requested)
		if err != nil {
			return "", err
		}
		if reserved {
			return "", fmt.Errorf("TASK ID %s already exists", requested)
		}
		return requested, nil
	}
	maxID := 0
	for _, task := range board.allTasks() {
		if value := leadingTaskNumber(task.ID); value > maxID {
			maxID = value
		}
	}
	for _, dir := range []string{board.Config.DetailDir, board.Config.DoneDir} {
		entries, err := os.ReadDir(filepath.Join(board.RepoRoot, filepath.FromSlash(dir)))
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("scan TASK IDs in %s: %w", dir, err)
		}
		for _, entry := range entries {
			if value := taskNumberFromFilename(entry.Name(), board.Profile); value > maxID {
				maxID = value
			}
		}
	}
	if maxID >= limit {
		return "", fmt.Errorf("TASK ID space for %s is exhausted", board.Profile)
	}
	return fmt.Sprintf("%0*d", width, maxID+1), nil
}

func taskIDReserved(board *Board, id string) (bool, error) {
	if _, exists := board.tasksByID[id]; exists {
		return true, nil
	}
	for _, dir := range []string{board.Config.DetailDir, board.Config.DoneDir} {
		entries, err := os.ReadDir(filepath.Join(board.RepoRoot, filepath.FromSlash(dir)))
		if err != nil && !os.IsNotExist(err) {
			return false, fmt.Errorf("scan TASK IDs in %s: %w", dir, err)
		}
		for _, entry := range entries {
			if board.Profile == ProfileSections && strings.HasPrefix(entry.Name(), id+"-") {
				return true, nil
			}
			if board.Profile == ProfileLogbook && strings.HasPrefix(entry.Name(), "TASK-"+id+"-") {
				return true, nil
			}
		}
	}
	return false, nil
}

func leadingTaskNumber(id string) int {
	digits := id
	if dash := strings.IndexByte(digits, '-'); dash >= 0 {
		digits = digits[:dash]
	}
	value, _ := strconv.Atoi(digits)
	return value
}

func taskNumberFromFilename(name string, profile Profile) int {
	if profile == ProfileLogbook {
		name = strings.TrimPrefix(name, "TASK-")
	}
	dash := strings.IndexByte(name, '-')
	if dash < 0 {
		return 0
	}
	return leadingTaskNumber(name[:dash])
}

func taskSlug(title string) string {
	var out strings.Builder
	separator := false
	for _, value := range strings.ToLower(title) {
		value = asciiTaskRune(value)
		switch {
		case value >= 'a' && value <= 'z', value >= '0' && value <= '9':
			if separator && out.Len() > 0 {
				out.WriteByte('-')
			}
			out.WriteRune(value)
			separator = false
		case unicode.IsSpace(value) || unicode.IsPunct(value) || unicode.IsSymbol(value):
			separator = true
		}
	}
	if out.Len() == 0 {
		return "task"
	}
	return out.String()
}

func asciiTaskRune(value rune) rune {
	switch value {
	case 'ä', 'á', 'à', 'â', 'ã', 'å':
		return 'a'
	case 'ö', 'ó', 'ò', 'ô', 'õ':
		return 'o'
	case 'ü', 'ú', 'ù', 'û':
		return 'u'
	case 'é', 'è', 'ê', 'ë':
		return 'e'
	case 'í', 'ì', 'î', 'ï':
		return 'i'
	case 'ß':
		return 's'
	default:
		return value
	}
}

func insertQueuedTaskRow(board *Board, row string) ([]byte, error) {
	lines := append([]string(nil), board.overviewLines...)
	insertAt := -1
	if board.Profile == ProfileSections {
		heading, ok := board.sectionLines[StateQueued]
		if !ok {
			return nil, fmt.Errorf("TASK overview has no Queue section")
		}
		insertAt = heading + 1
		for insertAt < len(lines) && strings.TrimSpace(lines[insertAt]) == "" {
			insertAt++
		}
		for index := insertAt; index < len(lines) && !strings.HasPrefix(lines[index], "## "); index++ {
			if strings.HasPrefix(lines[index], "- [") {
				insertAt = index + 1
			}
		}
	} else {
		lastTask := -1
		currentLine := -1
		for index, line := range lines {
			switch {
			case strings.HasPrefix(line, "Current:"):
				currentLine = index
			case strings.HasPrefix(line, "- [ ] TASK-"), strings.HasPrefix(line, "- [x] TASK-"):
				lastTask = index
			}
		}
		switch {
		case lastTask >= 0:
			insertAt = lastTask + 1
		case currentLine >= 0:
			insertAt = currentLine + 1
			for insertAt < len(lines) && strings.TrimSpace(lines[insertAt]) == "" {
				insertAt++
			}
		}
	}
	if insertAt < 0 || insertAt > len(lines) {
		return nil, fmt.Errorf("TASK overview has no safe queued-row insertion point")
	}
	needsTrailingBlank := board.Profile == ProfileSections && insertAt < len(lines) && strings.HasPrefix(lines[insertAt], "## ")
	lines = append(lines, "")
	copy(lines[insertAt+1:], lines[insertAt:])
	lines[insertAt] = row
	if needsTrailingBlank {
		lines = append(lines, "")
		copy(lines[insertAt+2:], lines[insertAt+1:])
		lines[insertAt+1] = ""
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func renderQueuedTaskDetail(board *Board, id, name, title string) []byte {
	var out strings.Builder
	if board.Profile == ProfileSections {
		fmt.Fprintf(&out, "# TASK %s: %s\n", id, title)
	} else {
		fmt.Fprintf(&out, "# %s\n", name)
	}
	writeTaskSection(&out, "Why", "Deliver "+title+".")
	if board.Profile == ProfileLogbook {
		writeTaskSection(&out, "Status", "State: Queued")
		writeTaskSection(&out, "Scheduling", "- Depends On: none")
		writeTaskSection(&out, "Technical Plan", "Implement and verify "+title+".")
	}
	writeTaskSection(&out, "Acceptance", "- "+title+" is implemented and verified.")
	base := make(map[string]bool, len(sectionsForProfile[board.Profile]))
	for _, section := range sectionsForProfile[board.Profile] {
		base[section] = true
	}
	for _, section := range board.Config.Completion.RequiredSections {
		if !base[section] && section != "Evidence" {
			writeTaskSection(&out, section, "None.")
			base[section] = true
		}
	}
	if len(board.Config.Completion.RequiredEvidenceFields) > 0 || containsString(board.Config.Completion.RequiredSections, "Evidence") {
		var evidence strings.Builder
		for _, field := range board.Config.Completion.RequiredEvidenceFields {
			fmt.Fprintf(&evidence, "- %s:\n", field)
		}
		writeTaskSection(&out, "Evidence", strings.TrimSuffix(evidence.String(), "\n"))
	}
	writeTaskSection(&out, "Sub-Tasks", "- [ ] Implement and verify "+title+".")
	writeTaskSection(&out, "Notes", "None.")
	writeTaskSection(&out, "Deviations", "None.")
	return []byte(out.String())
}

func writeTaskSection(out *strings.Builder, name, body string) {
	fmt.Fprintf(out, "\n## %s\n\n%s\n", name, body)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
