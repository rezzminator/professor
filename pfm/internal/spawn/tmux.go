package spawn

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

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
	// Env is the host-environment seam (pfm/TESTPLAN.md § Seams, paths.Env)
	// serviceScopeCommand reads INVOCATION_ID through; nil defaults to
	// paths.OSEnv{}.
	Env paths.Env
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
) error {
	if err := preflightBinary(spec.Binary); err != nil {
		return err
	}
	if err := paths.EnsureTmuxDir(tmux.TmuxDir); err != nil {
		return err
	}
	arguments := append(paths.TmuxConfigArguments(),
		"new-session", "-d",
		"-s", spec.Session,
		"-n", spec.Window,
		"-c", spec.CWD,
	)
	if spec.Width > 0 && spec.Height > 0 {
		arguments = append(arguments, "-x", strconv.Itoa(spec.Width), "-y", strconv.Itoa(spec.Height))
	}
	arguments = append(arguments, spec.Run)
	command, err := tmux.newSessionCommand(
		ctx,
		spec.Socket,
		arguments...,
	)
	if err != nil {
		return fmt.Errorf("create chat server: %w", err)
	}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("create chat server: %w: %s", err, output)
	}
	for _, options := range pfmconfig.ChatServerOptions(tmux.Titles) {
		if output, err := tmux.command(
			ctx,
			spec.Socket,
			options...,
		).CombinedOutput(); err != nil {
			// A server that vanished between creation and configuration died
			// with its only pane — name the pane command's SHAPE, because
			// that is where the death almost always started. spec.Run can
			// carry a prompt body (action.HeadlessRun appends it to the
			// launch line), so the error names the binary and word count,
			// never the command line itself.
			return fmt.Errorf(
				"configure chat server: %w: %s — the server died before it could be configured; its pane command likely exited at launch (%s)",
				err,
				output,
				runShape(spec),
			)
		}
	}
	return nil
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
	env := tmux.Env
	if env == nil {
		env = paths.OSEnv{}
	}
	command, err := serviceScopeCommand(ctx, binary, commandArguments, environment, env)
	if err != nil {
		return nil, err
	}
	return pfmtmux.Observe(ctx, command, arguments...), nil
}

func (tmux TmuxSpawner) Capture(
	ctx context.Context,
	socket, target string,
) (string, error) {
	output, err := tmux.command(
		ctx,
		socket,
		"capture-pane", "-t", target, "-p", "-J",
	).Output()
	return string(output), err
}

func (tmux TmuxSpawner) SendLiteral(
	ctx context.Context,
	socket, target, text string,
) error {
	return tmux.command(
		ctx,
		socket,
		"send-keys", "-t", target, "-l", "--", text,
	).Run()
}

func (tmux TmuxSpawner) SendKey(
	ctx context.Context,
	socket, target, key string,
) error {
	return tmux.command(ctx, socket, "send-keys", "-t", target, key).Run()
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
