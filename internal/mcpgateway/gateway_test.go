package mcpgateway

import (
	"context"
	"errors"
)

func (g *Gateway) finalizePendingAndDrainCalls(ctx context.Context) error {
	pendingErr := g.shutdownPending(ctx)
	drainErr := g.waitForCalls(ctx)
	return errors.Join(pendingErr, drainErr)
}
