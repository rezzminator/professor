package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/clock"
	fleetindex "hostops/pfm/internal/index"
	"hostops/pfm/internal/store"
)

func runIndex(args []string, stdout, stderr io.Writer, runtime commandRuntime, clk clock.Clock) (exitCode int) {
	clk = defaultClock(clk)
	flags := cli.NewFlagSet(
		indexCommand,
		"usage: pfm index [--full] [--progress]",
		stderr,
	)
	full := flags.Bool("full", false, "reparse every indexed file")
	progress := flags.Bool("progress", false, "report start and elapsed time to stderr")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	database, err := store.Open(store.WithWarningWriter(stderr))
	if err != nil {
		fmt.Fprintf(stderr, "pfm index: %v\n", err)
		return 1
	}
	defer func() { cli.CloseResource(database, "pfm index: close database", stderr, &exitCode) }()
	indexer, err := fleetindex.NewWithRoots(database, runtime.Paths, runtime.Paths.Roots)
	if err != nil {
		fmt.Fprintf(stderr, "pfm index: %v\n", err)
		return 1
	}
	started := clk.Now()
	if *progress {
		fmt.Fprintln(stderr, "pfm index: scanning")
	}
	counters, err := indexer.Run(context.Background(), fleetindex.Options{Full: *full})
	if err != nil {
		fmt.Fprintf(stderr, "pfm index: %v\n", err)
		return 1
	}
	if *full {
		// The counters below say the reparse ran; this row is what makes the
		// NEXT run able to tell "fully indexed at T" from "never fully
		// indexed". A dropped write leaves the command claiming a completed
		// full index it cannot prove, so it is reported and exits non-zero —
		// the counters still print, because the scan itself did happen.
		if err := database.SetMeta(
			context.Background(),
			"last_full_index_at",
			strconv.FormatInt(clk.Now().Unix(), 10),
		); err != nil {
			fmt.Fprintf(stderr, "pfm index: record the full-index timestamp: %v\n", err)
			exitCode = 1
		}
	}
	fmt.Fprintln(stdout, formatCounters(counters))
	if *progress {
		fmt.Fprintf(stderr, "pfm index: done in %s\n", clk.Now().Sub(started).Round(time.Millisecond))
	}
	return exitCode
}

func formatCounters(counters fleetindex.Counters) string {
	return fmt.Sprintf(
		"files=%d skipped=%d delta=%d full=%d deleted=%d touched=%d bytes=%d cx_names=%t",
		counters.FilesSeen,
		counters.FilesSkipped,
		counters.DeltaParsed,
		counters.FullParsed,
		counters.Deleted,
		counters.RowsTouched,
		counters.BytesRead,
		counters.CxNamesReloaded,
	)
}
