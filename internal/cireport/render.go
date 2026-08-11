package cireport

import "fmt"

// Render serializes one validated neutral report in the selected format.
func Render(format Format, model Model) ([]byte, error) {
	if err := validateModel(model); err != nil {
		return nil, err
	}
	var body []byte
	var err error
	switch format {
	case FormatSARIF:
		body, err = renderSARIF(model)
	case FormatJUnit:
		body, err = renderJUnit(model)
	case FormatGitHub:
		body, err = renderGitHub(model)
	default:
		return nil, fmt.Errorf("unsupported CI report format %q", format)
	}
	if err != nil {
		return nil, err
	}
	if len(body) > MaxBytes {
		return nil, fmt.Errorf("%s report exceeds %d bytes", format, MaxBytes)
	}
	return body, nil
}

func appendBoundedNewline(body []byte) ([]byte, error) {
	if len(body) >= MaxBytes {
		return nil, fmt.Errorf("CI report exceeds %d bytes", MaxBytes)
	}
	return append(body, '\n'), nil
}

func decisionExitCode(decision string) int {
	if decision == "block" {
		return 2
	}
	return 0
}
