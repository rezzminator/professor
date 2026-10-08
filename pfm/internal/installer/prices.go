package installer

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
)

// priceRefreshTimeout bounds install's price refresh, so an unreachable
// publisher costs a warning line, never a stalled install.
const priceRefreshTimeout = 30 * time.Second

// RefreshPrices refreshes the price table after a successful apply — at most
// once a day, never with PFM_PRICES_OFFLINE=1 — and reports it in one ok line;
// every problem is a named warn line, never a failed install.
func RefreshPrices(stdout io.Writer, options modelcost.Options) {
	ctx, cancel := context.WithTimeout(context.Background(), priceRefreshTimeout)
	defer cancel()
	prices, result, err := modelcost.Refresh(ctx, options)
	if err != nil {
		fmt.Fprintf(stdout, "  warn    prices: %v\n", err)
		return
	}
	switch {
	case result.Err != nil:
		fmt.Fprintf(
			stdout,
			"  warn    prices refresh failed, serving the table fetched %s: %v\n",
			prices.FetchedAt,
			result.Err,
		)
	case result.PersistErr != nil:
		fmt.Fprintf(stdout, "  warn    prices refreshed but not persisted: %v\n", result.PersistErr)
	default:
		fmt.Fprintf(
			stdout,
			"  ok      prices %s · %d rows · fetched %s\n",
			result.Status,
			len(prices.Rows),
			prices.FetchedAt,
		)
	}
	if result.StampErr != nil {
		fmt.Fprintf(stdout, "  warn    prices check stamp: %v\n", result.StampErr)
	}
	if prices.FileError != nil {
		fmt.Fprintf(
			stdout,
			"  warn    prices clone file unusable, serving the %s table: %v\n",
			prices.From,
			prices.FileError,
		)
	}
}
