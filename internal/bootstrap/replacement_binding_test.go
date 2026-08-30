//go:build !windows

package bootstrap

func replacementBindingDenied(_ error) bool {
	return false
}
