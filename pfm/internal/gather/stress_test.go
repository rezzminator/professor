package gather

import (
	"context"
)

type gatedTmuxClient struct {
	inner   TmuxClient
	started chan<- string
	release <-chan struct{}
}

func (client gatedTmuxClient) ListPanes(
	ctx context.Context,
	socket string,
) ([]ProbePane, error) {
	select {
	case client.started <- socket:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-client.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return client.inner.ListPanes(ctx, socket)
}
