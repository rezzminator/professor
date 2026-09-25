package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/rezzminator/professor/pfm/internal/action"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gitroot"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

const branchAction = "branch"

func runChatFind(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet("chat find", "usage: pfm chat find <excerpt-file>", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	match, alternatives, err := findTranscript(flags.Arg(0), runtimes...)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat find: %v\n", err)
		return 2
	}
	fmt.Fprintf(stderr, "Matched session: %s (%d/%d needles hit)\n", match.ID, match.Hits, match.Needles)
	if match.First != "" || match.Last != "" {
		fmt.Fprintf(stderr, "Range: %s -> %s\n", match.First, match.Last)
	}
	if len(alternatives) > 0 {
		fmt.Fprintln(stderr, "Other candidates (hits, file):")
		for _, candidate := range alternatives {
			fmt.Fprintf(stderr, "  %d %s\n", candidate.Hits, candidate.Path)
		}
	}
	fmt.Fprintf(stdout, "%s\t%s\n", match.ID, match.Path)
	return 0
}

// findTranscript is chat.Find over an excerpt file, in the CLI's display
// contract: the best match and at most four runners-up.
func findTranscript(
	excerptPath string,
	runtimes ...commandRuntime,
) (pfmchat.TranscriptMatch, []pfmchat.TranscriptMatch, error) {
	content, err := os.ReadFile(excerptPath)
	if err != nil {
		return pfmchat.TranscriptMatch{}, nil, err
	}
	matches, err := pfmchat.Find(
		context.Background(),
		firstRuntime(runtimes),
		pfmchat.FindRequest{Excerpt: string(content), Self: pfmchat.AskingSession()},
	)
	if err != nil {
		return pfmchat.TranscriptMatch{}, nil, err
	}
	alternatives := matches[1:]
	if len(alternatives) > 4 {
		alternatives = alternatives[:4]
	}
	return matches[0], alternatives, nil
}

func runChatReadExcerpt(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	if len(args) < 1 || len(args) > 2 {
		fmt.Fprintln(stderr, "usage: pfm chat read <excerpt-file> [last-N-lines]")
		return 2
	}
	limit := 0
	if len(args) == 2 {
		value, err := strconv.Atoi(args[1])
		if err != nil || value < 1 {
			fmt.Fprintln(stderr, "usage: pfm chat read <excerpt-file> [last-N-lines]")
			return 2
		}
		limit = value
	}
	match, _, err := findTranscript(args[0], runtimes...)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat read: %v\n", err)
		return 2
	}
	entries, err := transcript.All(context.Background(), match.Path, string(pfmengine.Claude))
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat read: %v\n", err)
		return 1
	}
	body := transcript.Markdown(entries)
	if limit > 0 {
		body = transcript.LastLines(body, limit)
	}
	sidDir := ""
	if len(runtimes) > 0 {
		sidDir = runtimes[0].Paths.SIDDir
	}
	if sidDir == "" {
		resolved, err := paths.Resolve()
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat read: resolve output directory: %v\n", err)
			return 1
		}
		sidDir = resolved.SIDDir
	}
	chatLoadsDir, err := filepath.Abs(filepath.Join(sidDir, paths.SIDScratchChatLoads))
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat read: resolve output directory: %v\n", err)
		return 1
	}
	if err := os.MkdirAll(chatLoadsDir, 0o700); err != nil {
		fmt.Fprintf(stderr, "pfm chat read: create output directory: %v\n", err)
		return 1
	}
	out := filepath.Join(chatLoadsDir, match.ID+".md")
	var document strings.Builder
	fmt.Fprintf(
		&document,
		"# Loaded chat — session %s\n\nSource: %s\nRange: %s -> %s\nVisible chat text only — thinking and tool outputs are not recorded here.\n",
		match.ID,
		match.Path,
		match.First,
		match.Last,
	)
	if limit > 0 {
		fmt.Fprintf(&document, "(last %d lines)\n", limit)
	}
	document.WriteString("\n")
	document.WriteString(body)
	if err := os.WriteFile(out, []byte(document.String()), 0o600); err != nil {
		fmt.Fprintf(stderr, "pfm chat read: write %s: %v\n", out, err)
		return 1
	}
	fmt.Fprintf(stdout, "Extracted -> %s (%d lines)\n", out, strings.Count(document.String(), "\n"))
	return 0
}

func runChatSaveContext(
	ctx context.Context, args []string, stdout, stderr io.Writer, env paths.Env, runtimes ...commandRuntime,
) (exitCode int) {
	env = defaultEnv(env)
	fail := func(format string, args ...any) int {
		fmt.Fprintf(stderr, "pfm chat save: "+format+"\n", args...)
		return 1
	}
	if len(args) < 1 || len(args) > 2 {
		fmt.Fprintln(stderr, "usage: pfm chat save <target-file> [transcript-jsonl]")
		return 2
	}
	target, transcriptPath := args[0], ""
	if len(args) == 2 {
		transcriptPath = args[1]
	} else {
		id := env.Get("CLAUDE_CODE_SESSION_ID")
		if id == "" {
			return fail("CLAUDE_CODE_SESSION_ID is not set and no transcript path was given")
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fail("current directory: %v", err)
		}
		transcriptPath = currentClaudeTranscriptPath(id, cwd, env, runtimes...)
		if transcriptPath == "" {
			return fail("could not resolve the current Claude transcript")
		}
	}
	entries, err := transcriptEntriesForSave(ctx, transcriptPath)
	if err != nil {
		return fail("%v", err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil && filepath.Dir(target) != "." {
		return fail("create target directory: %v", err)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fail("open target: %v", err)
	}
	closed, users := false, 0
	defer func() {
		if !closed {
			cli.CloseResource(file, "pfm chat save: close target", stderr, &exitCode)
		}
	}()
	if _, err := fmt.Fprintf(
		file,
		"\n---\n\n# FULL TRANSCRIPT (script-dumped, verbatim)\n\nVisible chat text only — thinking and tool outputs are not recorded here.\nSource: %s\n\n%s---\n\n# ENVIRONMENT SNAPSHOT (script-dumped)\n\n",
		transcriptPath,
		transcript.Markdown(entries),
	); err != nil {
		return fail("write transcript: %v", err)
	}
	writeRepositorySnapshot(ctx, file, obs.Runner(deps.RealRunner{}))
	closed = true
	if err := file.Close(); err != nil {
		return fail("close target: %v", err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return fail("stat target: %v", err)
	}
	for _, entry := range entries {
		if entry.Role == transcript.RoleUser {
			users++
		}
	}
	fmt.Fprintf(
		stdout,
		"Appended transcript (%d user records) + env snapshot -> %s (%d bytes total)\n",
		users,
		target,
		info.Size(),
	)
	return 0
}

// runChatLS is `pfm chat ls`: chat.List's live, unkilled chats in the current
// directory's repository, or with --all in every repository. MCP chat_ls's
// `all` differs: it adds killed and background rows as well.
func runChatLS(args []string, stdout, stderr io.Writer, clk clock.Clock, runtimes ...commandRuntime) int {
	clk = defaultClock(clk)
	all := false
	for _, arg := range args {
		switch arg {
		case "--all", "-a", "all":
			all = true
		default:
			fmt.Fprintln(
				stderr,
				"usage: pfm chat ls [--all]  (--all: live chats in every repo; MCP chat_ls all also adds killed rows)",
			)
			return 2
		}
	}
	request := pfmchat.ListRequest{View: compose.AllView, LiveOnly: true, ReadOnly: true}
	if !all {
		directory, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat ls: read the current directory: %v\n", err)
			return 1
		}
		request.Repo = gitroot.RepoRoot(directory)
	}
	listed, err := pfmchat.List(context.Background(), firstRuntime(runtimes), request, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat ls: %v\n", err)
		return 1
	}
	if all {
		fmt.Fprintln(stdout, "live chats everywhere (name · session · state · dir · last activity):")
	} else {
		fmt.Fprintln(stdout, "live chats in this repo (name · session · state · last activity):")
	}
	for index := range listed.Rows {
		row := &listed.Rows[index]
		chat := pfmchat.FromRow(*row)
		status, inspectErr := headless.Inspect(context.Background(), chat, clk.Now())
		state := doctor.StateUnknown
		if inspectErr != nil {
			fmt.Fprintf(stderr, "pfm chat ls: inspect %s: %v\n", chat.Name, inspectErr)
		} else {
			state = status.State
		}
		// Name is the actionable identity; retain the raw tmux session second
		// so parsers lose no information.
		handle := row.Socket
		if row.SessionName != "" {
			handle = row.SessionName
		}
		location := ""
		if all {
			location = strings.Replace(row.CWD, listed.Home, "~", 1) + "  "
		}
		fmt.Fprintf(
			stdout, "  %-28s %-24s %-7s %s%s\n",
			transcript.Truncate(row.Name, 28), handle, state, location, transcript.Truncate(row.LastPrompt, 64),
		)
	}
	if len(listed.Rows) == 0 {
		fmt.Fprintln(stdout, "  (none)")
	}
	if listed.Elsewhere > 0 {
		fmt.Fprintf(stdout, "  (+%d live in other dirs — pfm chat ls --all to see them)\n", listed.Elsewhere)
	}
	if listed.KilledCount > 0 {
		fmt.Fprintf(stdout, "  (+%d killed — pfm ls --killed to manage)\n", listed.KilledCount)
	}
	return 0
}

func runChatBranch(
	args []string,
	stdout, stderr io.Writer,
	env paths.Env,
	clk clock.Clock,
	runtimes ...commandRuntime,
) int {
	env = defaultEnv(env)
	clk = defaultClock(clk)
	flags := cli.NewFlagSet(
		"chat branch",
		"usage: pfm chat branch [--engine claude|codex] [--session-id ID] [--cwd DIR] [--account N] [--name NAME] [name]",
		stderr,
	)
	requestedEngine := flags.String("engine", "", "engine of the session to fork")
	id := flags.String("session-id", "", "session id to fork")
	requestedCWD := flags.String("cwd", "", "project directory for the detached fork")
	account := flags.Int("account", 0, "configured engine account")
	requestedName := flags.String("name", "", "detached fork name")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if *requestedName != "" && flags.NArg() != 0 {
		flags.Usage()
		return 2
	}
	runtime, err := pfmconfig.OptionalRuntime(runtimes)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat branch: load config: %v\n", err)
		return 1
	}
	engineInput := strings.TrimSpace(*requestedEngine)
	if engineInput == "" {
		caller, ok := callerEngine(env.Get)
		if !ok {
			fmt.Fprintln(stderr, "pfm chat branch: no ambient session id; pass --engine and --session-id")
			return 1
		}
		engineInput = string(caller)
	}
	engine, parseErr := pfmengine.Parse(engineInput)
	if parseErr != nil || (engine != pfmengine.Claude && engine != pfmengine.Codex) {
		fmt.Fprintf(stderr, "pfm chat branch: engine must be claude or codex: %v\n", parseErr)
		return 2
	}
	if *id == "" {
		if engine == pfmengine.Claude {
			*id = env.Get(resolve.ClaudeSessionEnv)
		} else {
			*id = env.Get(resolve.CodexThreadEnv)
		}
	}
	if strings.TrimSpace(*id) == "" || strings.ContainsAny(*id, "\r\n\x00") {
		fmt.Fprintln(stderr, "pfm chat branch: --session-id is required and must be one safe line")
		return 2
	}
	// A fork inherits its parent's account and cache posture, not the caller's.
	parent, parentFound, err := parentBranchRow(context.Background(), *id, runtimes...)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat branch: resolve parent session: %v\n", err)
		return 1
	}
	requestedAccount := *account
	if requestedAccount == 0 && parentFound && parent.Account != 0 {
		requestedAccount = parent.Account
	}
	primary, primaryErr := fleet.PrimaryAccount(runtime.Paths, runtime.Config)
	if primaryErr != nil {
		fmt.Fprintf(stderr, "pfm chat branch: read primary account: %v\n", primaryErr)
		return 1
	}
	engine, selectedAccount, err := resolveRunEngineIDAccount(engine, requestedAccount, runtime.Config, primary)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat branch: %v\n", err)
		return 1
	}
	if *account == 0 && (!parentFound || parent.Account == 0) {
		fmt.Fprintf(
			stderr,
			"pfm chat branch: parent account for session %s could not be resolved; forking on primary account %d\n",
			transcript.Truncate(*id, 8), selectedAccount,
		)
	}
	binary := runtime.Config.Claude.Binary
	if engine == pfmengine.Codex {
		binary = runtime.Config.EffectiveCodex(selectedAccount).Binary
	}
	if _, err := obs.Runner(deps.RealRunner{}).LookPath(binary); err != nil {
		fmt.Fprintf(stderr, "pfm chat branch: configured %s binary %q is not executable: %v\n", engine, binary, err)
		return 1
	}
	if _, err := obs.Runner(deps.RealRunner{}).LookPath(pfmtmux.Binary); err != nil {
		fmt.Fprintln(stderr, "pfm chat branch: tmux is not on PATH")
		return 1
	}
	resolved := runtime.Paths
	cwd := strings.TrimSpace(*requestedCWD)
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat branch: current directory: %v\n", err)
			return 1
		}
	}
	nameInput := *requestedName
	if flags.NArg() != 0 {
		nameInput = strings.Join(flags.Args(), " ")
	}
	name := sanitizeBranchName(nameInput)
	if strings.TrimSpace(name) == "" {
		name = defaultBranchName(*id)
	}
	model := ""
	if engine == pfmengine.Claude {
		model = currentClaudeModel(*id, env, runtime)
	}
	plan, err := action.HeadlessFork(action.HeadlessForkRequest{
		Engine: engine, SessionID: *id, Name: name, CWD: cwd,
		Home: runtime.Paths.Home, PrimaryAccount: selectedAccount,
		Cache1H: engine == pfmengine.Claude && forkCache1H(parent, parentFound, runtime.Config, selectedAccount),
		Model:   model, Config: runtime.Config,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat branch: plan fork: %v\n", err)
		return 1
	}
	socket := spawn.FreshSocket(engine)
	if override := env.Get(spawn.TestFreshSocketEnv); override != "" {
		socket = override
	}
	titles := runtime.Config.Tmux.Titles
	tmux := spawn.TmuxSpawner{TmuxDir: resolved.TmuxDir, Titles: &titles}
	var branchWarnings []string
	if engine == pfmengine.Codex {
		spawned, spawnErr := spawn.Run(context.Background(), tmux, spawn.Request{
			Engine: engine, Name: name, Socket: socket, CWD: cwd, Run: plan.Run,
			Binary:              plan.Binary,
			PromptOnCommandLine: plan.PromptOnCommandLine,
			Width:               action.HeadlessWidth, Height: action.HeadlessHeight,
		})
		err = spawnErr
		branchWarnings = append(branchWarnings, spawned.Warnings...)
	} else {
		err = tmux.NewSession(context.Background(), spawn.SessionSpec{
			Socket: socket, Session: socket, Window: spawn.WindowName(name),
			CWD: cwd, Run: plan.Run, Binary: plan.Binary,
			Width: action.HeadlessWidth, Height: action.HeadlessHeight,
		})
	}
	if err != nil {
		rollbackErr := pfmchat.KillServer(context.Background(), resolved, socket)
		if rollbackErr != nil {
			fmt.Fprintf(stderr, "pfm chat branch: create detached seat: %v; rollback: %v\n", err, rollbackErr)
		} else {
			fmt.Fprintf(stderr, "pfm chat branch: create detached seat: %v\n", err)
		}
		return 1
	}
	// If Codex declines its startup rename, retry through guarded /rename.
	if engine == pfmengine.Codex && len(branchWarnings) != 0 {
		var renameStderr bytes.Buffer
		deliver := func(ctx context.Context, chat headless.Chat, name string) (int, string, error) {
			return deliverChatNameWithRuntime(ctx, chat, name, runtime)
		}
		renameCode := applyChatName(context.Background(), headless.Chat{
			Name: name, Engine: engine, CWD: cwd, Socket: socket, Session: socket, Live: true,
		}, name, deliver, &renameStderr)
		if renameCode == 0 {
			branchWarnings = nil
		} else {
			branchWarnings = append(branchWarnings,
				fmt.Sprintf("guarded /rename retry rc=%d: %s", renameCode, strings.TrimSpace(renameStderr.String())),
			)
		}
	}
	state := fleetdb.OpenSharedState(context.Background(), resolved)
	recordErr := state.RecordBranchSeat(context.Background(), socket, *id, clk.Now().Unix())
	closeErr := state.Close()
	if recordErr != nil || closeErr != nil {
		rollbackErr := pfmchat.KillServer(context.Background(), resolved, socket)
		failure := errors.Join(recordErr, closeErr)
		if rollbackErr != nil {
			failure = errors.Join(failure, fmt.Errorf("rollback detached seat: %w", rollbackErr))
		}
		fmt.Fprintf(stderr, "pfm chat branch: record detached seat: %v\n", failure)
		return 1
	}
	fmt.Fprintf(stdout, "Branched %s…", transcript.Truncate(*id, 8))
	if len(branchWarnings) == 0 {
		fmt.Fprintf(stdout, " as %q", name)
	} else {
		fmt.Fprintf(stdout, " with requested name %q still unconfirmed", name)
	}
	if model != "" {
		fmt.Fprintf(stdout, " on %s", model)
	}
	fmt.Fprintf(
		stdout,
		" into detached socket %s — waiting in pfm ls; open later with pfm chat open %q.\n",
		socket,
		name,
	)
	if len(branchWarnings) != 0 {
		for _, warning := range branchWarnings {
			fmt.Fprintf(stdout, "warning: %s\n", warning)
		}
	}
	return 0
}

// parentBranchRow distinguishes a cleanly absent parent from a failed scan.
func parentBranchRow(ctx context.Context, id string, runtimes ...commandRuntime) (compose.Row, bool, error) {
	rows, err := pfmchat.Rows(ctx, io.Discard, firstRuntime(runtimes))
	if err != nil {
		return compose.Row{}, false, err
	}
	for index := range rows {
		row := &rows[index]
		if row.ID == id {
			return *row, true, nil
		}
	}
	return compose.Row{}, false, nil
}

// forkCache1H inherits a live parent's observed TTL; otherwise it uses the
// parent's account default because a dead row's false C1H is only "unobserved".
func forkCache1H(parent compose.Row, parentFound bool, config pfmconfig.Config, account int) bool {
	if parentFound && parent.Kind.IsAddressable() {
		return parent.C1H
	}
	return config.EffectiveClaude(account).Cache1H
}

func sanitizeBranchName(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(" ._-", r) {
			return r
		}
		return -1
	}, value)
}

func defaultBranchName(id string) string {
	parent := ""
	database, err := store.Open(store.WithWarningWriter(io.Discard))
	if err == nil {
		if indexed, found, queryErr := database.Transcript(context.Background(), id); queryErr == nil && found {
			parent = naming.DisplayName(indexed.CustomTitle, indexed.AITitle, indexed.FirstPrompt)
		}
		_ = database.Close()
	}
	if strings.TrimSpace(parent) == "" {
		parent = transcript.Truncate(id, 8)
	}
	name := strings.TrimSpace(sanitizeBranchName(parent + "-branch"))
	if name == "" {
		return branchAction
	}
	return name
}

func currentClaudeModel(id string, env paths.Env, runtimes ...commandRuntime) string {
	env = defaultEnv(env)
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	path := currentClaudeTranscriptPath(id, cwd, env, runtimes...)
	if path == "" {
		return ""
	}
	meta, err := transcript.ReadMeta(path, string(pfmengine.Claude))
	if err != nil {
		return ""
	}
	switch {
	case strings.Contains(meta.Model, "opus"):
		return "opus[1m]"
	case strings.Contains(meta.Model, "sonnet"):
		return "sonnet[1m]"
	case strings.Contains(meta.Model, "haiku"):
		return "haiku"
	case strings.Contains(meta.Model, "fable"):
		return "fable"
	default:
		return ""
	}
}

// currentClaudeTranscriptPath prefers explicit config, then loaded account roots.
func currentClaudeTranscriptPath(id, cwd string, env paths.Env, runtimes ...commandRuntime) string {
	env = defaultEnv(env)
	slug := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	if config := strings.TrimSpace(env.Get("CLAUDE_CONFIG_DIR")); config != "" {
		return filepath.Join(config, "projects", slug, id+".jsonl")
	}
	if len(runtimes) != 0 {
		roots := runtimes[0].Paths.Roots[pfmengine.Claude]
		for _, root := range roots {
			candidate := filepath.Join(root, slug, id+".jsonl")
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
		if len(roots) != 0 {
			return filepath.Join(roots[0], slug, id+".jsonl")
		}
		if runtimes[0].Paths.Home != "" {
			return filepath.Join(runtimes[0].Paths.Home, ".claude", "projects", slug, id+".jsonl")
		}
	}
	resolved, err := paths.Resolve()
	if err != nil {
		return ""
	}
	return filepath.Join(resolved.Home, ".claude", "projects", slug, id+".jsonl")
}

// historyMessage is one surviving user/assistant turn from a transcript tail,
// ready to print — the native port of history.sh's jq pipeline.
type historyMessage struct {
	timestamp, role, text string
}

// runChatHistory is the native port of the retired history.sh compatibility
// script: read a chat's on-disk transcript as deep as its tail carries, not
// bounded to a live pane's visible scrollback.
func runChatHistory(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet(
		"chat history",
		"usage: pfm chat history <sid-prefix|jsonl-path> [messages] [project-slug]",
		stderr,
	)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() < 1 || flags.NArg() > 3 {
		flags.Usage()
		return 2
	}
	sid := flags.Arg(0)
	count := 20
	if flags.NArg() >= 2 {
		parsed, err := strconv.Atoi(flags.Arg(1))
		if err != nil || parsed < 1 {
			fmt.Fprintf(stderr, "pfm chat history: messages must be a positive integer, got %q\n", flags.Arg(1))
			return 2
		}
		count = parsed
	}
	slug := flags.Arg(2)
	if slug == "" {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat history: current directory: %v\n", err)
			return 1
		}
		slug = strings.ReplaceAll(cwd, "/", "-")
	}
	path := sid
	if info, statErr := os.Stat(sid); statErr != nil || !info.Mode().IsRegular() {
		resolvedPath, err := resolveHistoryTranscript(sid, slug, runtimes...)
		if err != nil {
			fmt.Fprintln(stderr, err.Error())
			return 1
		}
		path = resolvedPath
	}
	messages, err := readHistoryMessages(path, count)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat history: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "== %s · last %d messages ==\n", path, count)
	for _, message := range messages {
		fmt.Fprintf(stdout, "\n───── %s · %s ─────\n%s\n", message.timestamp, message.role, message.text)
	}
	return 0
}

// resolveHistoryTranscript reproduces history.sh's pool search: each pool is
// tried in order, the newest-mtime `{pool}/{slug}/{sid}*.jsonl` match wins,
// and the first pool with any match short-circuits the rest.
func resolveHistoryTranscript(sid, slug string, runtimes ...commandRuntime) (string, error) {
	pools, err := historyPools(runtimes...)
	if err != nil {
		return "", err
	}
	for _, pool := range pools {
		match, err := newestHistoryMatch(pool, slug, sid)
		if err != nil {
			return "", err
		}
		if match != "" {
			return match, nil
		}
	}
	return "", fmt.Errorf("no transcript matching sid '%s' under %s in any account pool", sid, slug)
}

// historyPools mirrors runChatScript's former PFM_HISTORY_ROOTS_JSON
// marshalling exactly: a supplied runtime's configured Claude roots are used
// as-is (even an empty list, never widened), and only the ENTIRE absence of a
// runtime falls back to the unconfigured defaults history.sh assumed.
func historyPools(runtimes ...commandRuntime) ([]string, error) {
	if len(runtimes) != 0 {
		return runtimes[0].Paths.Roots[pfmengine.Claude], nil
	}
	resolved, err := paths.Resolve()
	if err != nil {
		return nil, err
	}
	pools := []string{filepath.Join(resolved.Home, ".claude", "projects")}
	matches, err := filepath.Glob(filepath.Join(resolved.Home, ".cc", "*", "projects"))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", filepath.Join(resolved.Home, ".cc", "*", "projects"), err)
	}
	sort.Strings(matches)
	return append(pools, matches...), nil
}

// newestHistoryMatch returns the newest-mtime file under pool/slug matching
// sid*.jsonl, or "" when the pool has none. A glob candidate that fails to
// stat for a reason other than having vanished between glob and stat is a
// real error, never silently read as absence.
func newestHistoryMatch(pool, slug, sid string) (string, error) {
	matches, err := filepath.Glob(filepath.Join(pool, slug, sid+"*.jsonl"))
	if err != nil {
		return "", fmt.Errorf("scan %s: %w", pool, err)
	}
	newest := ""
	var newestModTime time.Time
	for _, match := range matches {
		info, statErr := os.Stat(match)
		if errors.Is(statErr, fs.ErrNotExist) {
			continue
		}
		if statErr != nil {
			return "", fmt.Errorf("stat %s: %w", match, statErr)
		}
		if newest == "" || info.ModTime().After(newestModTime) {
			newest = match
			newestModTime = info.ModTime()
		}
	}
	return newest, nil
}

// readHistoryMessages is the native port of history.sh's jq pipeline: tail
// generously, drop the (possibly partial) first line, keep only user/
// assistant records with non-empty rendered text, drop synthetic reminder and
// caveat preambles, then take the last count survivors.
func readHistoryMessages(path string, count int) (messages []historyMessage, returnErr error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close %s: %w", path, err))
		}
	}()
	content, err := io.ReadAll(file)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	lines := tailLines(string(content), 800)
	if len(lines) > 0 {
		lines = lines[1:]
	}
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			// Match history.sh: a malformed transcript row is skipped, not fatal.
			continue
		}
		if record.Type != transcriptRoleUser && record.Type != "assistant" {
			continue
		}
		text := historyMessageText(record.Message.Content)
		if text == "" || strings.HasPrefix(text, "<system-reminder") ||
			strings.HasPrefix(text, "Caveat: The messages below") {
			continue
		}
		timestamp := record.Timestamp
		if timestamp == "" {
			timestamp = "?"
		}
		messages = append(messages, historyMessage{timestamp: timestamp, role: record.Type, text: text})
	}
	if len(messages) > count {
		messages = messages[len(messages)-count:]
	}
	return messages, nil
}

// historyMessageText extracts message.content the way history.sh's jq does:
// a string is itself; an array joins the .text of every type=="text" element
// with "\n"; anything else renders as "".
func historyMessageText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		texts := make([]string, 0, len(parts))
		for _, part := range parts {
			if part.Type == textFormat {
				texts = append(texts, part.Text)
			}
		}
		return strings.Join(texts, "\n")
	}
	return ""
}

// tailLines reproduces `tail -n count`: the last count newline-delimited
// lines, tolerant of a missing trailing newline and of fewer lines than
// count.
func tailLines(content string, count int) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if lines[len(lines)-1] == "" {
		// A file ending in a newline splits into one trailing empty element
		// that is the terminator, not a line.
		lines = lines[:len(lines)-1]
	}
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return lines
}

func runChatModal(args []string, stdout, stderr io.Writer, clk clock.Clock) int {
	clk = defaultClock(clk)
	if len(args) != 3 || args[1] != "deny" {
		fmt.Fprintln(stderr, "usage: pfm chat modal <tmux-session> deny <down-count>")
		return 2
	}
	count, err := strconv.Atoi(args[2])
	if err != nil || count < 0 {
		fmt.Fprintln(stderr, "usage: pfm chat modal <tmux-session> deny <down-count>")
		return 2
	}
	socketPath, err := chatSocketPath(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat modal: %v\n", err)
		return 1
	}
	if _, err := os.Stat(socketPath); err != nil {
		fmt.Fprintf(stderr, "pfm chat modal: no tmux socket for %q: %v\n", args[0], err)
		return 1
	}
	for index := 0; index < count; index++ {
		if output, err := pfmtmux.Exec(context.Background(), "", socketPath, "send-keys", "Down").
			CombinedOutput(); err != nil {
			fmt.Fprintf(stderr, "pfm chat modal: send Down: %v: %s\n", err, strings.TrimSpace(string(output)))
			return 1
		}
		if err := clk.Sleep(context.Background(), 200*time.Millisecond); err != nil {
			fmt.Fprintf(stderr, "pfm chat modal: %v\n", err)
			return 1
		}
	}
	if output, err := pfmtmux.Exec(context.Background(), "", socketPath, "send-keys", "Enter").
		CombinedOutput(); err != nil {
		fmt.Fprintf(stderr, "pfm chat modal: send Enter: %v: %s\n", err, strings.TrimSpace(string(output)))
		return 1
	}
	fmt.Fprintf(stdout, "modal denied on %s\n", args[0])
	return 0
}
