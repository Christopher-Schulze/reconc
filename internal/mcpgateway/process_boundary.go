package mcpgateway

import "os"

type processBoundary interface {
	Attach(*os.Process) error
	Terminate() error
	Kill() error
	Close() error
}
