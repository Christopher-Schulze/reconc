package actionledger

import (
	"errors"
	"fmt"

	"reconc.dev/reconc/internal/action"
)

type Error struct {
	Code  action.ReasonCode
	Cause error
}

func (e *Error) Error() string {
	if e == nil || e.Cause == nil {
		return "action ledger error"
	}
	return e.Cause.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func ledgerError(code action.ReasonCode, message string, cause error) error {
	if code != action.ReasonLedgerUnavailable && code != action.ReasonLedgerCorrupt {
		code = action.ReasonLedgerCorrupt
	}
	if cause != nil {
		cause = fmt.Errorf("%s: %w", message, cause)
	} else {
		cause = errors.New(message)
	}
	return &Error{Code: code, Cause: cause}
}

func ErrorCode(err error) action.ReasonCode {
	var ledgerErr *Error
	if errors.As(err, &ledgerErr) {
		return ledgerErr.Code
	}
	if err != nil {
		return action.ReasonLedgerUnavailable
	}
	return ""
}

func wrapLedgerError(code action.ReasonCode, message string, err error) error {
	var existing *Error
	if errors.As(err, &existing) {
		return err
	}
	return ledgerError(code, message, err)
}
