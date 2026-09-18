// Package picker is the pfm ls loop: cached first frame, streamed refreshes
// with idle backoff, the cosmos sampler, and key to action over fleet.Scan,
// with every row action delegated to chat.
package picker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	pfmchat "hostops/pfm/internal/chat"
	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/clock"
	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/kill"
	"hostops/pfm/internal/paths"
	pfmstats "hostops/pfm/internal/stats"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/ui"
)

func Run(
	args []string,
	stdout, stderr io.Writer,
	runtime pfmconfig.Runtime,
) (exitCode int) {
	flags := cli.NewFlagSet(
		"ls",
		"usage: pfm ls [-a|--all] [--plain|--tsv] [id] | pfm ls --killed [--tsv]",
		stderr,
	)
	var all bool
	var killed bool
	flags.BoolVar(&all, "a", false, "include killed, background, and uncapped rows")
	flags.BoolVar(&all, "all", false, "include killed, background, and uncapped rows")
	flags.BoolVar(&killed, "K", false, "list the killed ledger")
	flags.BoolVar(&killed, "killed", false, "list the killed ledger")
	plain := flags.Bool("plain", false, "render a noninteractive list")
	tsv := flags.Bool("tsv", false, "render stable tab-separated rows")
	noSky := flags.Bool("no-sky", false, "disable the interactive sky widget")
	safe := flags.String(
		"safe",
		"auto",
		"vscode-safe cosmos rendering: auto|on|off (auto arms when TERM_PROGRAM=vscode)",
	)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() > 1 ||
		boolCount(*plain, *tsv) > 1 {
		flags.Usage()
		return 2
	}
	if *safe != "auto" && *safe != "on" && *safe != "off" {
		fmt.Fprintf(stderr, "pfm ls: --safe must be auto, on, or off (got %q)\n", *safe)
		return 2
	}
	// Read first, then arm the detached lookup: a network result from THIS
	// invocation is deliberately eligible only for the NEXT `pfm ls`.
	updateRow, hasUpdate := cachedProfessorUpdateRow(runtime)
	triggerProfessorUpdateCheck(runtime)
	if killed {
		if flags.NArg() != 0 || all || *plain {
			flags.Usage()
			return 2
		}
		// The killed ledger is already a stable three-column TSV contract.
		// Accepting --tsv makes that format explicit for scripts instead of
		// returning an empty success or rejecting a harmless format request.
		return listKilled(nil, stdout, stderr, runtime)
	}
	if flags.NArg() == 1 {
		if all || *plain || *tsv {
			flags.Usage()
			return 2
		}
		return pfmchat.OpenID(context.Background(), flags.Arg(0), stdout, stderr, &runtime)
	}

	view := compose.DefaultView
	if all {
		view = compose.AllView
	}
	ctx := context.Background()
	database, err := store.Open(store.WithWarningWriter(stderr))
	if err != nil {
		fmt.Fprintf(stderr, "pfm ls: %v\n", err)
		return 1
	}
	defer func() { cli.CloseResource(database, "pfm ls: close database", stderr, &exitCode) }()
	sharedState := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() {
		if err := sharedState.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm ls: close shared state: %v\n", err)
		}
	}()

	request := scanRequest{
		View: view,
		// Fleet-wide picker: no chat chosen yet, so no per-account override applies.
		Cache1H: runtime.Config.InitialCache1H(0),
		NoSky:   *noSky,
		Safe:    *safe,
		Runtime: &runtime,
		Comms:   sharedState,
	}
	var scan scanResult
	var outcome ui.Outcome
	if *plain || *tsv {
		scan, err = scanFleet(ctx, database, request, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "pfm ls: %v\n", err)
			return 1
		}
		var picker ui.Picker
		if *plain {
			picker = ui.PlainPicker{Writer: stdout}
		} else {
			picker = ui.TSVPicker{Writer: stdout}
		}
		outcome, err = picker.Pick(ctx, scan.Snapshot)
	} else {
		scan, err = scanFleetCached(ctx, database, request)
		if err != nil {
			fmt.Fprintf(stderr, "pfm ls: %v\n", err)
			return 1
		}
		if hasUpdate {
			scan.Snapshot.Rows = append([]compose.Row{updateRow}, scan.Snapshot.Rows...)
		}
		applier, applierErr := killApplier(ctx, database, runtime)
		if applierErr != nil {
			fmt.Fprintf(stderr, "pfm ls: %v\n", applierErr)
			return 1
		}
		scan.Snapshot.ApplyKill = applier
		scan.Snapshot.ApplyDeactivate = deactivateApplier(ctx, scan.Paths)
		scan.Snapshot.MergeNewChat = true
		statsSampler := pfmstats.NewSampler(
			scan.Paths.ProcRoot,
			scan.Paths.CgroupRoot,
		)
		statsSampler.Limits = pfmstats.NewLimitsSampler(limitAccounts(runtime))
		statsSampler.Limits.TTL = pfmstats.LiveLimitsTTL
		// Codex's own fetch execs `codex app-server` (unlike Claude's cheap
		// disk-cache-backed HTTP path) — see CodexLiveLimitsTTL.
		statsSampler.Limits.CodexTTL = pfmstats.CodexLiveLimitsTTL
		scan.Snapshot.StatsSampler = statsSampler
		scan.Snapshot.CosmosSampler = cosmosSampler{reader: sharedState}
		refreshContext, refreshCancel := context.WithCancel(ctx)
		updates := make(chan ui.Snapshot, 1)
		// Bubble Tea owns the tty for as long as Pick runs: a probe warning
		// the background refresh raises mid-frame corrupts the alt-screen,
		// so it is buffered here and flushed only once Pick has released
		// the terminal.
		var warnings bufferedWarnings
		// Opening the picker IS an interaction, so the clock starts stamped
		// and the first frames refresh at full cadence. Every keystroke
		// restamps it; going quiet is what makes the stream back off.
		activity := ui.NewActivityClock(clock.Real.Now())
		scan.Snapshot.Activity = activity
		go streamFleetRefreshes(
			refreshContext,
			database,
			request,
			warnings.add,
			stderr,
			updates,
			activity,
		)
		outcome, err = (ui.BubblePicker{Updates: updates}).Pick(
			ctx,
			scan.Snapshot,
		)
		refreshCancel()
		warnings.flush(stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm ls: %v\n", err)
		return 1
	}
	if *plain || *tsv {
		return 0
	}
	// Every ⌃X already landed the moment it was typed, so there is nothing
	// here to apply and no exit key that can lose one. What remains is the
	// receipt, printed once the picker has released the terminal.
	reportKills(outcome.KillChanges, stderr)
	// A ⌃S account switch IS still a pending intent, and backing out with
	// Esc/⌃C must not write it. Nor does a non-positive PrimaryAccount ever
	// mean a deliberate choice — see primaryWriteback.
	claudePrimary := outcome.ClaudePrimaryAccount
	if claudePrimary == 0 && compose.EngineForKind(outcome.Row.Kind) != pfmengine.Codex {
		claudePrimary = outcome.PrimaryAccount
	}
	if account, should := primaryWriteback(
		outcome.Kind,
		claudePrimary,
		fleet.PrimaryAccount(scan.Paths, runtime.Config),
	); should {
		if err := fleet.SetPrimaryAccount(scan.Paths, runtime.Config, account); err != nil {
			fmt.Fprintf(stderr, "pfm ls: save primary account: %v\n", err)
			return 1
		}
	}
	cache1H := outcome.Cache1H
	switch outcome.Kind {
	case ui.OutcomeProfessorUpdate:
		return openProfessorUpdate(ctx, outcome, stdout, stderr, runtime)
	case ui.OutcomeDeactivate:
		if outcome.Row.Socket == "" {
			fmt.Fprintf(stderr, "pfm ls: deactive %s: no live server\n", outcome.Row.Name)
			return 1
		}
		if outcome.Row.Kind == compose.LiveSplit {
			fmt.Fprintln(stderr, "pfm ls: deactive refused for split live window")
			return 1
		}
		if err := pfmchat.KillServer(ctx, scan.Paths, outcome.Row.Socket); err != nil {
			fmt.Fprintf(stderr, "pfm ls: deactive %s: %v\n", outcome.Row.Name, err)
			return 1
		}
		fmt.Fprintf(stderr, "pfm ls: deactivated %s — ready to resume\n", outcome.Row.Name)
		return 0
	case ui.OutcomeReboot:
		row, err := rebootRow(ctx, scan.Paths, outcome.Row, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "pfm ls: %v\n", err)
			return 1
		}
		return pfmchat.OpenRow(ctx, row, outcome.PrimaryAccount, cache1H, "", stdout, stderr, &runtime)
	case ui.OutcomeSelected:
		selectedRow, err := resolveSelectedRow(ctx, database, outcome.Row, &runtime, stderr)
		if err != nil {
			fmt.Fprintf(stderr, "pfm ls: %v\n", err)
			return 1
		}
		return pfmchat.OpenRow(
			ctx,
			selectedRow,
			outcome.PrimaryAccount,
			cache1H,
			"",
			stdout,
			stderr,
			&runtime,
		)
	case ui.OutcomeCancelled, ui.OutcomeNone:
		return 0
	default:
		fmt.Fprintf(stderr, "pfm ls: unsupported picker outcome %d\n", outcome.Kind)
		return 1
	}
}

// resolveSelectedRow re-resolves an OutcomeSelected row against the current
// fleet before OpenRow acts on it. The picker's first frame comes from
// scanFleetCached, which composes over an EMPTY gather (Run above) — a chat
// that goes live after that frame was painted still renders as a stale
// Resume row when Enter lands, and opening it straight would have OpenRow's
// Resume branch synthesize a FRESH server for a chat that is already
// running: a second seat of the same conversation. fleet.Scan, ReadOnly, is
// the same live re-scan chat.OpenID already resolves an id through before
// opening it (internal/chat/open.go) — reusing it here is what lets a row
// that is live NOW attach instead of resuming.
//
// A row with no ID (a new-chat or split row) or one that is not a Resume
// kind is already the freshest thing the picker can act on, so there is
// nothing to re-resolve and no extra scan is paid for it. A scan failure is
// returned rather than swallowed: silently keeping the stale row here is
// exactly the hazard this function exists to close.
func resolveSelectedRow(
	ctx context.Context,
	database *store.Store,
	row compose.Row,
	runtime *pfmconfig.Runtime,
	stderr io.Writer,
) (compose.Row, error) {
	if row.ID == "" || !isResumeRowKind(row.Kind) {
		return row, nil
	}
	scan, err := fleet.Scan(ctx, database, fleet.Request{
		View: compose.AllView, ReadOnly: true, Runtime: runtime,
	}, stderr)
	if err != nil {
		return compose.Row{}, fmt.Errorf("re-resolve %s before open: %w", row.ID, err)
	}
	for index := range scan.Output.Rows {
		if scan.Output.Rows[index].ID == row.ID {
			return scan.Output.Rows[index], nil
		}
	}
	return row, nil
}

// isResumeRowKind reports whether kind is one of the resumable-only kinds
// resolveSelectedRow re-resolves: the kinds scanFleetCached's empty gather
// can paint for a chat that is actually live right now.
func isResumeRowKind(kind compose.Kind) bool {
	switch kind {
	case compose.ResumeClaude, compose.ResumeCodex, compose.ResumeOpenCode:
		return true
	default:
		return false
	}
}

func limitAccounts(runtime pfmconfig.Runtime) []pfmstats.LimitAccount {
	accounts := make(
		[]pfmstats.LimitAccount,
		0,
		len(
			runtime.Config.Accounts,
		)+len(
			runtime.Config.AccountSkips,
		)+len(
			runtime.Config.CodexAccounts,
		)+len(
			runtime.Config.OpenCodeAccounts,
		)+3,
	)
	if len(runtime.Config.Accounts) == 0 {
		accounts = append(accounts, pfmstats.LimitAccount{
			Engine: pfmengine.Claude,
			Label:  "no " + pfmengine.MustLookup(pfmengine.Claude).Short + " accounts configured",
			Absent: true,
		})
	}
	for _, account := range runtime.Config.Accounts {
		claude := runtime.Config.EffectiveClaude(account.ID)
		accounts = append(accounts, pfmstats.LimitAccount{
			ID:           account.ID,
			Emoji:        runtime.Config.EmojiFor(account.ID),
			Engine:       pfmengine.Claude,
			Label:        pfmconfig.DisplayAccountDir(runtime.Paths.Home, account.ID, account.ConfigDir),
			ConfigDir:    account.ConfigDir,
			ClaudeBinary: claude.Binary,
		})
	}
	for _, skip := range runtime.Config.AccountSkips {
		accounts = append(accounts, pfmstats.LimitAccount{
			ID: skip.ID, Engine: pfmengine.Claude,
			Label:     pfmconfig.DisplayAccountDir(runtime.Paths.Home, skip.ID, skip.ConfigDir),
			ConfigDir: skip.ConfigDir, SkipReason: skip.Reason,
		})
	}
	if len(runtime.Config.CodexAccounts) == 0 {
		accounts = append(accounts, pfmstats.LimitAccount{
			Engine: pfmengine.Codex,
			Label:  "no " + pfmengine.MustLookup(pfmengine.Codex).Short + " accounts configured",
			Absent: true,
		})
	}
	for _, account := range runtime.Config.CodexAccounts {
		accounts = append(accounts, pfmstats.LimitAccount{
			ID:            account.ID,
			Emoji:         runtime.Config.CodexEmojiFor(account.ID),
			Engine:        pfmengine.Codex,
			Label:         fmt.Sprintf("%s %d", pfmengine.MustLookup(pfmengine.Codex).Short, account.ID),
			CodexBinary:   runtime.Config.Codex.Binary,
			CodexHome:     account.Home,
			CodexAuthPath: filepath.Join(account.Home, "auth.json"),
		})
	}
	if len(runtime.Config.OpenCodeAccounts) == 0 {
		accounts = append(accounts, pfmstats.LimitAccount{
			Engine: pfmengine.OpenCode,
			Label:  "no " + pfmengine.MustLookup(pfmengine.OpenCode).Short + " accounts configured",
			Absent: true,
		})
	}
	for _, account := range runtime.Config.OpenCodeAccounts {
		accounts = append(accounts, pfmstats.LimitAccount{
			ID: account.ID, Engine: pfmengine.OpenCode,
			Label: fmt.Sprintf("%s %d", pfmengine.MustLookup(pfmengine.OpenCode).Short, account.ID),
		})
	}
	return accounts
}

func boolCount(values ...bool) int {
	count := 0
	for _, value := range values {
		if value {
			count++
		}
	}
	return count
}

// reportKills is the receipt for hidden-state writes made while the picker was
// open. Hiding a resumable-only chat changes list visibility only; hiding a
// LIVE chat also ends it, through the same exit choreography `pfm chat kill`
// runs — the receipt still just says "hidden" either way, since visibility
// is the one outcome every ⌃X guarantees.
func reportKills(changes []ui.KillChange, stderr io.Writer) {
	hidden := 0
	for _, change := range changes {
		if !change.Killed {
			continue
		}
		hidden++
	}
	if hidden > 0 {
		fmt.Fprintf(stderr, "pfm ls: hidden %d\n", hidden)
	}
}

// killApplier performs a picker ⌃X hidden-state write the instant it is typed.
// Hiding a resumable-only row changes list visibility only; hiding a row
// carrying a live tmux address also ends it — kill.Manager runs the exit
// choreography itself once it sees the address, so this applier only forwards
// the row's own Socket/PaneID and never decides live-ness on its own.
//
// It reports failure by returning it, never by writing to stderr: Bubble Tea
// owns the terminal for as long as the picker is open.
func killApplier(
	ctx context.Context,
	database *store.Store,
	runtime pfmconfig.Runtime,
) (func(ui.KillChange) error, error) {
	manager, err := kill.New(database, fleet.KillDependencies(runtime))
	if err != nil {
		return nil, err
	}
	return func(change ui.KillChange) error {
		if !change.Killed {
			return manager.Unkill(ctx, change.ID)
		}
		request := kill.Request{
			ID: change.ID,
			// The picker was showing the row, so it vouches for the engine.
			Engine: change.Engine,
		}
		if change.Socket != "" && change.PaneID != "" {
			request.SocketName = change.Socket
			request.PaneID = change.PaneID
		}
		if _, err := manager.Kill(ctx, request); err != nil {
			return err
		}
		return nil
	}, nil
}

func deactivateApplier(
	ctx context.Context,
	resolved paths.Values,
) func(compose.Row) error {
	return func(row compose.Row) error {
		if row.Socket == "" {
			return errors.New("selected chat has no live server")
		}
		return pfmchat.KillServer(ctx, resolved, row.Socket)
	}
}

func rebootRow(
	ctx context.Context,
	resolved paths.Values,
	row compose.Row,
	stderr io.Writer,
) (compose.Row, error) {
	if row.Kind == compose.LiveSplit || row.ID == "" {
		return compose.Row{}, errors.New("a split live row cannot be rebooted as one chat")
	}
	if err := pfmchat.KillServer(ctx, resolved, row.Socket); err != nil {
		fmt.Fprintf(stderr, "pfm: reboot kill-server %s: %v\n", row.Socket, err)
	}
	if row.Kind == compose.LiveCodex {
		row.Kind = compose.ResumeCodex
	} else {
		row.Kind = compose.ResumeClaude
	}
	row.Socket = ""
	row.PaneID = ""
	row.SessionName = ""
	row.WindowName = ""
	fmt.Fprintf(stderr, "pfm: rebooting %s in a fresh server\n", row.ID)
	return row, nil
}

func listKilled(args []string, stdout, stderr io.Writer, runtime pfmconfig.Runtime) (exitCode int) {
	flags := cli.NewFlagSet("ls --killed", "usage: pfm ls --killed", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	database, manager, code := fleet.OpenKillManager(stderr, runtime)
	if code != 0 {
		return code
	}
	defer func() { cli.CloseResource(database, "pfm ls --killed: close database", stderr, &exitCode) }()
	rows, err := manager.Killed(context.Background())
	if err != nil {
		fmt.Fprintf(stderr, "pfm ls --killed: %v\n", err)
		return 1
	}
	for _, row := range rows {
		fmt.Fprintf(stdout, "%s\t%s\t%d\n", row.ID, row.Engine, row.KilledAt)
	}
	return 0
}
