package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/agentrole"
	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/inject"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/recovery"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

func renderNoSuchChat(name string, stdout, stderr io.Writer, asJSON bool) int {
	if asJSON {
		if err := writeJSON(stdout, headless.Missing(name)); err != nil {
			fmt.Fprintf(stderr, "pfm chat: encode JSON: %v\n", err)
			return 1
		}
	} else {
		fmt.Fprintf(stdout, "%s\t%s\n", name, headless.StateMissing)
	}
	fmt.Fprintf(stderr, "pfm chat: no chat named %q\n", name)
	return codeUnknownChat
}

func chatResolver(
	handle string,
	runtimes ...commandRuntime,
) func(context.Context) (headless.Chat, bool, error) {
	return func(ctx context.Context) (headless.Chat, bool, error) {
		return pfmchat.Resolve(ctx, handle, io.Discard, firstRuntime(runtimes))
	}
}

func writeJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func runChatRead(args []string, _ io.Reader, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	if len(args) > 0 {
		if info, err := os.Stat(args[0]); err == nil && info.Mode().IsRegular() &&
			filepath.Ext(args[0]) != ".jsonl" {
			return runChatReadExcerpt(args, stdout, stderr, runtimes...)
		}
	}
	return runHeadlessTranscript(args, stdout, stderr, runtimes...)
}

func runChatOpen(
	args []string,
	stdout, stderr io.Writer,
	runtime commandRuntime,
) (exitCode int) {
	flags := cli.NewFlagSet("chat open", "usage: pfm chat open <target>", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	chat, code := headlessTarget(context.Background(), flags.Arg(0), stdout, stderr, false, runtime)
	if code != 0 {
		return code
	}
	return pfmchat.OpenID(context.Background(), chat.ID, stdout, stderr, &runtime)
}

func runChatKill(args []string, stdout, stderr io.Writer, env paths.Env, runtimes ...commandRuntime) (code int) {
	return runChatKillContext(context.Background(), args, stdout, stderr, env, runtimes...)
}

func runChatKillContext(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	env paths.Env,
	runtimes ...commandRuntime,
) (code int) {
	defer func() { pfmchat.RecordVerb(ctx, "kill", code) }()
	env = defaultEnv(env)
	flags := cli.NewFlagSet("chat kill", "usage: pfm chat kill <target> [--exit]", stderr)
	exit := flags.Bool("exit", false, "gracefully close after killing")
	targets, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(targets) != 1 {
		flags.Usage()
		return 2
	}
	if targets[0] == "self" || targets[0] == "me" {
		if chat, scoped := pfmchat.ScopedSelf(ctx); scoped {
			confirm := *exit || (chat.Live && chat.Socket != "" && chat.Pane != "")
			return runResolvedChatKillContext(ctx, chat, confirm, stdout, stderr, runtimes...)
		}
		if env.Get("TMUX") == "" && env.Get(resolve.CodexThreadEnv) != "" {
			chat, found, err := pfmchat.Resolve(ctx, targets[0], io.Discard, firstRuntime(runtimes))
			if err != nil {
				fmt.Fprintf(stderr, "pfm chat kill: %v\n", err)
				return 1
			}
			if !found {
				fmt.Fprintln(stderr, "pfm chat kill: this Codex chat has no live fleet seat")
				return codeUnknownChat
			}
			return runResolvedChatKillContext(ctx, chat, *exit, stdout, stderr, runtimes...)
		}
		killArgs := []string{"--self"}
		if *exit {
			killArgs = append(killArgs, "--exit")
		}
		return runKill(killArgs, stdout, stderr, runtimes...)
	}
	target := targets[0]
	id := target
	// Resolve before killing so a live target retains its exact socket and pane.
	chat, found, err := pfmchat.Resolve(ctx, target, io.Discard, firstRuntime(runtimes))
	if err != nil {
		if !fleet.ChatIDPattern.MatchString(target) {
			fmt.Fprintf(stderr, "pfm chat kill: %v\n", err)
			return 1
		}
		// An id needs no roster to be tombstoned, so a resolve failure must
		// not swallow a kill that never depended on it — but it is reported,
		// because the kill that follows can only de-list.
		fmt.Fprintf(
			stderr,
			"pfm chat kill: could not resolve %s to a live pane (%v); recording the kill without closing it\n",
			target, err,
		)
		found = false
	}
	switch {
	case found:
		id = chat.ID
		if chat.Live && chat.Socket != "" && chat.Pane != "" {
			return runResolvedChatKillContext(ctx, chat, true, stdout, stderr, runtimes...)
		}
		if chat.Live {
			// Live with no address to close: say so instead of letting the
			// tombstone read like a termination.
			fmt.Fprintf(
				stderr,
				"pfm chat kill: %s is live but carries no tmux address "+
					"(socket %q pane %q) — recording the kill without closing it\n",
				id, chat.Socket, chat.Pane,
			)
		}
	case !fleet.ChatIDPattern.MatchString(target):
		return renderNoSuchChat(target, stdout, stderr, false)
	}
	killArgs := make([]string, 0, 2)
	if *exit {
		killArgs = append(killArgs, "--exit")
	}
	killArgs = append(killArgs, id)
	var killOut, killErr strings.Builder
	code = runKill(killArgs, &killOut, &killErr, runtimes...)
	_, _ = io.WriteString(stdout, killOut.String())
	_, _ = io.WriteString(stderr, killErr.String())
	if code != 0 && strings.Contains(killErr.String(), "not indexed") {
		return codeUnknownChat
	}
	return code
}

func runResolvedChatKill(
	chat headless.Chat,
	exit bool,
	stdout, stderr io.Writer,
	runtimes ...commandRuntime,
) (exitCode int) {
	return pfmchat.KillResolved(context.Background(), chat, exit, stdout, stderr, firstRuntime(runtimes))
}

func runResolvedChatKillContext(
	ctx context.Context,
	chat headless.Chat,
	exit bool,
	stdout, stderr io.Writer,
	runtimes ...commandRuntime,
) (exitCode int) {
	return pfmchat.KillResolved(ctx, chat, exit, stdout, stderr, firstRuntime(runtimes))
}

func runChatUnkill(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) (code int) {
	return runChatUnkillContext(context.Background(), args, stdout, stderr, runtimes...)
}

func runChatUnkillContext(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	runtimes ...commandRuntime,
) (code int) {
	defer func() { pfmchat.RecordVerb(ctx, "unkill", code) }()
	flags := cli.NewFlagSet("chat unkill", "usage: pfm chat unkill <target>", stderr)
	if parseCode, ok := cli.ParseFlags(flags, args); !ok {
		return parseCode
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	target := flags.Arg(0)
	if !fleet.ChatIDPattern.MatchString(target) {
		chat, found, err := pfmchat.Resolve(ctx, target, io.Discard, firstRuntime(runtimes))
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat unkill: %v\n", err)
			return 1
		}
		if !found {
			return renderNoSuchChat(target, stdout, stderr, false)
		}
		target = chat.ID
	}
	code = runUnkill([]string{target}, stdout, stderr, runtimes...)
	return code
}

func runChatResolve(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet("chat resolve", "usage: pfm chat resolve <target>", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	name := flags.Arg(0)
	engine, err := pfmchat.NewInjectEngine(false, firstRuntime(runtimes))
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat resolve: %v\n", err)
		return codeUndelivered
	}
	target, code, detail, err := engine.Resolve(context.Background(), name)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat resolve: %v\n", err)
		return codeUndelivered
	}
	if code != 0 {
		if code == inject.CodeUnknown {
			return renderNoSuchChat(name, stdout, stderr, false)
		}
		if detail != "" {
			fmt.Fprintln(stderr, detail)
		}
		if code == inject.CodeAmbiguous {
			return 2
		}
		return codeUndelivered
	}
	fmt.Fprintf(
		stdout, "%s\t%s\t%s\n",
		filepath.Base(target.SocketPath), target.Session, target.ID,
	)
	return 0
}

func runChatCapture(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet("chat capture", "usage: pfm chat capture <target>", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	chat, code := headlessTarget(context.Background(), flags.Arg(0), stdout, stderr, false, runtimes...)
	if code != 0 {
		return code
	}
	if !chat.Live {
		fmt.Fprintf(stderr, "pfm chat capture: %q is not running\n", chat.Name)
		return codeDeadChat
	}
	socketPath, err := chatSocketPath(chat.Socket)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat capture: %v\n", err)
		return 1
	}
	capture, err := (inject.TmuxInjector{}).Capture(
		context.Background(), socketPath, pfmchat.PaneTarget(chat), true, inject.FullScrollback,
	)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat capture: %v\n", err)
		return codeDeadChat
	}
	fmt.Fprint(stdout, capture)
	if !strings.HasSuffix(capture, "\n") {
		fmt.Fprintln(stdout)
	}
	return 0
}

func runChatRecover(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet("chat recover", "usage: pfm chat recover <thread-id|rollout-path>", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	_, err := pfmconfig.OptionalRuntime(runtimes)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat recover: load config: %v\n", err)
		return 1
	}
	resolved, err := paths.Resolve()
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat recover: resolve source paths: %v\n", err)
		return 1
	}
	codexHome := resolved.FirstRoot(pfmengine.Codex)
	result, err := recovery.Run(context.Background(), codexHome, flags.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat recover: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "messages=%d carried=%d malformed=%d\n", result.Messages, result.Carried, result.Malformed)
	fmt.Fprintf(stdout, "recovered thread %s\n", result.ThreadID)
	fmt.Fprintf(
		stdout,
		"  %s\n  %s\n  %s\n",
		filepath.Join(codexHome, "recovered-"+result.ThreadID, "brief.md"),
		filepath.Join(codexHome, "recovered-"+result.ThreadID, "compaction-memory.md"),
		filepath.Join(codexHome, "recovered-"+result.ThreadID, "transcript.md"),
	)
	fmt.Fprintf(
		stdout,
		"\nBrief a replacement seat with:\n  pfm chat inject <socket> 'RECOVERY: read %s, then compaction-memory.md, then the end of transcript.md.'\n",
		filepath.Join(codexHome, "recovered-"+result.ThreadID, "brief.md"),
	)
	return 0
}

func runChatName(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) int {
	return runChatNameContext(context.Background(), args, stdout, stderr, runtimes...)
}

func runChatNameContext(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	runtimes ...commandRuntime,
) int {
	deliver := func(ctx context.Context, chat headless.Chat, name string) (int, string, error) {
		return deliverChatNameWithRuntime(ctx, chat, name, runtimes...)
	}
	return runChatNameWithContext(ctx, args, stdout, stderr, deliver, runtimes...)
}

type chatNameDelivery func(
	context.Context,
	headless.Chat,
	string,
) (int, string, error)

func deliverChatNameWithRuntime(
	ctx context.Context,
	chat headless.Chat,
	name string,
	runtimes ...commandRuntime,
) (int, string, error) {
	return pfmchat.DeliverName(ctx, chat, name, firstRuntime(runtimes))
}

func runChatNameWithContext(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
	deliver chatNameDelivery,
	runtimes ...commandRuntime,
) int {
	flags := cli.NewFlagSet("chat name", "usage: pfm chat name <target> <name>", stderr)
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() < 2 {
		flags.Usage()
		return 2
	}
	name := strings.TrimSpace(strings.Join(flags.Args()[1:], " "))
	if name == "" || strings.ContainsAny(name, "\r\n\x00") {
		fmt.Fprintln(stderr, "pfm chat name: name must be one non-empty line")
		return 2
	}
	chat, code := headlessTarget(ctx, flags.Arg(0), stdout, stderr, false, runtimes...)
	if code != 0 {
		return code
	}
	if !chat.Live {
		fmt.Fprintf(stderr, "pfm chat name: %q is not running\n", chat.Name)
		return codeDeadChat
	}
	code = applyChatName(ctx, chat, name, deliver, stderr)
	if code != 0 {
		return code
	}
	fmt.Fprintf(stdout, "named %s -> %s\n", chat.ID, name)
	return 0
}

func applyChatName(
	ctx context.Context,
	chat headless.Chat,
	name string,
	deliver chatNameDelivery,
	stderr io.Writer,
) (code int) {
	defer func() { pfmchat.RecordVerb(ctx, "name", code) }()
	resultCode, resultMessage, err := deliver(ctx, chat, name)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat name: %v\n", err)
		return 1
	}
	if resultCode != 0 {
		fmt.Fprintf(stderr, "pfm chat name: %s\n", resultMessage)
		return resultCode
	}
	target := chat.Pane
	if target == "" {
		target = chat.Session
	}
	if err := renameChatWindow(ctx, chat.Socket, target, name); err != nil {
		fmt.Fprintf(stderr, "pfm chat name: rename delivered but window convergence failed: %v\n", err)
		return 1
	}
	return 0
}

func runChatEnd(args []string, stdout, stderr io.Writer, runtimes ...commandRuntime) (code int) {
	defer func() { pfmchat.RecordVerb(context.Background(), "end", code) }()
	flags := cli.NewFlagSet("chat end", "usage: pfm chat end <target>", stderr)
	if parseCode, ok := cli.ParseFlags(flags, args); !ok {
		return parseCode
	}
	if flags.NArg() != 1 {
		flags.Usage()
		return 2
	}
	chat, code := headlessTarget(context.Background(), flags.Arg(0), stdout, stderr, false, runtimes...)
	if code != 0 {
		return code
	}
	if !chat.Live {
		fmt.Fprintf(stderr, "pfm chat end: %q is not running\n", chat.Name)
		return codeDeadChat
	}
	socketPath, err := chatSocketPath(chat.Socket)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat end: %v\n", err)
		return 1
	}
	command := pfmtmux.Exec(context.Background(), "", socketPath, "kill-server")
	if output, err := command.CombinedOutput(); err != nil {
		fmt.Fprintf(stderr, "pfm chat end: %v: %s\n", err, strings.TrimSpace(string(output)))
		return 1
	}
	// The seat is dead, so its prompt file is litter. Cleanup is best-effort:
	// the end itself succeeded and a prompt-file failure must remain visible
	// without changing that result.
	if endRuntime, err := pfmconfig.OptionalRuntime(runtimes); err != nil {
		fmt.Fprintf(stderr, "pfm chat end: WARNING: could not resolve paths to remove its role prompt: %v\n", err)
	} else if err := agentrole.RemoveSeatPrompt(endRuntime.Paths.SIDDir, chat.Socket, chat.Pane); err != nil {
		fmt.Fprintf(stderr, "pfm chat end: WARNING: could not remove role prompt: %v\n", err)
	}
	fmt.Fprintf(stdout, "ended %s\n", chat.ID)
	return 0
}

func renameChatWindow(ctx context.Context, socket, target, name string) error {
	// Every window-name writer clips through WindowNameFor, or the writers
	// rename the window back and forth (see its doc).
	name = gather.WindowNameFor(name)
	socketPath, err := chatSocketPath(socket)
	if err != nil {
		return err
	}
	if target == "" {
		target = socket
	}
	command := pfmtmux.Exec(ctx, "", socketPath, "rename-window", "-t", target, name)
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("tmux rename-window: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func chatSocketPath(socket string) (string, error) {
	return paths.SocketPath(socket)
}

func runChatSatellite(
	verb string,
	args []string,
	_ io.Reader,
	stdout, stderr io.Writer,
	env paths.Env,
	clk clock.Clock,
	runtimes ...commandRuntime,
) int {
	switch verb {
	case "find":
		return runChatFind(args, stdout, stderr, runtimes...)
	case "save":
		return runChatSave(args, stdout, stderr, env, runtimes...)
	case branchAction:
		return runChatBranch(args, stdout, stderr, env, clk, runtimes...)
	case "ls":
		return runChatLS(args, stdout, stderr, clk, runtimes...)
	case "history":
		return runChatHistory(args, stdout, stderr, runtimes...)
	default:
		fmt.Fprintf(stderr, "pfm chat: unsupported compatibility command %q\n", verb)
		return 2
	}
}
