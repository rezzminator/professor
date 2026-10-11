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
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

func runHeadlessWatch(args []string, stdout, stderr io.Writer, runner deps.Runner, runtimes ...commandRuntime) int {
	return watchCommand(context.Background(), nil, args, stdout, stderr, runner, runtimes...)
}

// watchCommand is `pfm chat watch` under a caller's context and poll clock:
// nil is the real clock. The jail ladder drives its own, whose poll timers
// fire when the test says, so every rung is one poll of real seats.
func watchCommand(
	ctx context.Context,
	pollClock clock.Clock,
	args []string,
	stdout, stderr io.Writer,
	runner deps.Runner,
	runtimes ...commandRuntime,
) int {
	usage := "usage: pfm chat watch <target|glob>... [--transitions [--quiet-after SECS]] [--idle-after SECS] " +
		"[--on-idle CMD] [--on-exit CMD] [--once] [--poll SECS]"
	flags := cli.NewFlagSet("chat watch", usage, stderr)
	// The flags' own lines follow the usage line, so --help states the poll
	// default and floor a caller sizes its wait by.
	flags.Usage = func() {
		fmt.Fprintln(stderr, usage)
		flags.PrintDefaults()
	}
	transitions := flags.Bool("transitions", false, "announce every state change, after one SEEN snapshot per target")
	quietAfter := flags.Int("quiet-after", 0, "with --transitions: seconds of unchanged transcript before QUIET")
	idleAfter := flags.Int("idle-after", 0, "seconds of idle before IDLE is emitted")
	onIdle := flags.String("on-idle", "", "shell command to run on IDLE")
	onExit := flags.String("on-exit", "", "shell command to run on EXIT or DEAD")
	once := flags.Bool("once", false, "stop each target after its first IDLE")
	minPoll := int(headless.MinPoll / time.Second)
	poll := flags.Int("poll", minPoll, fmt.Sprintf("seconds between samples (minimum %d; a smaller value runs as %d)",
		minPoll, minPoll))
	targets, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(targets) == 0 || *idleAfter < 0 || *quietAfter < 0 || (*quietAfter > 0 && !*transitions) {
		flags.Usage()
		return 2
	}
	if *poll < minPoll {
		fmt.Fprintf(stderr, "pfm chat watch: --poll %d is below the %d-second minimum; polling every %d seconds\n",
			*poll, minPoll, minPoll)
		*poll = minPoll
	}
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
	// One scan per poll tick serves every watch on the box: a share that
	// cannot open costs this watch its saving, never a fact.
	share, err := pfmchat.NewScanShare(runtime)
	if err != nil {
		obs.Logger(ctx).Warn("watch.snapshot.open", obs.FieldErr, err.Error())
	}
	precheck := named
	if *transitions {
		precheck = nil // first sight answers with SEEN or ERROR
	}
	// The precheck's scan is the first poll's: a watch that starts pays for
	// one scan, shared with the watches starting beside it.
	var first *watchRound
	if len(precheck) > 0 {
		now := clock.Real.Now
		if pollClock != nil {
			now = pollClock.Now
		}
		first = &watchRound{runtime: runtime, share: share, since: now()}
	}
	for _, target := range precheck {
		if _, err := pfmchat.TargetIn(ctx, target, first.rows, runtime); err != nil {
			rc := reportTargetError(err, target, stdout, stderr, false)
			if !errors.Is(err, pfmchat.ErrUnknownChat) {
				fmt.Fprintln(stdout, headless.ErrorLine(target, err))
			}
			return rc
		}
	}
	// A followed seat whose fingerprint holds keeps its row without a scan; a
	// printer that cannot open costs the watch a scan per poll, never a fact.
	var fingerprint func(context.Context, headless.Chat) (string, bool)
	if printer, err := pfmchat.NewSeatPrinter(runtime); err != nil {
		obs.Logger(ctx).Warn("watch.fingerprint.open", obs.FieldErr, err.Error())
	} else {
		fingerprint = func(_ context.Context, chat headless.Chat) (string, bool) { return printer.Print(chat) }
	}
	// Each seat's transcript is read from where the last poll left it.
	inspector := pfmchat.NewSeatInspector(runtime)
	fleet := headless.FleetWatcher{
		Targets: targets,
		Discover: func(since time.Time) headless.Discovery {
			inspector.Sweep() // one poll per discovery: the seats not inspected since the last
			round := first
			if first = nil; round == nil {
				round = &watchRound{runtime: runtime, share: share, since: since}
			}
			return round.discovery()
		},
		Inspect:     inspector.Inspect,
		Fingerprint: fingerprint,
		Clock:       pollClock,
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

// watchRound is one poll's fleet scan, run on first use and shared by every
// target of the poll: each named target resolves against it and the globs
// list it, so a poll costs one scan however many targets it serves. With a
// share, the scan is the one another watch took at this poll's tick, when one
// did (pfmchat.ScanShare).
type watchRound struct {
	runtime *commandRuntime
	share   *pfmchat.ScanShare
	since   time.Time
	scanned bool
	scan    pfmchat.FleetScan
	err     error
}

func (round *watchRound) load(ctx context.Context) (pfmchat.FleetScan, error) {
	if !round.scanned {
		scan := func(ctx context.Context) (pfmchat.FleetScan, error) {
			return pfmchat.ScanRows(ctx, io.Discard, round.runtime)
		}
		if round.share != nil {
			round.scan, round.err = round.share.Scan(ctx, round.since, scan)
		} else {
			round.scan, round.err = scan(ctx)
		}
		round.scanned = true
	}
	return round.scan, round.err
}

// rows is the round's listing, scanned on first use.
func (round *watchRound) rows(ctx context.Context) ([]compose.Row, error) {
	scan, err := round.load(ctx)
	return scan.Rows, err
}

func (round *watchRound) discovery() headless.Discovery {
	return headless.Discovery{
		Resolve: func(ctx context.Context, name string) (headless.Chat, bool, error) {
			return pfmchat.ResolveIn(ctx, name, round.rows, io.Discard, round.runtime)
		},
		List: func(ctx context.Context) ([]headless.Chat, error) {
			scan, err := round.load(ctx)
			if err != nil {
				return nil, err
			}
			chats := make([]headless.Chat, 0, len(scan.Rows))
			for index := range scan.Rows {
				chats = append(chats, pfmchat.FromRow(scan.Rows[index]))
			}
			return chats, nil
		},
		Unreadable: func() map[string]string { return round.scan.Unreadable },
	}
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
