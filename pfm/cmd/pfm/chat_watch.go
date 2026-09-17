package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/headless"
)

func runHeadlessWatch(args []string, stdout, stderr io.Writer, runner deps.Runner, runtimes ...commandRuntime) int {
	flags := cli.NewFlagSet(
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
	names, code, ok := cli.ParseFlagsAnywhere(flags, args)
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
		Name:    name,
		Resolve: chatResolver(name, runtimes...),
	}
	status, err := watcher.Watch(ctx, headless.WatchOptions{
		IdleAfter: time.Duration(*idleAfter) * time.Second,
		Poll:      time.Duration(*poll) * time.Second,
		Once:      *once,
		OnIdle:    hookRunner(*onIdle, runner, stderr),
		OnExit:    hookRunner(*onExit, runner, stderr),
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
