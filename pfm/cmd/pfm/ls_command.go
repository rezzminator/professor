package main

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/fleetdb"
	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/heal"
	fleetindex "hostops/pfm/internal/index"
	"hostops/pfm/internal/kill"
	"hostops/pfm/internal/paths"
	pfmstats "hostops/pfm/internal/stats"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/ui"
)

func runLS(
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) (exitCode int) {
	flags := newFlagSet(
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
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() > 1 ||
		boolCount(*plain, *tsv) > 1 {
		flags.Usage()
		return 2
	}
	if *safe != "auto" && *safe != "on" && *safe != toggleOffFlag {
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
		return runKilled(nil, stdout, stderr, runtime)
	}
	if flags.NArg() == 1 {
		if all || *plain || *tsv {
			flags.Usage()
			return 2
		}
		return openID(context.Background(), flags.Arg(0), stdout, stderr, runtime)
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
	defer func() { closeCommandResource(database, "pfm ls: close database", stderr, &exitCode) }()
	sharedState := fleetdb.Open(ctx, runtime.Paths)
	defer func() {
		if err := sharedState.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm ls: close shared state: %v\n", err)
		}
	}()

	request := scanRequest{
		View: view,
		// Fleet-wide picker: no chat chosen yet, so no per-account override applies.
		Cache1H: initialCache1H(runtime.Config, 0),
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
		activity := ui.NewActivityClock(time.Now())
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
		if err := killChatServer(ctx, scan.Paths, outcome.Row.Socket); err != nil {
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
		return openRow(ctx, row, outcome.PrimaryAccount, cache1H, stdout, stderr, runtime)
	case ui.OutcomeSelected:
		return openRow(
			ctx,
			outcome.Row,
			outcome.PrimaryAccount,
			cache1H,
			stdout,
			stderr,
			runtime,
		)
	case ui.OutcomeCancelled, ui.OutcomeNone:
		return 0
	default:
		fmt.Fprintf(stderr, "pfm ls: unsupported picker outcome %d\n", outcome.Kind)
		return 1
	}
}

func limitAccounts(runtime commandRuntime) []pfmstats.LimitAccount {
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

func closeCommandResource(closer io.Closer, label string, stderr io.Writer, exitCode *int) {
	if err := closer.Close(); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", label, err)
		if *exitCode == 0 {
			*exitCode = 1
		}
	}
}

func openID(
	ctx context.Context,
	id string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) (exitCode int) {
	database, err := store.Open(store.WithWarningWriter(stderr))
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	defer func() { closeCommandResource(database, "pfm chat open: close database", stderr, &exitCode) }()
	// READ-ONLY: open needs to FIND one row, never to persist a gather pass.
	// A writing scan here wedges whenever the caller already holds the fleet
	// store open in the same process — which is exactly what made chat_open
	// hang forever when it was served by the `pfm mcp serve` daemon while the
	// identical call over a one-shot CLI returned in seconds.
	scan, err := scanFleet(
		ctx,
		database,
		scanRequest{View: compose.AllView, ReadOnly: true, Runtime: &runtime},
		stderr,
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	for index := range scan.Output.Rows {
		row := &scan.Output.Rows[index]
		if row.ID == id {
			primary := fleet.PrimaryAccount(scan.Paths, runtime.Config)
			return openRow(
				ctx,
				*row,
				runtime.Config.PrimaryAccountFor(compose.EngineForKind(row.Kind), primary),
				initialCache1H(runtime.Config, primary),
				stdout,
				stderr,
				runtime,
			)
		}
	}
	fmt.Fprintf(stderr, "pfm chat open: chat %q is not indexed\n", id)
	return 1
}

func openRow(
	ctx context.Context,
	row compose.Row,
	primary int,
	cache1H bool,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) int {
	return openRowWithPrompt(ctx, row, primary, cache1H, "", stdout, stderr, runtime)
}

func openRowWithPrompt(
	ctx context.Context,
	row compose.Row,
	primary int,
	cache1H bool,
	prompt string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) int {
	resolved := runtime.Paths
	if row.Kind != compose.LiveClaude &&
		row.Kind != compose.LiveCodex &&
		row.Kind != compose.LiveSplit {
		if info, statErr := os.Stat(row.CWD); statErr != nil || !info.IsDir() {
			if currentDir, cwdErr := os.Getwd(); cwdErr == nil {
				row.CWD = currentDir
			}
		}
	}
	// The Codex projection repair rides the resume path itself: a wedged
	// thread is repaired in the same breath that opens it, with no shell
	// helper in the run string to be missing, unexecutable, or stale.
	healCodexRoot := resolved.FirstRoot(pfmengine.Codex)
	if account, found := runtime.Config.CodexAccountByID(primary); found {
		healCodexRoot = account.Home
	}
	executor, err := action.New(action.Dependencies{
		Stderr: stderr,
		Heal: func(ctx context.Context, threadID string) string {
			return heal.Thread(ctx, healCodexRoot, threadID)
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	fresh, err := freshSocket(row.Kind)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	line, err := executor.Open(ctx, action.Request{
		Row:            row,
		Prompt:         prompt,
		PrimaryAccount: primary,
		Cache1H:        cache1H,
		Bunker:         inBunker(),
		Home:           resolved.Home,
		FreshSocket:    fresh,
		CurrentTMUX:    os.Getenv("TMUX"),
		Config:         runtime.Config,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	if line != "" {
		if err := dispatchAction(stdout, line); err != nil {
			fmt.Fprintf(stderr, "pfm chat open: execute action: %v\n", err)
			return 1
		}
	}
	return 0
}

func freshSocket(kind compose.Kind) (string, error) {
	id, err := compose.EngineForKindChecked(kind)
	if err != nil {
		return "", err
	}
	return freshEngineSocket(id), nil
}

func freshEngineSocket(id pfmengine.ID) string {
	if value := os.Getenv(testFreshSocketEnv); value != "" {
		return value
	}
	descriptor := pfmengine.MustLookup(id)
	var randomBytes [2]byte
	_, _ = rand.Read(randomBytes[:])
	return fmt.Sprintf(
		"%s%d-%d-%d",
		descriptor.SocketPrefix,
		time.Now().Unix(),
		os.Getpid(),
		binary.BigEndian.Uint16(randomBytes[:]),
	)
}

// initialCache1H resolves the prompt-cache TTL for a freshly launched chat.
// Config sets the default; CC_ARM_1H or ENABLE_PROMPT_CACHING_1H, when
// PRESENT in the environment at all (any value, not just "1"), is an
// explicit override that wins over config. CC_ARM_1H is accepted from
// existing automation; native actions carry the chosen TTL in ClaudeSpawn.
//
// account is the Claude account the chat will be born under, or 0 when the
// caller has no account context yet (the fleet-wide picker default).
// EffectiveClaude resolves an unknown account to the top-level posture, so
// 0 is the honest way to say "no per-account override applies here" — the
// accounts[N].claude.cache1h key is otherwise decoded and marshalled but
// never read, a knob that reports "set" while changing nothing.
func initialCache1H(config pfmconfig.Config, account int) bool {
	if value, ok := os.LookupEnv("CC_ARM_1H"); ok {
		return value == "1"
	}
	if value, ok := os.LookupEnv("ENABLE_PROMPT_CACHING_1H"); ok {
		return value == "1" && os.Getenv("CLAUDECODE") == ""
	}
	return config.EffectiveClaude(account).Cache1H
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
	runtime commandRuntime,
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

// killChatServer ends one chat's tmux server and removes every handle that
// would otherwise keep pointing at it — the socket file, and the sid crumbs
// that resolve a chat to its server. It is the single termination sequence:
// ⌃O reboots a chat through it and deactive puts one to sleep through it.
func killChatServer(
	ctx context.Context,
	resolved paths.Values,
	socket string,
) error {
	tmux := action.CommandTmux{TmuxDir: resolved.TmuxDir}
	if err := tmux.KillServer(ctx, socket); err != nil {
		// Killing a corpse fails loudly for no reason — a socket file outlives
		// its server. The goal is "not running", so ask whether it is rather
		// than reporting a failure the user cannot act on.
		if tmux.SocketAlive(ctx, socket) {
			return err
		}
	}
	_ = os.Remove(filepath.Join(resolved.TmuxDir, socket))
	entries, _ := os.ReadDir(resolved.SIDDir)
	for _, entry := range entries {
		if name, _, ok := gather.ParseCrumbName(entry.Name()); ok &&
			name == socket {
			_ = os.Remove(filepath.Join(resolved.SIDDir, entry.Name()))
		}
	}
	state := fleetdb.Open(ctx, resolved)
	clearErr := state.ClearBranchSeat(ctx, socket)
	closeErr := state.Close()
	if clearErr != nil || closeErr != nil {
		return fmt.Errorf(
			"clear branch marker after ending %s: %w",
			socket,
			errors.Join(clearErr, closeErr),
		)
	}
	return nil
}

func deactivateApplier(
	ctx context.Context,
	resolved paths.Values,
) func(compose.Row) error {
	return func(row compose.Row) error {
		if row.Socket == "" {
			return errors.New("selected chat has no live server")
		}
		return killChatServer(ctx, resolved, row.Socket)
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
	if err := killChatServer(ctx, resolved, row.Socket); err != nil {
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

func runIndex(args []string, stdout, stderr io.Writer, runtime commandRuntime) (exitCode int) {
	flags := newFlagSet(
		indexCommand,
		"usage: pfm index [--full] [--progress]",
		stderr,
	)
	full := flags.Bool("full", false, "reparse every indexed file")
	progress := flags.Bool("progress", false, "report start and elapsed time to stderr")
	if code, ok := parseFlags(flags, args); !ok {
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
	defer func() { closeCommandResource(database, "pfm index: close database", stderr, &exitCode) }()
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

// pruneOrphanedKills reports, and only with confirm deletes, the kills doctor
// counts as orphaned_killed. A kill cannot be recovered once deleted, so the
// dry run is the default and the count is always printed.
func pruneOrphanedKills(
	ctx context.Context,
	database *store.Store,
	confirm bool,
	stdout, stderr io.Writer,
) int {
	orphans, err := database.OrphanedKills(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "pfm archive: %v\n", err)
		return 1
	}
	if !confirm {
		for _, orphan := range orphans {
			fmt.Fprintf(
				stdout,
				"would prune\t%s\t%s\t%d\n",
				orphan.ID,
				orphan.Engine,
				orphan.KilledAt,
			)
		}
		fmt.Fprintf(
			stdout,
			"pfm archive: %d orphaned kill(s); re-run with --yes to delete\n",
			len(orphans),
		)
		return 0
	}
	deleted, err := database.DeleteOrphanedKills(ctx)
	if err != nil {
		fmt.Fprintf(stderr, "pfm archive: %v\n", err)
		return 1
	}
	for _, orphan := range orphans {
		fmt.Fprintf(
			stdout,
			"pruned\t%s\t%s\t%d\n",
			orphan.ID,
			orphan.Engine,
			orphan.KilledAt,
		)
	}
	fmt.Fprintf(
		stdout,
		"pfm archive: pruned %d orphaned kill(s)\n",
		deleted,
	)
	return 0
}
