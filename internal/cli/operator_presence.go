package cli

import (
	"bufio"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
)

// confirmOperatorPresence adds explicit local confirmation to administration.
// A terminal and a displayed code are interaction requirements, not proof of
// human identity: another process with the same privileges can automate a PTY
// or change local state. Tests replace this input boundary.
var confirmOperatorPresence = func(action string, prompt io.Writer) error {
	code, err := operatorConfirmationCode()
	if err != nil {
		return &CLIError{ExitCode: 1, Message: action + ": create confirmation code: " + err.Error()}
	}
	return confirmOperatorAtTerminal(action, os.Stdin, inputIsTerminal(os.Stdin), prompt, code)
}

// confirmOperatorAtTerminal requires an interactive terminal on input and the
// exact code typed back; anything else leaves state unchanged.
func confirmOperatorAtTerminal(action string, input io.Reader, isTerminal bool, prompt io.Writer, code string) error {
	if !isTerminal {
		return &CLIError{ExitCode: 1, Message: action + " changes who governs agent sessions and requires local confirmation at an interactive terminal; non-terminal input is refused"}
	}
	if _, err := fmt.Fprintln(prompt, "Local confirmation only; this does not authenticate a human or isolate same-user processes."); err != nil {
		return &CLIError{ExitCode: 1, Message: action + ": write confirmation notice: " + err.Error()}
	}
	if _, err := fmt.Fprintf(prompt, "%s: type %s and press Enter to confirm: ", action, code); err != nil {
		return &CLIError{ExitCode: 1, Message: action + ": write confirmation prompt: " + err.Error()}
	}
	line, err := bufio.NewReader(io.LimitReader(input, 64)).ReadString('\n')
	if err != nil && err != io.EOF {
		return &CLIError{ExitCode: 1, Message: action + ": read confirmation: " + err.Error()}
	}
	if strings.TrimSpace(line) != code {
		return &CLIError{ExitCode: 1, Message: action + ": confirmation code did not match; nothing changed"}
	}
	return nil
}

func operatorConfirmationCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}
