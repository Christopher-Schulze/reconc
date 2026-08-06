//go:build !windows

package runtime

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestConfigureScriptProcessUsesUnixProcessGroup(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	done := make(chan struct{})
	configureScriptProcess(context.Background(), cmd, done, 2*time.Second)
	close(done)

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setpgid {
		t.Fatal("unix script process must run in its own process group")
	}
	if cmd.Cancel == nil {
		t.Fatal("unix script process must install process-group cancellation")
	}
	if cmd.WaitDelay != 2*time.Second {
		t.Fatalf("expected WaitDelay=2s, got %s", cmd.WaitDelay)
	}
}
