package spawn

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/servicemanager"
	"github.com/rezzminator/professor/pfm/internal/themesync"
	"github.com/rezzminator/professor/pfm/internal/themetmux"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// SessionCreatedError reports creation whose child has not been proven terminated.
// Callers must retain launch ownership even when no successful result is returned.
type SessionCreatedError struct {
	Err error
}

func (err *SessionCreatedError) Error() string { return err.Err.Error() }
func (err *SessionCreatedError) Unwrap() error { return err.Err }

// TmuxSpawner invokes tmux only through the configured socket directory, the
// same jailed shape action.TmuxExecutor uses.
type TmuxSpawner struct {
	Binary  string
	TmuxDir string
	// Titles is the resolved tmux.titles policy. NIL is the default (pfm owns
	// the terminal title), never "off": a client built without a machine
	// config keeps today's behaviour instead of silently handing the title to
	// the host.
	Titles *pfmconfig.TmuxTitles
	// applyTheme puts the theme on a born server; nil is themesync.ApplyTmuxServer.
	// A test injects a recorder here.
	applyTheme func(ctx context.Context, servers themesync.TmuxServers, stateDir, socket string) error
	// Env is the host-environment seam (pfm/TESTPLAN.md § Seams, paths.Env)
	// chatServerCommand reads INVOCATION_ID through; nil defaults to
	// paths.OSEnv{}.
	Env paths.Env
	// serviceKind is the service manager a chat server escapes; "" is
	// servicemanager.Native(). A test sets the other kind here.
	serviceKind servicemanager.Kind
	// lookPath resolves the scope launcher; nil is the real runner's. A test
	// points it at a fake.
	lookPath func(string) (string, error)
	// Screen is the pane door Pane hands out; nil is the call engine over
	// Binary. A test injects a recorder here.
	Screen pfmcall.Screen
}

// preflightBinary proves the executable word a pane is about to run resolves
// from THIS process's environment — the same environment the pane's shell
// inherits. A pane whose command cannot resolve dies at launch and takes the
// fresh server with it, so the visible failure becomes "no server running":
// tmux named, the engine never mentioned. That silence is exactly how an MCP
// daemon under systemd's default PATH failed to spawn `claude` on a live box.
func preflightBinary(binary string) error {
	if binary == "" {
		return nil
	}
	if strings.Contains(binary, "/") {
		info, err := os.Stat(binary)
		if err != nil {
			return fmt.Errorf(
				"engine binary %s is not on disk: %w — fix the configured <engine>.binary path",
				binary, err,
			)
		}
		if info.IsDir() || info.Mode()&0o111 == 0 {
			return fmt.Errorf("engine binary %s is not an executable file", binary)
		}
		return nil
	}
	if _, err := obs.Runner(deps.RealRunner{}).LookPath(binary); err != nil {
		return fmt.Errorf(
			"engine binary %q is not reachable from this process's PATH: %w — a systemd user service starts with systemd's default PATH; pin an absolute <engine>.binary in the machine config or extend the unit's Environment=PATH",
			binary,
			err,
		)
	}
	return nil
}

// NewSession is the ONE chat-server creator: spawn.Run, the Claude launcher,
// the picker (action.TmuxExecutor.CreateChatServer) and the shell shim
// (`pfm internal chat-server`) all create a chat's server here, detached, and
// give it pfmconfig.ChatServerOptions — the list name-sync converges live
// servers onto. A second creator is a server born without that list: no title
// policy, and a window its pane command renames out from under the fleet.
//
// A zero Width or Height states no size, so the first client to attach sizes
// the window; tmux refuses `-x 0`.
func (tmux TmuxSpawner) NewSession(
	ctx context.Context,
	spec SessionSpec,
) (returnErr error) {
	if err := preflightBinary(spec.Binary); err != nil {
		return err
	}
	if err := paths.EnsureTmuxDir(tmux.TmuxDir); err != nil {
		return err
	}
	account, err := claudelaunch.ConfigDirFromRun(spec.Run)
	if err != nil {
		return err
	}
	guard, err := gather.AcquireAccountGuard(account, true)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, guard.Close()) }()
	started := false
	defer func() {
		if !started {
			returnErr = errors.Join(returnErr, guard.Abort())
		}
	}()
	arguments := append(paths.TmuxConfigArguments(),
		"new-session", "-d",
		"-s", spec.Session,
		"-n", spec.Window,
		"-c", spec.CWD,
	)
	if spec.Width > 0 && spec.Height > 0 {
		arguments = append(arguments, "-x", strconv.Itoa(spec.Width), "-y", strconv.Itoa(spec.Height))
	}
	// A run past one tmux client message (a Codex role seat's launch line
	// carries the whole composed fleet prompt) goes through a one-shot
	// script in the socket directory: private, 0700, secured just above, and
	// living exactly as long as the servers whose panes it launches.
	launch, err := pfmtmux.PrepareLaunch(tmux.TmuxDir, spec.Run)
	if err != nil {
		return fmt.Errorf("create chat server (%s): %w", runShape(spec), err)
	}
	arguments = append(arguments, launch.Command)
	// An exact chat name rides in the same tmux command as the session, so the
	// Herdr reporter reads it from the first client attach; a name no user
	// chose is marked automatic too, and an unnamed chat sets nothing.
	switch {
	case spec.ChatName != "" && spec.ChatNameAutomatic:
		arguments = append(append(arguments, ";"), gather.AutomaticChatNameCommand(spec.Session, spec.ChatName)...)
	case spec.ChatName != "":
		arguments = append(append(arguments, ";"), gather.ChatNameCommand(spec.Session, spec.ChatName)...)
	}
	command, err := tmux.newSessionCommand(
		ctx,
		spec.Socket,
		arguments...,
	)
	if err != nil {
		return errors.Join(fmt.Errorf("create chat server: %w", err), launch.Discard())
	}
	// A failed client call may already have created its server. Retain the
	// pending claim unless a fresh cleanup call proves it terminated.
	started = guard != nil
	if output, err := command.CombinedOutput(); err != nil {
		createErr := fmt.Errorf("create chat server: %w: %s", err, output)
		if ctxErr := ctx.Err(); ctxErr != nil {
			createErr = fmt.Errorf("create chat server: %w", ctxErr)
		}
		return errors.Join(tmux.terminateUnrecorded(ctx, guard, spec.Socket, createErr), launch.Discard())
	}
	started = true
	if guard != nil {
		// A freshly born pane can answer once with nothing: ask again before ending the server.
		var (
			queryErr, parseErr error
			pid                int
		)
		for attempt := 0; attempt < 2; attempt++ {
			var output []byte
			output, queryErr = tmux.command(
				ctx, spec.Socket, "display-message", "-p", "-t", spec.Session+":", "#{pane_pid}",
			).Output()
			pid, parseErr = strconv.Atoi(strings.TrimSpace(string(output)))
			if queryErr == nil && parseErr == nil || ctx.Err() != nil {
				break
			}
		}
		recordErr := queryErr
		if recordErr == nil && parseErr != nil {
			recordErr = fmt.Errorf("read account launch pane pid: %w", parseErr)
		}
		if recordErr == nil {
			recordErr = guard.Record(pid)
		}
		if recordErr != nil {
			// A cancelled query returns as the cancellation, as at creation and
			// configuration: the killed query alone says only "signal: killed".
			// A record failure keeps its own error.
			if ctxErr := ctx.Err(); queryErr != nil && ctxErr != nil {
				recordErr = fmt.Errorf("read account launch pane pid: %w", ctxErr)
			}
			return tmux.terminateUnrecorded(ctx, guard, spec.Socket, recordErr)
		}
	}
	for _, options := range pfmconfig.ChatServerOptions(tmux.Titles, tmux.pfmBinary()) {
		if output, err := tmux.command(
			ctx,
			spec.Socket,
			options...,
		).CombinedOutput(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return &SessionCreatedError{Err: fmt.Errorf("configure chat server: %w", ctxErr)}
			}
			// A server that vanished between creation and configuration died
			// with its only pane — name the pane command's SHAPE, because
			// that is where the death almost always started. spec.Run can
			// carry a prompt body (action.HeadlessRun appends it to the
			// launch line), so the error names the binary and word count,
			// never the command line itself. Cancellation returns as itself.
			return &SessionCreatedError{Err: fmt.Errorf(
				"configure chat server: %w: %s — the server died before it could be configured; its pane command likely exited at launch (%s)",
				err,
				output,
				runShape(spec),
			)}
		}
	}
	tmux.themeBornServer(ctx, spec.Socket)
	return nil
}

// themeBornServer puts the pfm theme on the server just created, when
// `pfm theme` has the sync on. The theme is cosmetic, so a failure is logged
// and the spawn stands: a chat must open even when its colours could not be set.
func (tmux TmuxSpawner) themeBornServer(ctx context.Context, socket string) {
	home, err := tmux.home()
	if err != nil {
		obs.Logger(ctx).Warn("tmux theme at birth skipped: no home", "socket", socket, "err", err)
		return
	}
	apply := tmux.applyTheme
	if apply == nil {
		apply = themesync.ApplyTmuxServer
	}
	if err := apply(ctx, themetmux.AtDir(tmux.TmuxDir), themesync.DefaultStateDir(home), socket); err != nil {
		obs.Logger(ctx).Warn("tmux theme at birth failed", "socket", socket, "err", err)
	}
}

// terminateUnrecorded keeps protection if the child could still be alive.
func (tmux TmuxSpawner) terminateUnrecorded(
	ctx context.Context,
	guard *gather.AccountGuard, socket string, launchErr error,
) error {
	if guard == nil {
		return &SessionCreatedError{Err: launchErr}
	}
	// A canceled launch still owns its child; cleanup outlives its cancel.
	if err := tmux.command(context.WithoutCancel(ctx), socket, "kill-server").Run(); err != nil {
		return &SessionCreatedError{
			Err: errors.Join(launchErr, fmt.Errorf("terminate unrecorded chat server: %w", err)),
		}
	}
	return errors.Join(launchErr, guard.Abort())
}

// runShape is the pane command's SHAPE for an error message: the binary's
// base name and how many whitespace-separated words the launch line carries
// — the same "argv, never argv content" law obs/runner.go's argvShape
// applies to every logged process door. spec.Run itself never reaches an
// error string, because it can carry a prompt body.
func runShape(spec SessionSpec) string {
	binary := spec.Binary
	if binary == "" {
		if fields := strings.Fields(spec.Run); len(fields) > 0 {
			binary = fields[0]
		}
	}
	name := "?"
	if binary != "" {
		name = filepath.Base(binary)
	}
	return fmt.Sprintf("%s argc=%d", name, len(strings.Fields(spec.Run)))
}

func (tmux TmuxSpawner) newSessionCommand(
	ctx context.Context,
	socket string,
	arguments ...string,
) (*pfmtmux.Cmd, error) {
	binary, commandArguments, environment := pfmtmux.Invocation(
		tmux.Binary,
		filepath.Join(tmux.TmuxDir, socket),
		arguments...)
	// The chat lives in its own pane: the caller's Herdr pane identity would attribute it to the
	// caller (an attach re-copies the attaching client's through update-environment), and a Codex
	// tool shell's injections would start it monochrome and pager-less. tmux sets TMUX and
	// TMUX_PANE in the pane itself, so the caller's never reach it.
	environment = claudelaunch.WithoutCodexToolShell(herdr.StripPane(environment))
	env := tmux.Env
	if env == nil {
		env = paths.OSEnv{}
	}
	kind := tmux.serviceKind
	if kind == "" {
		kind = servicemanager.Native()
	}
	lookPath := tmux.lookPath
	if lookPath == nil {
		lookPath = obs.Runner(deps.RealRunner{}).LookPath
	}
	command, err := chatServerCommand(ctx, kind, lookPath, binary, commandArguments, environment, env)
	if err != nil {
		return nil, err
	}
	return pfmtmux.Observe(ctx, command, arguments...), nil
}

// Pane is the door to pane on socket: the socket's path under TmuxDir on the
// tmux backend, read and typed through Screen — or, when no Screen is set, the
// call engine over this spawner's tmux binary, the one place that decides how
// a pane is captured, keyed and pasted into.
func (tmux TmuxSpawner) Pane(socket, pane string) (pfmcall.Screen, pfmcall.Target, error) {
	target := pfmcall.Target{SocketPath: filepath.Join(tmux.TmuxDir, socket), Pane: pane}
	if tmux.Screen != nil {
		return tmux.Screen, target, nil
	}
	engine, err := pfmcall.New(pfmcall.Dependencies{Tmux: pfmcall.TmuxCaller{Binary: tmux.Binary}, Env: tmux.Env})
	if err != nil {
		return nil, pfmcall.Target{}, fmt.Errorf("open the pane door for socket %s: %w", socket, err)
	}
	return engine, target, nil
}

// socket is the one tmux-addressing wrapper (internal/tmux.Socket).
func (tmux TmuxSpawner) socket() pfmtmux.Socket {
	return pfmtmux.Socket{Binary: tmux.Binary, Dir: tmux.TmuxDir}
}

func (tmux TmuxSpawner) command(
	ctx context.Context,
	socket string,
	arguments ...string,
) *pfmtmux.Cmd {
	return tmux.socket().Command(ctx, socket, arguments...)
}

// pfmBinary is the installed pfm launcher the chat server's client-attached
// hook runs, the same path every launch-time hook uses.
func (tmux TmuxSpawner) pfmBinary() string {
	home, err := tmux.home()
	if err != nil {
		return "pfm"
	}
	return filepath.Join(home, ".local", "bin", "pfm")
}

// home is the user's home directory the spawner's environment resolves.
func (tmux TmuxSpawner) home() (string, error) {
	env := tmux.Env
	if env == nil {
		env = paths.OSEnv{}
	}
	return paths.HomeFrom(env)
}
