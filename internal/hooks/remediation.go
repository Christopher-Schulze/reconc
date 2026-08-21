package hooks

import (
	"runtime"
	"strings"
	"unicode"
)

type remediationDisposition string

const (
	remediationNone           remediationDisposition = "none"
	remediationInstall        remediationDisposition = "install"
	remediationForceRepair    remediationDisposition = "force-repair"
	remediationManualConflict remediationDisposition = "manual-conflict"
	remediationHostAction     remediationDisposition = "host-action"
)

type remediationCommand struct {
	Program string
	Args    []string
}

type remediationPlan struct {
	Disposition remediationDisposition
	Command     remediationCommand
	Message     string
}

func hookInstallRemediation(kind, root string, force bool) remediationPlan {
	disposition := remediationInstall
	args := []string{"hook", "install", kind}
	if kind != KindKimiCode {
		args = append(args, root)
	}
	if force {
		disposition = remediationForceRepair
		args = append(args, "--force")
	}
	return remediationPlan{
		Disposition: disposition,
		Command:     remediationCommand{Program: "reconc", Args: args},
	}
}

func manualRemediation(message string) remediationPlan {
	return remediationPlan{Disposition: remediationManualConflict, Message: message}
}

func hostRemediation(message string, command remediationCommand) remediationPlan {
	return remediationPlan{Disposition: remediationHostAction, Message: message, Command: command}
}

func noRemediation() remediationPlan {
	return remediationPlan{Disposition: remediationNone}
}

func applyRemediation(report *PlatformStatus, plan remediationPlan) {
	report.remediation = plan
	report.Remediation = renderCurrentRemediation(plan)
}

func renderCurrentRemediation(plan remediationPlan) string {
	return renderRemediation(plan, runtime.GOOS)
}

func renderRemediation(plan remediationPlan, operatingSystem string) string {
	if plan.Disposition == remediationNone {
		return ""
	}
	if plan.Command.Program == "" {
		return plan.Message
	}
	command, language := renderRemediationCommand(plan.Command, operatingSystem)
	fenced := fencedCommand(command, language)
	if plan.Message == "" {
		return "Run:\n\n" + fenced
	}
	return plan.Message + "\n\n" + fenced
}

func renderRemediationCommand(command remediationCommand, operatingSystem string) (string, string) {
	if operatingSystem == "windows" {
		return renderPowerShellProcess(command), "powershell"
	}
	values := append([]string{command.Program}, command.Args...)
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = quotePOSIXArgument(value)
	}
	return strings.Join(quoted, " "), "sh"
}

func quotePOSIXArgument(value string) string {
	if shellSafeBareArgument(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func quotePowerShellArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func renderPowerShellProcess(command remediationCommand) string {
	arguments := make([]string, len(command.Args))
	for index, argument := range command.Args {
		arguments[index] = quoteWindowsCommandLineArgument(argument)
	}
	return strings.Join([]string{
		"$startInfo = [System.Diagnostics.ProcessStartInfo]::new()",
		"$startInfo.FileName = " + quotePowerShellArgument(command.Program),
		"$startInfo.UseShellExecute = $false",
		"$startInfo.Arguments = " + quotePowerShellArgument(strings.Join(arguments, " ")),
		"$process = [System.Diagnostics.Process]::Start($startInfo)",
		"if ($null -eq $process) { throw 'failed to start remediation process' }",
		"$process.WaitForExit()",
		"exit $process.ExitCode",
	}, "; ")
}

// quoteWindowsCommandLineArgument implements the CommandLineToArgvW-compatible
// escaping used by Go's Windows process launcher. ProcessStartInfo.Arguments
// accepts the resulting complete native command line even in Windows
// PowerShell 5.1, whose direct native-command binder loses several edge cases.
func quoteWindowsCommandLineArgument(value string) string {
	if value == "" {
		return `""`
	}
	needsQuotes := strings.ContainsAny(value, " \t")
	needsEscaping := strings.ContainsAny(value, `\"`)
	if !needsQuotes && !needsEscaping {
		return value
	}
	var result strings.Builder
	if needsQuotes {
		result.WriteByte('"')
	}
	slashes := 0
	for _, char := range value {
		switch char {
		case '\\':
			slashes++
			result.WriteRune(char)
		case '"':
			result.WriteString(strings.Repeat(`\`, slashes+1))
			slashes = 0
			result.WriteRune(char)
		default:
			slashes = 0
			result.WriteRune(char)
		}
	}
	if needsQuotes {
		result.WriteString(strings.Repeat(`\`, slashes))
		result.WriteByte('"')
	}
	return result.String()
}

func shellSafeBareArgument(value string) bool {
	return value != "" && strings.IndexFunc(value, func(char rune) bool {
		return !(unicode.IsLetter(char) || unicode.IsDigit(char) || strings.ContainsRune("_@%+=:,./-", char))
	}) < 0
}

func fencedCommand(command, language string) string {
	longest := 0
	current := 0
	for _, char := range command {
		if char == '`' {
			current++
			if current > longest {
				longest = current
			}
			continue
		}
		current = 0
	}
	fenceLength := longest + 1
	if fenceLength < 3 {
		fenceLength = 3
	}
	fence := strings.Repeat("`", fenceLength)
	return fence + language + "\n" + command + "\n" + fence
}
