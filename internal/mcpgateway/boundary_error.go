package mcpgateway

type boundaryError struct {
	operation string
	cause     error
}

func (e *boundaryError) Error() string {
	return e.operation + " failed"
}

func (e *boundaryError) Unwrap() error {
	return e.cause
}

func wrapBoundaryError(operation string, cause error) error {
	if cause == nil {
		return nil
	}
	return &boundaryError{operation: operation, cause: cause}
}
