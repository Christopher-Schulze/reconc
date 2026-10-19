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

// confirmOperatorPresence proves that an operator, not an agent tool call,
// runs a command that changes who governs agent sessions. The pre-tool guard
// only recognizes direct invocations; this check also covers scripts, aliases
// and copies, because an agent tool call has no interactive terminal and cannot
// read back a one-time code it never saw. Tests replace it.
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
		return &CLIError{ExitCode: 1, Message: action + " changes who governs agent sessions and needs the operator at an interactive terminal; piped and agent-run invocations are refused"}
	}
	fmt.Fprintf(prompt, "%s: type %s and press Enter to confirm: ", action, code)
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
