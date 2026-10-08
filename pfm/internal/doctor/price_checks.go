package doctor

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
)

// priceRefreshTimeout bounds doctor's price refresh, so an unreachable
// publisher costs a named warning, never a hung doctor.
const priceRefreshTimeout = 30 * time.Second

// printPrices refreshes the price table — at most once a day, never with
// PFM_PRICES_OFFLINE=1 — and reports it in one row, then one warning row per
// problem: a failed fetch, a refresh that could not be persisted, a stamp that
// could not be read or written, an unusable clone file, an invalid override.
// Offline and current are no warning. It returns the number of warnings.
func printPrices(stdout io.Writer, runtime config.Runtime, dependencies Dependencies) int {
	ctx, cancel := context.WithTimeout(context.Background(), priceRefreshTimeout)
	defer cancel()
	prices, result, err := modelcost.Refresh(ctx, modelcost.Options{
		Home:       runtime.Paths.Home,
		ConfigPath: runtime.Config.Path,
		Offline:    paths.PricesOfflineIn(dependencies.Env),
		Clock:      dependencies.Clock,
		Fetch:      dependencies.FetchPrices,
	})
	warnings := 0
	warning := func(name string, cause error) {
		if cause != nil {
			fmt.Fprintf(stdout, "doctor: prices warning=%s error=%v\n", name, cause)
			warnings++
		}
	}
	if err != nil {
		fmt.Fprintf(stdout, "doctor: prices refresh=%s override=invalid error=%v\n", result.Status, err)
		warnings++
		warning("fetch-failed", result.Err)
		warning("not-persisted", result.PersistErr)
		warning("stamp", result.StampErr)
		return warnings
	}
	from := prices.From
	if prices.File != "" {
		from += " file=" + prices.File
	}
	override := emptySummary
	if prices.Override != nil {
		override = fmt.Sprintf("active rows=%d path=%s", prices.Override.Rows, prices.Override.Path)
	}
	fmt.Fprintf(stdout, "doctor: prices rows=%d fetched=%s from=%s refresh=%s override=%s\n",
		len(prices.Rows), prices.FetchedAt, from, result.Status, override)
	warning("fetch-failed", result.Err)
	warning("not-persisted", result.PersistErr)
	warning("stamp", result.StampErr)
	warning("clone-file", prices.FileError)
	return warnings
}
