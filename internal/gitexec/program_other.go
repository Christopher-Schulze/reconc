//go:build !darwin

package gitexec

// gitProgram returns the program for a Git command, resolved through PATH.
func gitProgram() string {
	return "git"
}
