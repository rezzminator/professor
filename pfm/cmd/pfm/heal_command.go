package main

import (
	"context"
	"fmt"
	"io"
	"sort"

	"github.com/rezzminator/professor/pfm/internal/cli"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/heal"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// runHeal reports, and on request repairs, Codex thread-history projections.
//
// Report is the default because a heal DELETES rows: they are rebuildable
// from the rollout, which is why this is safe at all, but a rebuild is only
// free while the rollout is whole.
func runHeal(args []string, stdout, stderr io.Writer) int {
	flags := cli.NewFlagSet(
		"heal",
		"usage: pfm heal [--apply | --thread id]",
		stderr,
	)
	apply := flags.Bool("apply", false, "heal every broken thread that is not live")
	thread := flags.String("thread", "", "heal one thread if its projection is broken")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || (*apply && *thread != "") {
		flags.Usage()
		return 2
	}

	resolved, err := paths.Resolve()
	if err != nil {
		fmt.Fprintf(stderr, "pfm heal: %v\n", err)
		return 1
	}
	// --thread is the pre-resume shape: silent when there is nothing to do,
	// and exit 0 whatever it finds, so a resume path can call it for free.
	if *thread != "" {
		if message := heal.Thread(
			context.Background(),
			resolved.FirstRoot(pfmengine.Codex),
			*thread,
		); message != "" {
			fmt.Fprintln(stderr, message)
		}
		return 0
	}

	runner, err := heal.New(resolved.FirstRoot(pfmengine.Codex), nil)
	if err != nil {
		fmt.Fprintf(stderr, "pfm heal: %v\n", err)
		return 1
	}
	report, err := runner.Run(context.Background(), heal.Options{Apply: *apply})
	if err != nil {
		fmt.Fprintf(stderr, "pfm heal: %v\n", err)
		return 1
	}
	printHealReport(report, *apply, stdout)
	return 0
}

func printHealReport(report heal.Report, apply bool, stdout io.Writer) {
	for _, thread := range report.Threads {
		if !thread.Verdict.Broken() && !thread.Verdict.LeftAlone() {
			continue
		}
		fmt.Fprintf(
			stdout,
			"%s\t%s\t%s\n",
			thread.Verdict,
			thread.ID,
			thread.Detail,
		)
	}
	verdicts := make([]string, 0, len(report.Totals))
	for verdict := range report.Totals {
		verdicts = append(verdicts, string(verdict))
	}
	sort.Strings(verdicts)
	line := "totals:"
	for _, verdict := range verdicts {
		line += fmt.Sprintf(" %s=%d", verdict, report.Totals[heal.Verdict(verdict)])
	}
	fmt.Fprintln(stdout, line)
	if report.Totals[heal.VerdictNoncanonical] > 0 {
		fmt.Fprintf(
			stdout,
			"NONCANONICAL threads are never rebuilt: the rebuild from zero fails on the "+
				"same record on Codex < %s — upgrade Codex to >= %s and resume the thread; "+
				"its projector skips the record.\n",
			heal.CodexProjectsPastAnomalies,
			heal.CodexProjectsPastAnomalies,
		)
	}
	if report.Totals[heal.VerdictUnscanned] > 0 {
		fmt.Fprintln(
			stdout,
			"UNSCANNED threads are never rebuilt: the rollout could not be read end to "+
				"end — fix the read error and re-run.",
		)
	}
	if !apply {
		if report.Totals[heal.VerdictWedged]+report.Totals[heal.VerdictMidline] > 0 {
			fmt.Fprintln(
				stdout,
				"report only — re-run with --apply to rebuild these projections.",
			)
		}
		return
	}
	if report.BackupDir != "" {
		fmt.Fprintf(stdout, "backed up the history store -> %s\n", report.BackupDir)
	}
	fmt.Fprintf(
		stdout,
		"healed=%d skipped_live=%d left_alone=%d — healed threads rebuild at their next resume\n",
		len(report.Healed),
		len(report.SkippedLive),
		len(report.LeftAlone),
	)
	for _, id := range report.SkippedLive {
		fmt.Fprintf(stdout, "SKIP live thread %s\n", id)
	}
}
