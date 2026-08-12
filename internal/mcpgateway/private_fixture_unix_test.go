//go:build !windows

package mcpgateway

import "os"

func securePrivateGatewayFixture(path string) error {
	return os.Chmod(path, 0o600)
}
