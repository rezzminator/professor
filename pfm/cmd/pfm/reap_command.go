package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/reap"
)

// runReap sweeps the chat socket graveyard: the tmux servers (and their
// ~0.5-1 GB chat processes) that outlive the terminal tabs they were opened
// in, the socket files crashed servers leave behind, and — the idle-horizon
// rule — a chat left open on the operator's screen with no real prompt or
// response inside it for days. "idle" here means the timestamp inside the
// transcript's own last parseable record, never tmux's #{window_activity}
// (which redraws forever) and never the transcript file's raw mtime (which
// bookkeeping records touch every 30-60 minutes on their own).
//
// The default is a DRY RUN. A wrongly kept socket costs memory; a wrongly
// killed one costs a chat nobody can get back, so killing has to be asked for.
func runReap(args []string, stdout, stderr io.Writer, runtime commandRuntime) int {
	flags := newFlagSet(
		"reap",
		"usage: pfm reap [--apply] [--horizon 48h] [--busy-recent SECONDS] [--json]",
		stderr,
	)
	apply := flags.Bool(
		"apply",
		false,
		"kill idle-past-horizon and unattached orphan chats, and remove dead socket files",
	)
	horizon := flags.Duration(
		"horizon",
		48*time.Hour,
		"how long an ATTACHED chat may sit with no last registered prompt or response before it is reapable",
	)
	busyRecent := flags.Int(
		"busy-recent",
		60,
		"seconds of transcript writes that count as a working chat",
	)
	asJSON := flags.Bool(jsonFormat, false, "emit one JSON report instead of the text table")
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *busyRecent < 0 || *horizon < 0 {
		flags.Usage()
		return 2
	}

	resolved := runtime.Paths
	configDirs := make([]string, 0, len(runtime.Config.Accounts))
	for _, account := range runtime.Config.Accounts {
		configDirs = append(configDirs, account.ConfigDir)
	}
	ctx := context.Background()
	runner, err := reap.New(reap.Dependencies{
		Paths:          resolved,
		Busy:           reap.NewClaudeAgentsConfigured(resolved, runtime.Config.Claude.Binary, configDirs),
		ClaudeBinary:   runtime.Config.Claude.Binary,
		CodexBinary:    runtime.Config.Codex.Binary,
		OpenCodeBinary: runtime.Config.OpenCode.Binary,
		CodexHomes:     runtime.Config.CodexHomes(),
		KillServer: func(ctx context.Context, socket string) error {
			return killChatServer(ctx, resolved, socket)
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm reap: %v\n", err)
		return 1
	}
	report, err := runner.Run(ctx, reap.Options{
		Apply:      *apply,
		BusyRecent: time.Duration(*busyRecent) * time.Second,
		Horizon:    *horizon,
		Self:       fleet.CurrentSocket(),
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm reap: %v\n", err)
		return 1
	}
	if err := printReapReport(report, *apply, *asJSON, stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "pfm reap: encode JSON: %v\n", err)
		return 1
	}
	// A sweep that reports success having failed to kill is the failure mode
	// this command exists to prevent: any decision apply actually attempted
	// and could not complete forces the exit code non-zero.
	if report.Failed > 0 {
		return 1
	}
	return 0
}

// groupReapDecisions splits one sweep's decisions into the three clearly
// separated groups the order asked for: REAP (idle past horizon, unattached
// orphans, and dead socket files — everything this sweep marked or made
// reapable), SPARED (kept, with the reason), and UNKNOWN (could not be read
// — never killed on an absent measurement). A run that could not read
// anything prints a populated UNKNOWN section; a run that found nothing to
// do prints all three sections empty. Those are never the same output.
func groupReapDecisions(decisions []reap.Decision) (reapGroup, sparedGroup, unknownGroup []reap.Decision) {
	for _, decision := range decisions {
		switch decision.State {
		case reap.StateOrphan, reap.StateFork, reap.StateIdle, reap.StateDead, reap.StateKilled:
			reapGroup = append(reapGroup, decision)
		case reap.StateUnknown:
			unknownGroup = append(unknownGroup, decision)
		default:
			sparedGroup = append(sparedGroup, decision)
		}
	}
	return reapGroup, sparedGroup, unknownGroup
}

func printReapReport(
	report reap.Report,
	apply bool,
	asJSON bool,
	stdout, stderr io.Writer,
) error {
	if !report.AgentsOK {
		fmt.Fprintf(
			stderr,
			"pfm reap: busy state unknown (%s) — every chat with a breadcrumb is skipped\n",
			report.BusyError,
		)
	}

	reapGroup, sparedGroup, unknownGroup := groupReapDecisions(report.Decisions)

	if asJSON {
		return writeJSON(stdout, reapJSONReport{
			Apply:     apply,
			Reap:      toReapJSONRows(reapGroup),
			Spared:    toReapJSONRows(sparedGroup),
			Unknown:   toReapJSONRows(unknownGroup),
			Keep:      report.Keep,
			DeadFiles: report.DeadFiles,
			Killed:    report.Killed,
			Failed:    report.Failed,
			Warnings:  report.Warnings,
		})
	}

	fmt.Fprintf(stdout, "%-38s %-6s %8s  %s\n", "SOCKET", "STATE", "RAM(MB)", "LABEL [cwd]")
	printReapSection(stdout, "REAP", reapGroup)
	printReapSection(stdout, "SPARED", sparedGroup)
	printReapSection(stdout, "UNKNOWN", unknownGroup)

	fmt.Fprintln(stdout)
	fmt.Fprintf(
		stdout,
		"KEEP: %d (~%d MB)   REAP: %d (~%d MB summed RSS)   dead files: %d   UNKNOWN: %d\n",
		report.Keep,
		report.KeepKB/1024,
		report.Orphans,
		report.OrphanKB/1024,
		report.DeadFiles,
		report.Unknown,
	)
	if !apply {
		fmt.Fprintln(
			stdout,
			"dry run — nothing changed. Re-run with --apply to reap.",
		)
		return nil
	}
	fmt.Fprintf(
		stdout,
		"reaped %d socket(s), ~%d MB summed RSS\n",
		report.Killed,
		report.FreedKB/1024,
	)
	if report.Failed > 0 {
		fmt.Fprintf(
			stdout,
			"FAILED: %d reap attempt(s) did not complete — see the reasons above\n",
			report.Failed,
		)
	}
	// A warning never touches the exit code — it holds up nothing this
	// sweep answers for — but it is never swallowed either.
	for _, warning := range report.Warnings {
		fmt.Fprintf(stderr, "pfm reap: warning: %s\n", warning)
	}
	if report.AvailBefore > 0 || report.AvailAfter > 0 {
		fmt.Fprintf(
			stdout,
			"memory available: %d MB before, %d MB after (the honest reclaim)\n",
			report.AvailBefore/1024,
			report.AvailAfter/1024,
		)
	}
	return nil
}

func printReapSection(w io.Writer, name string, decisions []reap.Decision) {
	fmt.Fprintf(w, "-- %s (%d) --\n", name, len(decisions))
	if len(decisions) == 0 {
		fmt.Fprintln(w, "  (none)")
		return
	}
	for _, decision := range decisions {
		fmt.Fprintln(w, formatReapRow(decision))
	}
}

func formatReapRow(decision reap.Decision) string {
	detail := decision.Label
	if decision.CWD != "" {
		detail = fmt.Sprintf("%s [%s]", detail, decision.CWD)
	}
	if decision.Reason != "" {
		detail = fmt.Sprintf("%s — %s", decision.Reason, detail)
	}
	return fmt.Sprintf(
		"%-38s %-6s %8d  %s",
		decision.Socket,
		decision.State,
		decision.RSSKB/1024,
		detail,
	)
}

// reapJSONReport is --json's whole shape: the same three groups as the text
// table, plus the summary counters printed after them.
type reapJSONReport struct {
	Apply     bool          `json:"apply"`
	Reap      []reapJSONRow `json:"reap"`
	Spared    []reapJSONRow `json:"spared"`
	Unknown   []reapJSONRow `json:"unknown"`
	Keep      int           `json:"keep"`
	DeadFiles int           `json:"dead_files"`
	Killed    int           `json:"killed"`
	Failed    int           `json:"failed"`
	Warnings  []string      `json:"warnings,omitempty"`
}

type reapJSONRow struct {
	Socket string `json:"socket"`
	State  string `json:"state"`
	Reason string `json:"reason"`
	Label  string `json:"label,omitempty"`
	CWD    string `json:"cwd,omitempty"`
	RSSMB  int64  `json:"rss_mb"`
}

func toReapJSONRows(decisions []reap.Decision) []reapJSONRow {
	rows := make([]reapJSONRow, 0, len(decisions))
	for _, decision := range decisions {
		rows = append(rows, reapJSONRow{
			Socket: decision.Socket,
			State:  string(decision.State),
			Reason: decision.Reason,
			Label:  decision.Label,
			CWD:    decision.CWD,
			RSSMB:  decision.RSSKB / 1024,
		})
	}
	return rows
}
