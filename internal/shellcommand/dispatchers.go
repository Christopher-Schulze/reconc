package shellcommand

import "strings"

// skipStaticOperands consumes count following words only when each is a
// static option operand. Missing or dynamic operands are incomplete so an
// unknown value cannot be mistaken for the dispatched command.
func skipStaticOperands(words []commandWord, index, count int) (int, bool) {
	if count < 0 || index+count > len(words) {
		return index, false
	}
	for offset := 0; offset < count; offset++ {
		if words[index+offset].dynamic {
			return index, false
		}
	}
	return index + count, true
}

// classifyInlineOption maps `--flag=value` onto classify(flag). Empty values
// and short `-f=value` forms are incomplete. A recognized flag may carry an
// optional attached value without consuming the following word.
func classifyInlineOption(word string, classify func(string) (int, bool)) (int, bool) {
	name, value, ok := strings.Cut(word, "=")
	if !ok {
		return classify(word)
	}
	if !strings.HasPrefix(name, "--") {
		return 0, false
	}
	operands, recognized := classify(name)
	if !recognized || value == "" {
		return 0, false
	}
	if operands < 1 {
		// Optional attached value on a flag, such as unshare --mount=/ns.
		return 0, true
	}
	return operands - 1, true
}

// skipDispatcherOptions walks known dispatcher flags. classify reports how
// many following operands a canonical option consumes. Unknown options,
// dynamic words, and missing operands are incomplete. `--` ends option
// parsing. The first non-option word is left for the caller as the command.
func skipDispatcherOptions(words []commandWord, index int, classify func(string) (int, bool)) (int, bool) {
	for index < len(words) {
		if words[index].dynamic {
			return index, false
		}
		word := words[index].value
		if word == "--" {
			return index + 1, true
		}
		if !strings.HasPrefix(word, "-") || word == "-" {
			return index, true
		}
		operands, recognized := classifyInlineOption(word, classify)
		if !recognized {
			return index, false
		}
		next, ok := skipStaticOperands(words, index+1, operands)
		if !ok {
			return index, false
		}
		index = next
	}
	return index, true
}

func skipBusyboxApplet(words []commandWord, index int) (int, bool) {
	if index >= len(words) || words[index].dynamic {
		return index, false
	}
	if words[index].value == "--" {
		index++
		if index >= len(words) || words[index].dynamic {
			return index, false
		}
	}
	if strings.HasPrefix(words[index].value, "-") {
		return index, false
	}
	return index, true
}

func skipTasksetOptions(words []commandWord, index int) (int, bool) {
	cpuList := false
	for index < len(words) {
		if words[index].dynamic {
			return index, false
		}
		word := words[index].value
		if word == "--" {
			index++
			break
		}
		if word == "-p" || word == "--pid" {
			return index, false
		}
		if !strings.HasPrefix(word, "-") || word == "-" {
			break
		}
		operands, recognized := classifyInlineOption(word, classifyTasksetOption)
		if !recognized {
			return index, false
		}
		if word == "-c" || word == "--cpu-list" || strings.HasPrefix(word, "--cpu-list=") {
			cpuList = true
		}
		next, ok := skipStaticOperands(words, index+1, operands)
		if !ok {
			return index, false
		}
		index = next
	}
	if index >= len(words) || words[index].dynamic {
		return index, false
	}
	if !cpuList {
		index++
		if index >= len(words) || words[index].dynamic {
			return index, false
		}
	}
	return index, true
}

func classifyTasksetOption(word string) (int, bool) {
	switch word {
	case "-a", "--all-tasks":
		return 0, true
	case "-c", "--cpu-list":
		return 1, true
	default:
		return 0, false
	}
}

func skipBwrapOptions(words []commandWord, index int) (int, bool) {
	return skipDispatcherOptions(words, index, classifyBwrapOption)
}

func classifyBwrapOption(word string) (int, bool) {
	switch word {
	case "--bind", "--bind-try", "--dev-bind", "--dev-bind-try", "--ro-bind", "--ro-bind-try", "--symlink", "--setenv", "--file":
		return 2, true
	case "--args", "--chdir", "--dev", "--dir", "--exec-label", "--file-label", "--gid", "--hostname", "--info-fd", "--json-status-fd", "--lock-file", "--perms", "--seccomp", "--tmpfs", "--uid", "--unsetenv", "--size", "--proc", "--remount-ro", "--userns", "--userns2", "--pidns", "--cap-add", "--cap-drop", "--sync-fd", "--block-fd", "--userns-block-fd":
		return 1, true
	case "--help", "--new-session", "--die-with-parent", "--as-pid-1", "--clearenv", "--level-prefix", "--share-net", "--unshare-all", "--unshare-user", "--unshare-user-try", "--unshare-ipc", "--unshare-pid", "--unshare-net", "--unshare-uts", "--unshare-cgroup", "--unshare-cgroup-try", "--disable-userns", "--assert-userns-disabled":
		return 0, true
	default:
		return 0, false
	}
}

func skipUnshareOptions(words []commandWord, index int) (int, bool) {
	return skipDispatcherOptions(words, index, classifyUnshareOption)
}

func classifyUnshareOption(word string) (int, bool) {
	switch word {
	case "--map-user", "--map-group", "--map-users", "--map-groups", "--propagation", "--setgroups", "-R", "--root", "-w", "--wd", "-S", "--setuid", "-G", "--setgid", "--monotonic", "--boottime":
		return 1, true
	case "-m", "--mount", "-u", "--uts", "-i", "--ipc", "-n", "--net", "-p", "--pid", "-U", "--user", "-C", "--cgroup", "-T", "--time", "-f", "--fork", "--kill-child", "--mount-proc", "-r", "--map-root-user", "--map-current-user", "--map-auto", "--keep-caps":
		return 0, true
	default:
		return 0, false
	}
}

func skipNsenterOptions(words []commandWord, index int) (int, bool) {
	return skipDispatcherOptions(words, index, classifyNsenterOption)
}

func classifyNsenterOption(word string) (int, bool) {
	switch word {
	case "-t", "--target", "-S", "--setuid", "-G", "--setgid", "--root", "--wd":
		return 1, true
	case "-m", "--mount", "-u", "--uts", "-i", "--ipc", "-n", "--net", "-p", "--pid", "-U", "--user", "-C", "--cgroup", "-T", "--time", "-r", "-w", "-F", "--no-fork", "-Z", "--follow-context", "--preserve-credentials":
		return 0, true
	default:
		return 0, false
	}
}

func skipPkexecOptions(words []commandWord, index int) (int, bool) {
	return skipDispatcherOptions(words, index, classifyPkexecOption)
}

func classifyPkexecOption(word string) (int, bool) {
	switch word {
	case "--user":
		return 1, true
	case "--disable-internal-agent":
		return 0, true
	default:
		return 0, false
	}
}

func skipSystemdRunOptions(words []commandWord, index int) (int, bool) {
	return skipDispatcherOptions(words, index, classifySystemdRunOption)
}

func classifySystemdRunOption(word string) (int, bool) {
	switch word {
	case "-S", "--shell":
		return 0, false
	case "-u", "--unit", "-p", "--property", "--description", "--slice", "--service-type", "--uid", "--gid", "--nice", "--working-directory", "--root-directory", "-E", "--setenv", "--output", "--on-active", "--on-boot", "--on-startup", "--on-unit-active", "--on-unit-inactive", "--on-calendar", "--path-property", "--socket-property", "--timer-property", "--job-mode", "--background", "-H", "--host", "-M", "--machine", "-C", "--capsule", "--json", "--expand-environment":
		return 1, true
	case "--scope", "--slice-inherit", "-r", "--remain-after-exit", "--send-sighup", "-d", "--same-dir", "-R", "--same-root-dir", "-t", "--pty", "-T", "--pty-late", "-P", "--pipe", "-q", "--quiet", "-v", "--verbose", "--on-clock-change", "--on-timezone-change", "--no-block", "--wait", "-G", "--collect", "--ignore-failure", "--user", "--system", "--no-ask-password", "-h", "--help", "--version", "--no-pager":
		return 0, true
	default:
		return 0, false
	}
}

func parallelCommand(words []commandWord) ([]commandWord, bool) {
	quote := false
	for index := 0; index < len(words); index++ {
		if words[index].dynamic || strings.ContainsAny(words[index].value, "{}") {
			return nil, false
		}
		word := words[index].value
		if word == "-q" || word == "--quote" {
			quote = true
			continue
		}
		if parallelArgSeparator(word) {
			return nil, false
		}
		if word == "--" {
			return commandUntilParallelArgs(words[index+1:], quote)
		}
		if parallelIncompleteMode(word) {
			return nil, false
		}
		if strings.HasPrefix(word, "-") && word != "-" {
			operands, recognized := classifyInlineOption(word, classifyParallelOption)
			if !recognized {
				return nil, false
			}
			next, ok := skipStaticOperands(words, index+1, operands)
			if !ok {
				return nil, false
			}
			// Parallel evaluates replacement expressions in option values too,
			// including tag strings and output paths, outside the inner command.
			for _, operand := range words[index+1 : next] {
				if strings.ContainsAny(operand.value, "{}") {
					return nil, false
				}
			}
			index = next - 1
			continue
		}
		return commandUntilParallelArgs(words[index:], quote)
	}
	return nil, false
}

func commandUntilParallelArgs(words []commandWord, quote bool) ([]commandWord, bool) {
	end := 0
	var body strings.Builder
	for end < len(words) {
		if words[end].dynamic {
			return nil, false
		}
		if parallelArgSeparator(words[end].value) {
			break
		}
		// Replacement syntax can change executable positions or shell structure.
		// Do not claim a complete command without evaluating that language.
		if strings.ContainsAny(words[end].value, "{}") {
			return nil, false
		}
		if end > 0 {
			body.WriteByte(' ')
		}
		if quote {
			body.WriteString("'" + strings.ReplaceAll(words[end].value, "'", "'\"'\"'") + "'")
		} else {
			body.WriteString(words[end].value)
		}
		end++
	}
	if end == 0 {
		return nil, false
	}
	// Parallel reparses the joined command in a shell, including nested
	// launchers. Model appended input as unknown positional arguments so exact
	// matching and shell/eval consumers cannot mistake it for absent input.
	body.WriteString(` "$@"`)
	return []commandWord{{value: "sh"}, {value: "-c"}, {value: body.String()}}, true
}

func parallelArgSeparator(word string) bool {
	return word == ":::" || word == "::::" || word == ":::+" || word == "::::+"
}

func parallelIncompleteMode(word string) bool {
	name, _, _ := strings.Cut(word, "=")
	switch name {
	case "--pipe", "--pipepart", "--pipe-part", "--fifo", "--cat", "--tee", "-I", "--replace", "--plus", "-m", "-X":
		return true
	default:
		return false
	}
}

func classifyParallelOption(word string) (int, bool) {
	if joinedParallelJobs(word) {
		return 0, true
	}
	switch word {
	case "-j", "--jobs", "--max-procs", "-n", "--max-args", "-N", "-I", "--replace", "-L", "--max-lines", "-a", "--arg-file", "--colsep", "--col-sep", "-C", "--header", "--timeout", "--retries", "--joblog", "--job-log", "--results", "--workdir", "--wd", "--delay", "--memfree", "--load", "--halt", "--tagstring", "--tag-string", "--env", "--trim", "--block", "--recstart", "--recend", "-S", "--sshlogin":
		return 1, true
	case "-0", "--null", "-k", "--keep-order", "--keeporder", "-q", "--quote", "-v", "--verbose", "-p", "--progress", "--eta", "--bar", "--latest-line", "--dry-run", "--dryrun", "--tag", "--plus", "-u", "--ungroup", "-g", "--group", "--line-buffer", "--linebuffer", "--lb", "--files", "--files0", "--compress", "--tty", "--record-env", "--session", "-m", "-X", "-x", "--no-notice", "--quiet", "-Q", "--willcite", "--citation":
		return 0, true
	default:
		return 0, false
	}
}

func joinedParallelJobs(word string) bool {
	if !strings.HasPrefix(word, "-j") || len(word) < 3 {
		return false
	}
	for _, character := range word[2:] {
		if (character >= '0' && character <= '9') || character == '+' || character == '%' {
			continue
		}
		return false
	}
	return true
}
