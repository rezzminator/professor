package main

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"hostops/pfm/internal/cli"
	fleetindex "hostops/pfm/internal/index"
	"hostops/pfm/internal/store"
)

func runIndex(args []string, stdout, stderr io.Writer, runtime commandRuntime) (exitCode int) {
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
	started := time.Now()
	if *progress {
		fmt.Fprintln(stderr, "pfm index: scanning")
	}
	counters, err := indexer.Run(context.Background(), fleetindex.Options{Full: *full})
	if err != nil {
		fmt.Fprintf(stderr, "pfm index: %v\n", err)
		return 1
	}
	if *full {
		_ = database.SetMeta(
			context.Background(),
			"last_full_index_at",
			strconv.FormatInt(time.Now().Unix(), 10),
		)
	}
	fmt.Fprintln(stdout, formatCounters(counters))
	if *progress {
		fmt.Fprintf(stderr, "pfm index: done in %s\n", time.Since(started).Round(time.Millisecond))
	}
	return 0
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
