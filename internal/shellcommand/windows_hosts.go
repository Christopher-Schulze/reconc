package shellcommand

import "strings"

// powerShellHostArgument returns the PowerShell text that `powershell` or
// `pwsh` runs, translated for analysis. An encoded command, a command read
// from standard input, a dynamic word, or an unknown parameter is not
// enumerable. `-File` runs a script, which, like `bash script.sh`, is the
// invocation itself.
func powerShellHostArgument(words []commandWord) (string, bool) {
	for index := 0; index < len(words); index++ {
		word := words[index]
		if word.dynamic {
			return "", false
		}
		if !strings.HasPrefix(word.value, "-") && !strings.HasPrefix(word.value, "/") {
			// A positional argument is the command for Windows PowerShell and
			// the file for pwsh; analyzing it as a command is the stricter.
			return powerShellCommandText(words[index:])
		}
		name := strings.ToLower(strings.TrimLeft(word.value, "-/"))
		name, _, _ = strings.Cut(name, ":")
		parameter, ok := resolvePowerShellHostParameter(name)
		if !ok {
			return "", false
		}
		switch parameter.name {
		case "command":
			if index+1 < len(words) && words[index+1].value == "-" && !words[index+1].dynamic {
				return "", false
			}
			return powerShellCommandText(words[index+1:])
		case "encodedcommand", "encodedarguments":
			return "", false
		case "file":
			return "", true
		}
		if parameter.takesValue {
			index++
		}
	}
	return "", true
}

func powerShellCommandText(words []commandWord) (string, bool) {
	if len(words) == 0 {
		return "", true
	}
	values := make([]string, len(words))
	for index, word := range words {
		if word.dynamic {
			return "", false
		}
		values[index] = word.value
	}
	return PowerShellAnalysisCommand(strings.Join(values, " ")), true
}

var powerShellHostParameters = []powerShellParameter{
	{name: "command", aliases: []string{"c"}},
	{name: "configurationfile", takesValue: true},
	{name: "configurationname", aliases: []string{"config"}, takesValue: true},
	{name: "custompipename", takesValue: true},
	{name: "encodedarguments", aliases: []string{"ea", "encodeda"}},
	{name: "encodedcommand", aliases: []string{"e", "ec", "en", "enc"}},
	{name: "executionpolicy", aliases: []string{"ep", "ex"}, takesValue: true},
	{name: "file", aliases: []string{"f"}},
	{name: "help", aliases: []string{"?", "h"}},
	{name: "inputformat", aliases: []string{"if", "inp"}, takesValue: true},
	{name: "interactive", aliases: []string{"i"}},
	{name: "login", aliases: []string{"l"}},
	{name: "mta"},
	{name: "noexit", aliases: []string{"noe"}},
	{name: "nologo", aliases: []string{"nol"}},
	{name: "noninteractive", aliases: []string{"noni"}},
	{name: "noprofile", aliases: []string{"nop"}},
	{name: "noprofileloadtime"},
	{name: "outputformat", aliases: []string{"o", "of"}, takesValue: true},
	{name: "psconsolefile", takesValue: true},
	{name: "settingsfile", aliases: []string{"settings"}, takesValue: true},
	{name: "sshservermode", aliases: []string{"sshs"}},
	{name: "sta"},
	{name: "version", aliases: []string{"v"}, takesValue: true},
	{name: "windowstyle", aliases: []string{"w"}, takesValue: true},
	{name: "workingdirectory", aliases: []string{"wd"}, takesValue: true},
}

func resolvePowerShellHostParameter(name string) (powerShellParameter, bool) {
	return resolvePowerShellParameter(powerShellHostParameters, name)
}

// cmdCommandArgument returns the command `cmd /c` or `cmd /k` runs. cmd.exe
// parses that text again with its own operators, quoting and %VARIABLE%
// expansion, so only plain words are enumerable.
func cmdCommandArgument(words []commandWord) (string, bool) {
	for index, word := range words {
		if word.dynamic {
			return "", false
		}
		switch strings.ToLower(word.value) {
		case "/c", "/k", "/r":
			values := make([]string, 0, len(words)-index-1)
			for _, rest := range words[index+1:] {
				if rest.dynamic {
					return "", false
				}
				values = append(values, rest.value)
			}
			text := strings.Join(values, " ")
			if strings.ContainsAny(text, "&|<>^%\"()") {
				return "", false
			}
			fields := strings.Fields(text)
			for field := range fields {
				fields[field] = shellQuote(fields[field])
			}
			return strings.Join(fields, " "), true
		}
		if !strings.HasPrefix(word.value, "/") {
			return "", true
		}
	}
	return "", true
}

func isPowerShellHost(word string) bool {
	switch windowsCommandBase(word) {
	case "powershell", "pwsh":
		return true
	default:
		return false
	}
}

func isCmdHost(word string) bool {
	return windowsCommandBase(word) == "cmd"
}

// windowsCommandBase is the lower-case command name of a path written with
// either separator, without its Windows executable suffix.
func windowsCommandBase(word string) string {
	word = strings.ReplaceAll(strings.TrimSpace(word), `\`, "/")
	return strings.ToLower(baseName(normalizedPowerShellExecutable(word)))
}
