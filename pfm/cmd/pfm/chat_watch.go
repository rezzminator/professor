package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/headless"
)

func runHeadlessWatch(args []string, stdout, stderr io.Writer, runner deps.Runner, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet(
		"chat watch",
		"usage: pfm chat watch <target|glob>... [--transitions [--quiet-after SECS]] [--idle-after SECS] "+
			"[--on-idle CMD] [--on-exit CMD] [--once] [--poll SECS]",
		stderr,
	)
	transitions := flags.Bool("transitions", false, "announce every state change, after one SEEN snapshot per target")
	quietAfter := flags.Int("quiet-after", 0, "with --transitions: seconds of unchanged transcript before QUIET")
	idleAfter := flags.Int("idle-after", 0, "seconds of idle before IDLE is emitted")
	onIdle := flags.String("on-idle", "", "shell command to run on IDLE")
	onExit := flags.String("on-exit", "", "shell command to run on EXIT or DEAD")
	once := flags.Bool("once", false, "stop each target after its first IDLE")
	poll := flags.Int("poll", 2, "seconds between samples")
	targets, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(targets) == 0 || *idleAfter < 0 || *poll < 1 || *quietAfter < 0 || (*quietAfter > 0 && !*transitions) {
		flags.Usage()
		return 2
	}
	ctx := context.Background()
	runtime := firstRuntime(runtimes)
	var named []string
	for _, target := range targets {
		glob, err := headless.ClassifyTarget(target)
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat watch: %v\n", err)
			return 2
		}
		if glob {
			continue
		}
		named = append(named, target)
	}
	precheck := named
	if *transitions {
		precheck = nil // first sight answers with SEEN or ERROR
	}
	for _, target := range precheck {
		if _, err := pfmchat.Target(ctx, target, runtime); err != nil {
			rc := reportTargetError(err, target, stdout, stderr, false)
			if !errors.Is(err, pfmchat.ErrUnknownChat) {
				fmt.Fprintln(stdout, headless.ErrorLine(target, err))
			}
			return rc
		}
	}
	fleet := headless.FleetWatcher{
		Targets: targets,
		Resolve: func(ctx context.Context, name string) (headless.Chat, bool, error) {
			return pfmchat.Resolve(ctx, name, io.Discard, runtime)
		},
		List: func(ctx context.Context) ([]headless.Chat, error) {
			rows, err := pfmchat.Rows(ctx, io.Discard, runtime)
			chats := make([]headless.Chat, 0, len(rows))
			for index := range rows {
				chats = append(chats, pfmchat.FromRow(rows[index]))
			}
			return chats, err
		},
		Inspect: func(ctx context.Context, chat headless.Chat, now time.Time) (headless.Status, error) {
			return pfmchat.InspectSeat(ctx, runtime, chat, now, nil)
		},
	}
	result, err := fleet.Watch(ctx, headless.WatchOptions{
		IdleAfter:   time.Duration(*idleAfter) * time.Second,
		Poll:        time.Duration(*poll) * time.Second,
		Once:        *once,
		Transitions: *transitions,
		QuietAfter:  time.Duration(*quietAfter) * time.Second,
		OnIdle:      hookRunner(*onIdle, runner, stderr),
		OnExit:      hookRunner(*onExit, runner, stderr),
	}, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat watch: %v\n", err)
		return 1
	}
	if len(result.Errors) > 0 {
		for _, name := range slices.Sorted(maps.Keys(result.Errors)) {
			fmt.Fprintf(stderr, "pfm chat watch: %s: %v\n", name, result.Errors[name])
		}
		return 1
	}
	for _, name := range named {
		if status := result.Ended[name]; !status.Alive() {
			return codeDeadChat
		}
	}
	return 0
}

func hookRunner(
	command string,
	runner deps.Runner,
	stderr io.Writer,
) func(headless.Status) error { // routed through the deps.Runner seam
	if strings.TrimSpace(command) == "" {
		return nil
	}
	return func(status headless.Status) error {
		argv := []string{deps.Executable("sh"), "-c", command}
		env := append(os.Environ(),
			"CC_CHAT_NAME="+status.Name,
			"CC_CHAT_STATE="+status.State,
			"CC_CHAT_ENGINE="+string(status.Engine),
			"CC_CHAT_SOCKET="+status.Socket,
			"CC_CHAT_SESSION_ID="+status.SessionID,
			"CC_CHAT_ERROR="+status.Error,
			"CC_CHAT_IDLE_SECONDS="+strconv.FormatInt(status.IdleSeconds, 10),
		)
		process, err := runner.Start(
			context.Background(),
			argv,
			deps.StartOptions{Env: env, Stdout: stderr, Stderr: stderr},
		)
		if err == nil {
			err = process.Wait()
		}
		if err != nil {
			fmt.Fprintf(stderr, "pfm chat watch: hook failed: %v\n", err)
		}
		return nil
	}
}
