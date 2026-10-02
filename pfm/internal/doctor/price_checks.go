package doctor

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// printPriceOverride reports whether a pfm.prices.json beside the config file
// is merged into the price table. An active override is no warning; an
// invalid one is one warning. It returns the number of warnings.
func printPriceOverride(stdout io.Writer, runtime config.Runtime) int {
	if runtime.Config.Path == "" {
		fmt.Fprintln(stdout, "doctor: prices override=none")
		return 0
	}
	pricesPath := config.PricesPath(runtime.Config.Path)
	table, err := pricing.Effective(runtime.Config.Path)
	switch {
	case err != nil:
		fmt.Fprintf(stdout, "doctor: prices override=invalid path=%s error=%v\n", pricesPath, err)
		return 1
	case table.Override == nil:
		fmt.Fprintf(stdout, "doctor: prices override=none path=%s\n", pricesPath)
	default:
		fmt.Fprintf(
			stdout,
			"doctor: prices override=active rows=%d path=%s\n",
			table.Override.Rows,
			table.Override.Path,
		)
	}
	return 0
}
