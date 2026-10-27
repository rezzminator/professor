package installer

import (
	"context"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func lookupLoginShell(ctx context.Context, runner deps.Runner, uid int) (string, error) {
	return lookupDarwinLoginShell(ctx, runner, uid)
}
