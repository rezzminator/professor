package update

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// restoreRefreshedPrices puts the clone's tracked prices.json back to HEAD
// when only its working copy changed — what `pfm model-cost` writes on a
// refresh — so a price change never blocks an update; the release carries its
// own table. A staged change is the user's and stays for the dirty check.
func restoreRefreshedPrices(ctx context.Context, repo string, stdout io.Writer) error {
	status, err := updateGitOutput(ctx, repo, "status", "--porcelain", "--", pricing.FileRel)
	if err != nil {
		return fmt.Errorf("inspect refreshed prices: %w", err)
	}
	if !strings.HasPrefix(status, " M ") {
		return nil
	}
	if err := updateGitRun(ctx, repo, "checkout", "--", pricing.FileRel); err != nil {
		return fmt.Errorf("restore refreshed prices: %w", err)
	}
	fmt.Fprintf(stdout, "update: restored the refreshed %s; the release carries its own price table\n", pricing.FileRel)
	return nil
}
