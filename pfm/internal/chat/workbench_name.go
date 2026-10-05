package chat

import (
	"context"
	"io"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// WorkbenchName reserves numbers against every roster row, including killed chats.
func WorkbenchName(ctx context.Context, cwd string, warn io.Writer, runtime *pfmconfig.Runtime) (string, bool, error) {
	bench, found, err := workbench.Nearest(cwd)
	if err != nil || !found {
		return "", false, err
	}
	if bench.Err != nil {
		return "", false, bench.Err
	}
	rows, err := Rows(ctx, warn, runtime)
	if err != nil {
		obs.Logger(ctx).Error("workbench roster", "path", cwd, obs.FieldErr, err)
		return "", false, err
	}
	names := make([]string, 0, len(rows))
	for i := range rows {
		names = append(names, rows[i].Name)
	}
	return naming.NextNumbered(bench.Prefix, names), true, nil
}
