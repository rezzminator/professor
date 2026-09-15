package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	pfmchat "hostops/pfm/internal/chat"
	pfmconfig "hostops/pfm/internal/config"
	"hostops/pfm/internal/deps"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/headless"
	"hostops/pfm/internal/inject"
	"hostops/pfm/internal/transcript"
)

// Exit codes the headless family answers with. A consumer scripts against
// these, so they are a contract: 0 only ever means the chat was found and the
// verb did what it says.
const (
	codeUnknownChat = 4
	codeDeadChat    = 3
	// codeAwaitTimeout says the message was delivered and the chat is still
	// working — a different fact from every failure, and the one a caller
	// retries rather than escalates.
	codeAwaitTimeout = 5
	// codeUndelivered says nothing reached the model. The chat may be fine;
	// the message is not in it.
	codeUndelivered = 6
)

func runHeadless(
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) int {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		return runHeadlessExec([]string{"--help"}, os.Stdin, stdout, stderr, runtime)
	}
	if args[0] == "exec" {
		return runHeadlessExec(args[1:], os.Stdin, stdout, stderr, runtime)
	}
	if strings.HasPrefix(args[0], "-") {
		return runHeadlessExec(args, os.Stdin, stdout, stderr, runtime)
	}
	if len(args) > 0 {
		switch args[0] {
		case "run":
			args[0] = "new"
		case "transcript":
			args[0] = "read"
		}
	}
	return runChatWithRuntime(args, os.Stdin, stdout, stderr, runtime)
}

func runChat(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		fmt.Fprintf(stderr, "pfm: config: %v\n", err)
		return 1
	}
	return runChatWithRuntime(args, stdin, stdout, stderr, runtime)
}

func runChatWithRuntime(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) int {
	if len(args) == 0 {
		printChatUsage(stderr)
		return 2
	}
	verb, rest := args[0], args[1:]
	switch verb {
	case "new":
		return runRun(rest, stdout, stderr, runtime)
	case "open":
		return runChatOpen(rest, stdout, stderr, runtime)
	case "read":
		return runChatRead(rest, stdin, stdout, stderr, runtime)
	case "last":
		return runHeadlessLast(rest, stdout, stderr, runtime)
	case "status":
		return runHeadlessStatus(rest, stdout, stderr, runtime)
	case "stream":
		return runHeadlessStream(rest, stdout, stderr, runtime)
	case "inject":
		return runHeadlessInject(rest, stdout, stderr, runtime)
	case "self-compact":
		return runHeadlessSelfCompact(rest, stdout, stderr, runtime)
	case "ask":
		return runHeadlessAsk(rest, stdout, stderr, runtime)
	case "watch":
		return runHeadlessWatch(rest, stdout, stderr, runtime)
	case "capture":
		return runChatCapture(rest, stdout, stderr, runtime)
	case "keys":
		return runChatKeys(rest, stdout, stderr, runtime)
	case "recover":
		return runChatRecover(rest, stdout, stderr, runtime)
	case "name":
		return runChatName(rest, stdout, stderr, runtime)
	case "kill":
		return runChatKill(rest, stdout, stderr, runtime)
	case "unkill":
		return runChatUnkill(rest, stdout, stderr, runtime)
	case "end":
		return runChatEnd(rest, stdout, stderr, runtime)
	case "reload":
		return runChatReloadWithRuntime(rest, stdout, stderr, runtime)
	case "whoami":
		// The settled public command is `pfm whoami`; `pfm chat whoami`
		// remains a compatibility alias for callers already spelling it
		// this way.
		return runWhoami(rest, stdout, stderr, runtime)
	case "find", "save", "branch", "history", "ls":
		return runChatSatellite(verb, rest, stdin, stdout, stderr, runtime)
	case "modal":
		return runChatModal(rest, stdout, stderr)
	case "resolve":
		return runChatResolve(rest, stdout, stderr, runtime)
	case "help", "-h", "--help":
		printChatUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "pfm chat: unknown command %q\n", verb)
		printChatUsage(stderr)
		return 2
	}
}

func printChatUsage(w io.Writer) {
	fmt.Fprintln(w, "usage: pfm chat <command> [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "commands:")
	fmt.Fprintln(w, "  new         start a named chat on its own server")
	fmt.Fprintln(w, "  open        open a chat by name, socket, or id")
	fmt.Fprintln(w, "  status      inspect state; --summary recaps the last exchange, --ask reads the live pane")
	fmt.Fprintln(w, "  last        the chat's last assistant message")
	fmt.Fprintln(w, "  read        read the chat's transcript")
	fmt.Fprintln(w, "  stream      follow the transcript as it is written")
	fmt.Fprintln(w, "  inject      deliver a message to the chat")
	fmt.Fprintln(w, "  self-compact  compact this chat in place after its own turn settles")
	fmt.Fprintln(w, "  ask         deliver a message and wait for the answer")
	fmt.Fprintln(w, "  watch       block, reporting IDLE / EXIT / DEAD")
	fmt.Fprintln(w, "  capture     print a live chat's tmux scrollback")
	fmt.Fprintln(w, "  keys        press keys in a live chat's pane (Escape, Enter, C-c, …)")
	fmt.Fprintln(w, "  recover     rebuild a Codex conversation from its rollout")
	fmt.Fprintln(w, "  name        rename a chat and converge its window")
	fmt.Fprintln(w, "  kill        kill a chat, optionally closing it")
	fmt.Fprintln(w, "  unkill      remove a chat kill")
	fmt.Fprintln(w, "  end         end a chat's tmux server")
	fmt.Fprintln(w, "  reload      reboot a Claude chat in place under another configured account/cache mode")
	fmt.Fprintln(w, "  find/save/branch/history/ls/resolve")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "exit codes: 0 done · 2 usage · 3 chat dead · 4 no such chat")
	fmt.Fprintln(w, "            5 answer timed out · 6 message not delivered")
}

// headlessTarget resolves a name for a verb that needs one, reporting the
// refusal itself. A name nothing answers to is never silent and never rc 0.
func headlessTarget(
	ctx context.Context,
	name string,
	stdout, stderr io.Writer,
	asJSON bool,
	runtimes ...commandRuntime,
) (headless.Chat, int) {
	target, err := pfmchat.Target(ctx, name, firstRuntime(runtimes))
	if err != nil {
		return headless.Chat{}, reportTargetError(err, name, stdout, stderr, asJSON)
	}
	return target, 0
}

// reportTargetError renders a verb's target failure on the CLI contract: an
// unknown chat prints its missing row and exits codeUnknownChat; a scan that
// could not look exits 2 and never reads as "no such chat".
func reportTargetError(err error, name string, stdout, stderr io.Writer, asJSON bool) int {
	if errors.Is(err, pfmchat.ErrUnknownChat) {
		if asJSON {
			writeJSON(stdout, headless.Missing(name))
		} else {
			fmt.Fprintf(stdout, "%s\t%s\n", name, headless.StateMissing)
		}
		fmt.Fprintf(stderr, "pfm chat: no chat named %q\n", name)
		return codeUnknownChat
	}
	fmt.Fprintf(stderr, "pfm chat: %v\n", err)
	return 2
}

func runHeadlessStatus(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := newFlagSet(
		"chat status",
		"usage: pfm chat status <target> [--json] [--summary] [--ask] [--engine claude|codex] [--model MODEL]",
		stderr,
	)
	asJSON := flags.Bool("json", false, "emit one JSON object")
	withSummary := flags.Bool("summary", false, "summarize the last exchange")
	withAsk := flags.Bool("ask", false, "answer the chat's current status from its live pane capture and last exchange")
	summaryEngine := flags.String("engine", "", "override the configured ask engine")
	summaryModel := flags.String("model", "", "override the configured ask model")
	names, code, ok := parseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(names) != 1 {
		flags.Usage()
		return 2
	}
	if !*withSummary && !*withAsk && (*summaryEngine != "" || *summaryModel != "") {
		fmt.Fprintln(stderr, "pfm chat status: --engine and --model require --summary or --ask")
		return 2
	}
	var summaryEngineID pfmengine.ID
	if *summaryEngine != "" {
		var parseErr error
		summaryEngineID, parseErr = pfmengine.Parse(*summaryEngine)
		if parseErr != nil {
			fmt.Fprintf(stderr, "pfm chat status: %v\n", parseErr)
			return 1
		}
	}
	status, err := pfmchat.Status(context.Background(), firstRuntime(runtimes), pfmchat.StatusRequest{
		Target: names[0], Summary: *withSummary, Ask: *withAsk,
		Engine: summaryEngineID, Model: *summaryModel,
	}, stderr)
	var targetErr *pfmchat.TargetError
	if errors.As(err, &targetErr) {
		return reportTargetError(err, names[0], stdout, stderr, *asJSON)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat status: %v\n", err)
		return 1
	}
	if *asJSON {
		writeJSON(stdout, status)
	} else {
		fmt.Fprintln(stdout, status.Line())
		if *withSummary {
			fmt.Fprintln(stdout, status.SummaryLine())
		}
		if *withAsk {
			fmt.Fprintln(stdout, status.AskLine())
		}
	}
	if !status.Alive() {
		return codeDeadChat
	}
	return 0
}

func runHeadlessTranscript(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := newFlagSet(
		"chat read",
		"usage: pfm chat read <target> [--tail N] [--condensed] [--json]",
		stderr,
	)
	tail := flags.Int("tail", 1, "how many entries to read, newest last")
	condensed := flags.Bool("condensed", false, "one T/A/U line per entry")
	asJSON := flags.Bool("json", false, "emit a JSON array of entries")
	names, code, ok := parseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(names) != 1 || *tail < 1 {
		flags.Usage()
		return 2
	}
	ctx := context.Background()
	chat, entries, truncated, err := pfmchat.ReadEntries(ctx, names[0], *tail, firstRuntime(runtimes))
	if err != nil {
		if errors.Is(err, pfmchat.ErrUnknownChat) {
			fmt.Fprintf(stderr, "pfm chat: no chat named %q\n", names[0])
			return codeUnknownChat
		}
		if errors.Is(err, pfmchat.ErrNoTranscript) {
			fmt.Fprintf(stderr, "pfm chat read: %q has not written a transcript yet\n", chat.Name)
			return codeDeadChat
		}
		fmt.Fprintf(stderr, "pfm chat read: %v\n", err)
		return 1
	}
	switch {
	case *asJSON:
		writeJSON(stdout, map[string]any{
			"name":      chat.Name,
			"engine":    chat.Engine,
			"path":      chat.Path,
			"truncated": truncated,
			"entries":   entries,
		})
	case *condensed:
		for _, entry := range entries {
			fmt.Fprintln(stdout, transcript.Condensed(entry))
		}
	default:
		for _, entry := range entries {
			fmt.Fprintf(stdout, "%s: %s\n", entry.Role, entryText(entry))
		}
	}
	return 0
}

func runHeadlessLast(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := newFlagSet(
		"chat last",
		"usage: pfm chat last <target>",
		stderr,
	)
	names, code, ok := parseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(names) != 1 {
		flags.Usage()
		return 2
	}
	result, err := pfmchat.Last(context.Background(), firstRuntime(runtimes), pfmchat.LastRequest{Target: names[0]})
	var targetErr *pfmchat.TargetError
	switch {
	case errors.As(err, &targetErr):
		return reportTargetError(err, names[0], stdout, stderr, false)
	case errors.Is(err, pfmchat.ErrNoTranscript):
		fmt.Fprintf(stderr, "pfm chat last: %q has not written a transcript yet\n", result.Chat.Name)
		return codeDeadChat
	case errors.Is(err, pfmchat.ErrNoAnswer):
		fmt.Fprintf(stderr, "pfm chat last: %q has not answered yet\n", result.Chat.Name)
		return codeDeadChat
	case err != nil:
		fmt.Fprintf(stderr, "pfm chat last: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Text)
	return 0
}

func runHeadlessStream(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := newFlagSet(
		"chat stream",
		"usage: pfm chat stream <target> [--filter REGEX] [--margin N] "+
			"[--from-start] [--raw] [--no-follow]",
		stderr,
	)
	filter := flags.String("filter", "", "keep only lines matching this regexp")
	margin := flags.Int("margin", 0, "lines of context on each side of a match")
	fromStart := flags.Bool("from-start", false, "replay the transcript before following")
	raw := flags.Bool("raw", false, "print full entry text instead of condensed lines")
	noFollow := flags.Bool("no-follow", false, "drain what exists and exit")
	names, code, ok := parseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(names) != 1 || *margin < 0 {
		flags.Usage()
		return 2
	}
	var pattern *regexp.Regexp
	if *filter != "" {
		compiled, err := regexp.Compile(*filter)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat stream: bad --filter: %v\n", err)
			return 2
		}
		pattern = compiled
	}
	ctx := context.Background()
	chat, code := headlessTarget(ctx, names[0], stdout, stderr, false, runtimes...)
	if code != 0 {
		return code
	}
	if chat.Path == "" {
		fmt.Fprintf(stderr, "pfm chat stream: %q has not written a transcript yet\n", chat.Name)
		return codeDeadChat
	}
	name := chat.Name
	err := headless.Stream(ctx, chat.Path, chat.Engine, headless.StreamOptions{
		Filter:    pattern,
		Margin:    *margin,
		FromStart: *fromStart,
		Follow:    !*noFollow,
		Raw:       *raw,
		Alive: func() bool {
			chat, found, err := pfmchat.Resolve(context.Background(), name, io.Discard, firstRuntime(runtimes))
			return err == nil && found && chat.Live
		},
	}, stdout)
	if err != nil {
		if err == headless.ErrChatGone {
			fmt.Fprintf(stderr, "pfm chat stream: %s is gone\n", name)
			return codeDeadChat
		}
		fmt.Fprintf(stderr, "pfm chat stream: %v\n", err)
		return 1
	}
	return 0
}

func runHeadlessInject(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := newFlagSet(
		"chat inject",
		"usage: pfm chat inject [--force-now] [--then STEER]... [--file PATH] [--allow-unsigned] <target> <message>\n"+
			"       `/compact` is refused here — use `pfm chat self-compact`",
		stderr,
	)
	var force bool
	var retiredNoSig bool
	var allowUnsigned bool
	flags.BoolVar(&force, "now", false, "interrupt a working chat instead of waiting")
	flags.BoolVar(&force, "force-now", false, "interrupt a working chat instead of waiting")
	flags.BoolVar(&retiredNoSig, "no-sig", false, "retired compatibility flag; signatures remain mandatory")
	flags.BoolVar(&allowUnsigned, "allow-unsigned", false, "send even when no sender identity can be derived")
	messageFile := flags.String("file", "", "read the message from a file")
	var steers steerList
	flags.Var(&steers, "then", "follow-up steer; repeat for a chain")
	// Only the flags BEFORE the name are parsed: everything after it is the
	// message, verbatim. A message may legitimately start with a dash, and an
	// order silently eaten as a flag is an order never delivered.
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if retiredNoSig {
		fmt.Fprintln(stderr, "pfm chat inject: --no-sig is retired; delivery signatures remain enabled")
	}
	filePath := *messageFile
	trailingFile := flags.NArg() >= 2 && flags.Arg(1) == "--file"
	if trailingFile {
		if filePath != "" || flags.NArg() != 3 {
			flags.Usage()
			return 2
		}
		filePath = flags.Arg(2)
	}
	minimum := 2
	if filePath != "" {
		minimum = 1
	}
	if flags.NArg() < minimum {
		flags.Usage()
		return 2
	}
	targetName := flags.Arg(0)
	message := strings.Join(flags.Args()[1:], " ")
	if filePath != "" {
		if !trailingFile && flags.NArg() != 1 {
			flags.Usage()
			return 2
		}
		content, err := os.ReadFile(filePath)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat inject: read --file: %v\n", err)
			return 2
		}
		message = string(content)
	}
	if strings.TrimSpace(message) == "" {
		fmt.Fprintln(stderr, "pfm chat inject: refusing to inject an empty message")
		return 2
	}
	ctx := context.Background()
	engine, err := newInjectEngineAllowingUnsigned(allowUnsigned, runtimes...)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat inject: %v\n", err)
		return codeUndelivered
	}
	result, err := engine.Inject(ctx, inject.Request{
		Target:     targetName,
		Message:    message,
		ForceNow:   force,
		FileBacked: filePath != "",
		Then:       steers,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat inject: %v\n", err)
		return codeUndelivered
	}
	if result.Code == inject.CodeUnknown && !strings.Contains(strings.ToLower(result.Message), "ambiguous") {
		target, found, resolveErr := resolveResumeTarget(targetName, runtimes...)
		if resolveErr != nil {
			fmt.Fprintf(stderr, "pfm chat inject: %v\n", resolveErr)
			return codeUnknownChat
		}
		if found {
			machine, runtimeErr := optionalCommandRuntime(runtimes)
			if runtimeErr != nil {
				fmt.Fprintf(stderr, "pfm chat inject: %v\n", runtimeErr)
				return codeUndelivered
			}
			resolved := machine.Paths
			if uuidSessionID(target.ID) {
				live, liveErr := runningClaudeSession(resolved.ProcRoot, target.ID)
				if liveErr != nil {
					fmt.Fprintf(stderr, "pfm chat inject: %v\n", liveErr)
					return codeUndelivered
				}
				if live {
					fmt.Fprintf(
						stderr,
						"pfm chat inject: %q is a LIVE session held by a running process with no resolvable tmux pane; transcript append refused\n",
						targetName,
					)
					return codeDeadChat
				}
				crumb, live, crumbErr := liveCrumbSession(ctx, resolved, target.ID)
				if crumbErr != nil {
					fmt.Fprintf(stderr, "pfm chat inject: %v\n", crumbErr)
					return codeUndelivered
				}
				if live {
					fmt.Fprintf(
						stderr,
						"pfm chat inject: %q is a LIVE session at %s with no resolvable target on this path; transcript append refused\n",
						targetName,
						crumb,
					)
					return codeDeadChat
				}
				config, live, registryErr := registeredDaemonSession(ctx, resolved, machine.Config, target.ID)
				if registryErr != nil {
					fmt.Fprintf(stderr, "pfm chat inject: %v\n", registryErr)
					return codeUndelivered
				}
				if live {
					fmt.Fprintf(
						stderr,
						"pfm chat inject: %q is a LIVE background agent under config %s; it reads its input stream, not transcript appends, so nothing was appended\n",
						targetName,
						config,
					)
					return codeDeadChat
				}
			}
			if force {
				fmt.Fprintln(stderr, "pfm chat inject: --force-now ignored for a dormant RESUME target")
			}
			if len(steers) > 0 {
				fmt.Fprintf(
					stderr,
					"pfm chat inject: --then ignored for a dormant RESUME target (%d steer(s))\n",
					len(steers),
				)
			}
			prepared, prepareErr := engine.PrepareForResume(
				ctx,
				target.ID,
				string(pfmengine.Claude),
				message,
			)
			if prepareErr != nil {
				fmt.Fprintf(stderr, "pfm chat inject: prepare resume body: %v\n", prepareErr)
				return codeUndelivered
			}
			receipt, appendErr := appendResumeInjection(ctx, resolved, target, prepared.Message)
			if appendErr != nil {
				fmt.Fprintf(stderr, "pfm chat inject: %v\n", appendErr)
				return codeUndelivered
			}
			if prepared.Unsigned {
				writeUnsignedInjectWarning(stderr)
			}
			if prepared.AutoFilePath != "" {
				fmt.Fprintf(
					stdout,
					"injected RESUME AUTO-FILE pointer %s (parent %s) into session %s — full body saved at %s; answered on next reopen; backup: %s\n",
					receipt.EventID,
					receipt.ParentID,
					receipt.SessionID,
					prepared.AutoFilePath,
					receipt.Backup,
				)
			} else {
				fmt.Fprintf(
					stdout,
					"injected RESUME user turn %s (parent %s) into session %s — answered on next reopen; backup: %s\n",
					receipt.EventID,
					receipt.ParentID,
					receipt.SessionID,
					receipt.Backup,
				)
			}
			for _, warning := range prepared.Warnings {
				fmt.Fprintf(stderr, "pfm chat inject: WARNING: %s\n", warning)
			}
			return 0
		}
	}
	if result.Unsigned {
		// The recipient is told the message is unsigned; the SENDER is the one
		// who can do something about it, and only if the reason reaches them.
		writeUnsignedInjectWarning(stderr)
	}
	return writeInjectResult(result, targetName, stdout, stderr)
}

// singleSteer collects at most one --then flag. A self-compaction carries
// exactly one continuation steer, by the operator's rule; a second --then is
// a usage error (rc 2), never a chain.
type singleSteer struct {
	value string
	set   bool
}

func (single *singleSteer) String() string {
	return single.value
}

func (single *singleSteer) Set(value string) error {
	if single.set {
		return fmt.Errorf("--then may be given at most once")
	}
	if value == "" {
		return fmt.Errorf("a then steer must be non-empty")
	}
	single.value = value
	single.set = true
	return nil
}

// runHeadlessSelfCompact is the CLI twin of the chat_self_compact MCP tool —
// both share Engine.ScheduleSelfCompact (Task D), the ONE implementation
// that composes "/compact " + focus and waits for the caller's OWN turn to
// end before typing it, never a live /compact keystroke raced against
// whatever the operator is doing right now
// (the 2026-09-03 self-compact that ate an operator's live draft).
func runHeadlessSelfCompact(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := newFlagSet(
		"chat self-compact",
		// --then is NOT optional here, unlike chat inject's own
		// [--then STEER]...: checkSteerChain unconditionally refuses any
		// /compact primary (which is exactly what ScheduleSelfCompact
		// always composes) carrying zero Then entries, so a self-compact
		// call omitting --then always fails (F9 of the merge-gating
		// review). Bracket-free and singular on purpose: exactly one.
		"usage: pfm chat self-compact --then STEER <focus>",
		stderr,
	)
	var steer singleSteer
	flags.Var(
		&steer,
		"then",
		"the one mandatory post-compact steer, typed into the reborn chat once compaction settles",
	)
	if code, ok := parseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() < 1 {
		flags.Usage()
		return 2
	}
	focus := strings.Join(flags.Args(), " ")
	ctx := context.Background()
	engine, err := newInjectEngine(runtimes...)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat self-compact: %v\n", err)
		return codeUndelivered
	}
	var then []string
	if steer.value != "" {
		then = []string{steer.value}
	}
	result, err := engine.ScheduleSelfCompact(ctx, focus, then)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat self-compact: %v\n", err)
		return codeUndelivered
	}
	return writeInjectResult(result, "self", stdout, stderr)
}

func writeInjectResult(
	result inject.Result,
	target string,
	stdout, stderr io.Writer,
) int {
	if result.ResolutionNote != "" {
		fmt.Fprintln(stderr, result.ResolutionNote)
	}
	if result.Code != 0 {
		if result.Code == inject.CodeUnknown {
			fmt.Fprintf(stderr, "pfm chat: no chat named %q\n", target)
			if strings.Contains(strings.ToLower(result.Message), "ambiguous") {
				fmt.Fprintln(stderr, result.Message)
			}
		} else {
			fmt.Fprintln(stderr, result.Message)
		}
		if result.Proof != "" {
			fmt.Fprintln(stderr, "--- delivery proof (FAILED): target pane tail ---")
			fmt.Fprintln(stderr, result.Proof)
			fmt.Fprintln(stderr, "--- end delivery proof ---")
		}
		switch result.Code {
		case inject.CodeUnknown:
			return codeUnknownChat
		case inject.CodeAmbiguous:
			return 2
		case inject.CodeDead:
			return codeDeadChat
		default:
			return codeUndelivered
		}
	}
	fmt.Fprintln(stdout, result.Message)
	if result.Proof != "" {
		// A "scheduled" result (runHeadlessSelfCompact's success case: the
		// primary hasn't typed yet, it rides out the caller's own turn
		// first) carries no pane proof — Typed and Proof both stay Go zero
		// values. Printing the "--- delivery proof ---" framing anyway
		// would sit an empty body under a header that reads as confirmed,
		// verified delivery to anyone scanning stdout, when nothing has
		// actually been typed yet.
		fmt.Fprintln(stdout, "--- delivery proof: target pane tail ---")
		fmt.Fprintln(stdout, result.Proof)
		fmt.Fprintln(stdout, "--- end delivery proof ---")
	}
	return 0
}

// writeUnsignedInjectWarning is now reached only when the operator asked for
// it with --allow-unsigned. Without that flag the send is REFUSED before
// delivery (inject.ErrUnsigned), because a message the recipient must not act
// on was never worth delivering. The warning stays because a deliberate
// unsigned send should still say what it cost.
func writeUnsignedInjectWarning(stderr io.Writer) {
	fmt.Fprintln(
		stderr,
		"WARNING: sent UNSIGNED (--allow-unsigned) — this process derived no"+
			" identity of its own, so the recipient cannot verify who sent"+
			" this and should not act on it. If this ran DETACHED"+
			" (setsid/nohup/disowned), that is why: detaching severs the"+
			" process chain the handle is recovered from. Send from the chat"+
			" itself, or state it: "+
			inject.SenderSessionEnv+"=$(pfm whoami) "+
			inject.SenderLabelEnv+"=<label> <command>.",
	)
}

func runHeadlessWatch(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := newFlagSet(
		"chat watch",
		"usage: pfm chat watch <target> [--idle-after SECS] "+
			"[--on-idle CMD] [--on-exit CMD] [--once]",
		stderr,
	)
	idleAfter := flags.Int("idle-after", 0, "seconds of idle before IDLE is emitted")
	onIdle := flags.String("on-idle", "", "shell command to run on IDLE")
	onExit := flags.String("on-exit", "", "shell command to run on EXIT or DEAD")
	once := flags.Bool("once", false, "stop after the first IDLE")
	poll := flags.Int("poll", 2, "seconds between samples")
	names, code, ok := parseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(names) != 1 || *idleAfter < 0 || *poll < 1 {
		flags.Usage()
		return 2
	}
	name := names[0]
	ctx := context.Background()
	if _, code := headlessTarget(ctx, name, stdout, stderr, false, runtimes...); code != 0 {
		return code
	}
	watcher := headless.Watcher{
		Name: name,
		Resolve: func(ctx context.Context) (headless.Chat, bool, error) {
			return pfmchat.Resolve(ctx, name, io.Discard, firstRuntime(runtimes))
		},
	}
	status, err := watcher.Watch(ctx, headless.WatchOptions{
		IdleAfter: time.Duration(*idleAfter) * time.Second,
		Poll:      time.Duration(*poll) * time.Second,
		Once:      *once,
		OnIdle:    hookRunner(*onIdle, stderr),
		OnExit:    hookRunner(*onExit, stderr),
	}, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat watch: %v\n", err)
		return 1
	}
	if !status.Alive() {
		return codeDeadChat
	}
	return 0
}

// hookRunner runs a --on-idle/--on-exit command with the chat's facts in the
// environment, so a hook can act without re-resolving anything.
func hookRunner(command string, stderr io.Writer) func(headless.Status) error {
	if strings.TrimSpace(command) == "" {
		return nil
	}
	return func(status headless.Status) error {
		process := exec.Command(deps.Executable("sh"), "-c", command)
		process.Env = append(
			os.Environ(),
			"CC_CHAT_NAME="+status.Name,
			"CC_CHAT_STATE="+status.State,
			"CC_CHAT_ENGINE="+string(status.Engine),
			"CC_CHAT_SOCKET="+status.Socket,
			"CC_CHAT_SESSION_ID="+status.SessionID,
		)
		process.Stdout = stderr
		process.Stderr = stderr
		if err := process.Run(); err != nil {
			fmt.Fprintf(stderr, "pfm chat watch: hook failed: %v\n", err)
		}
		return nil
	}
}

func entryText(entry transcript.Entry) string {
	if entry.Role == transcript.RoleTool {
		return entry.Tool + " " + entry.Input
	}
	return entry.Text
}

func writeJSON(out io.Writer, value any) {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	_ = encoder.Encode(value)
}
