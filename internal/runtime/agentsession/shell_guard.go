package agentsession

import "strings"

func forbiddenShellCommandReason(command string) string {
	tokens := shellTokens(command)
	for i, token := range tokens {
		if !isGitToken(token) {
			continue
		}
		subcommandIndex := gitSubcommandIndex(tokens, i+1)
		if subcommandIndex < 0 {
			continue
		}
		subcommand := tokens[subcommandIndex]
		args := commandArgs(tokens[subcommandIndex+1:])
		switch subcommand {
		case "clean":
			if !hasGitCleanDryRun(args) {
				return "reconc blocked destructive git clean before execution. Use a dry-run (`git clean -nd`) and get explicit user approval before deleting untracked files."
			}
		case "reset":
			if hasArg(args, "--hard") {
				return "reconc blocked destructive git reset --hard before execution. Preserve user work and ask for explicit approval before hard-resetting tracked changes."
			}
		}
	}
	for _, nested := range nestedShellCommandStrings(tokens) {
		if reason := forbiddenShellCommandReason(nested); reason != "" {
			return reason
		}
	}
	return ""
}

func shellTokens(command string) []string {
	var tokens []string
	var b strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if b.Len() == 0 {
			return
		}
		tokens = append(tokens, b.String())
		b.Reset()
	}
	for _, r := range command {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"':
			quote = r
		case ' ', '\t', '\n', '\r':
			flush()
		case ';', '|', '&':
			flush()
			tokens = append(tokens, string(r))
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return tokens
}

func isGitToken(token string) bool {
	token = strings.TrimSpace(token)
	return token == "git" || strings.HasSuffix(token, "/git")
}

func nestedShellCommandStrings(tokens []string) []string {
	var nested []string
	for i := 0; i < len(tokens); i++ {
		token := strings.TrimSpace(tokens[i])
		if token == "eval" {
			if i+1 < len(tokens) {
				nested = append(nested, tokens[i+1])
				i++
			}
			continue
		}
		if !isShellToken(token) {
			continue
		}
		for j := i + 1; j < len(tokens); j++ {
			arg := tokens[j]
			if isShellOperator(arg) {
				break
			}
			if isShellCommandFlag(arg) {
				if j+1 < len(tokens) {
					nested = append(nested, tokens[j+1])
				}
				break
			}
		}
	}
	return nested
}

func isShellToken(token string) bool {
	token = strings.TrimSpace(token)
	switch token {
	case "sh", "bash", "zsh", "dash", "ksh", "fish":
		return true
	}
	for _, suffix := range []string{"/sh", "/bash", "/zsh", "/dash", "/ksh", "/fish"} {
		if strings.HasSuffix(token, suffix) {
			return true
		}
	}
	return false
}

func isShellCommandFlag(arg string) bool {
	if arg == "-c" || arg == "-lc" {
		return true
	}
	if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "c") {
		return true
	}
	return false
}

func gitSubcommandIndex(tokens []string, start int) int {
	for i := start; i < len(tokens); i++ {
		token := tokens[i]
		if isShellOperator(token) {
			return -1
		}
		if token == "-C" || token == "-c" || token == "--git-dir" || token == "--work-tree" || token == "--namespace" {
			i++
			continue
		}
		if strings.HasPrefix(token, "-C") && token != "-C" {
			continue
		}
		if strings.HasPrefix(token, "--git-dir=") || strings.HasPrefix(token, "--work-tree=") || strings.HasPrefix(token, "--namespace=") {
			continue
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		return i
	}
	return -1
}

func commandArgs(tokens []string) []string {
	var args []string
	for _, token := range tokens {
		if isShellOperator(token) {
			break
		}
		args = append(args, token)
	}
	return args
}

func isShellOperator(token string) bool {
	switch token {
	case ";", "|", "&":
		return true
	default:
		return false
	}
}

func hasGitCleanDryRun(args []string) bool {
	for _, arg := range args {
		if arg == "--dry-run" {
			return true
		}
		if strings.HasPrefix(arg, "-") && !strings.HasPrefix(arg, "--") && strings.Contains(arg, "n") {
			return true
		}
	}
	return false
}

func hasArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
