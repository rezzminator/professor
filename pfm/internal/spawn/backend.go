package spawn

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/place"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

const (
	BackendAuto  = "auto"
	BackendTmux  = "tmux"
	BackendHerdr = "herdr"
)

// BackendChoice is the immutable birth location chosen for a chat. A Herdr
// choice names the caller's socket, pane and workspace: an in-place seat takes
// over that pane, while a new tab is born in its place's workspace, which
// SpawnHerdr finds from the fleet Places scans; only a new tab scans.
type BackendChoice struct {
	Backend, HerdrSocket, HerdrPane, HerdrWorkspace string
	InPlace                                         bool
	Places                                          func(context.Context) (*place.Fleet, error)
}

// ErrNoCaller is the caller error of a door that opens for no terminal (a
// timer, a daemon, an MCP open with no carried caller): its chat is placed by
// its subject and the Herdr server, never by the process that runs the door.
var ErrNoCaller = errors.New("no caller: placed by the chat's own host")

// ErrCallerUnproven is a caller pfm looked for and could not prove: an MCP
// request with no usable caller identity, an engine's shell or a Herdr pane
// claim that no live pane holds. It never reads as "outside Herdr": its chat
// is hosted in Herdr whenever a Herdr server runs.
var ErrCallerUnproven = errors.New("caller unproven")

// ErrHostRefused is every refusal of the placement door and of a tmux client
// in a Herdr pane; the CLI and the MCP door answer it with call.CodeHostRefused.
var ErrHostRefused = errors.New("host refused")

// Refusal is the one wording of a refusal: the door, then the reason.
func Refusal(door, format string, args ...any) error {
	return fmt.Errorf("%w at %s: "+format, append([]any{ErrHostRefused, door}, args...)...)
}

// FailureCode is a failed placement's exit code: call.CodeHostRefused for a
// refusal, 1 for any other failure.
func FailureCode(err error) int {
	if errors.Is(err, ErrHostRefused) {
		return pfmcall.CodeHostRefused
	}
	return 1
}

// ProvenCaller reads a local caller's lookup against its environment: a
// process whose environment names a Herdr pane its lookup did not prove, or an
// engine's shell that no pane holds, is unproven, never "outside Herdr".
func ProvenCaller(environment resolve.WhoamiEnvironment, caller resolve.Identity, err error) error {
	switch {
	case environment.HerdrPane != "" && caller.Backend != BackendHerdr:
		return fmt.Errorf("%w: its environment names Herdr pane %s, which its lookup did not prove (%v)",
			ErrCallerUnproven, environment.HerdrPane, err)
	case errors.Is(err, resolve.ErrNotInChat) && caller.ID != "":
		return fmt.Errorf("%w: %s session %s runs in no pane pfm can prove (%v)",
			ErrCallerUnproven, caller.Engine, caller.ID, err)
	}
	return err
}

// RefuseTmuxClient refuses to start a tmux client in this terminal when its
// environment names a Herdr pane: a tmux chat is opened from a plain terminal.
func RefuseTmuxClient(door string, environment resolve.WhoamiEnvironment) error {
	if environment.HerdrPane == "" {
		return nil
	}
	return Refusal(door, "this terminal is Herdr pane %s, where a tmux client is never attached; "+
		"open the chat from a plain terminal", environment.HerdrPane)
}

// HerdrServer is the Herdr server a birth with no Herdr caller is hosted on:
// the socket `herdr` itself serves from this environment (SocketErr when it
// cannot be resolved) and the read of whether a server answers there.
type HerdrServer struct {
	Socket    string
	SocketErr error
	Up        func(ctx context.Context, socket string) error
}

// HerdrServerFrom reads the Herdr server of env (paths.HerdrServerSocket),
// probed by herdr.ServerUp.
func HerdrServerFrom(env paths.Env) HerdrServer {
	socket, err := paths.HerdrServerSocket(env)
	return HerdrServer{Socket: socket, SocketErr: err, Up: herdr.ServerUp}
}

// ownsTerminal names the doors that launch a chat into the terminal a person
// sits at (the picker and `pfm chat open`'s attach, a typed claude, a typed
// codex): outside Herdr that terminal's own chat is hosted in tmux, never
// spawned into Herdr.
func ownsTerminal(door string) bool {
	return door == "picker" || door == "hook.launch" || door == "shim.cx"
}

// provenOutside is a caller proven outside every Herdr pane: a tmux pane, or
// a process no pane holds.
func provenOutside(caller resolve.Identity, callerErr error) bool {
	if caller.Backend == BackendHerdr || errors.Is(callerErr, ErrNoCaller) || errors.Is(callerErr, ErrCallerUnproven) {
		return false
	}
	return callerErr == nil || errors.Is(callerErr, resolve.ErrNotInChat) ||
		errors.Is(callerErr, resolve.ErrNotRegistered)
}

// Place chooses a birth by the owner's rule: tmux asked for in words is tmux,
// from anywhere; a caller in a Herdr pane places the chat there; a launch that
// owns a terminal proven outside Herdr stays in that terminal on tmux; every
// other birth goes to Herdr when its server runs (the chat's recorded Herdr
// host first, else server), whoever the caller is, and to tmux only when no
// server runs there. A Herdr server pfm cannot read is an ErrHostRefused,
// never tmux. A Herdr choice leaves Places for the door to fill; one with no
// caller pane is a new tab its place's workspace takes.
func Place(
	ctx context.Context,
	door, requested, recorded string,
	revive bool,
	caller resolve.Identity,
	callerErr error,
	server HerdrServer,
) (choice BackendChoice, err error) {
	if requested == "" {
		requested = BackendAuto
	}
	read := "" // the Herdr server read, "" when none was needed
	defer func() {
		callerError := ""
		if callerErr != nil {
			callerError = callerErr.Error()
		}
		fields := []any{
			"door", door, "requested", requested, "caller_backend", caller.Backend,
			"caller_socket", caller.SocketPath, "caller_pane", caller.Pane, "caller_workspace", caller.HerdrWorkspace,
			"caller_fronted", caller.Fronted, "caller_source", caller.Source, "caller_err", callerError,
			"recorded_socket", recorded, "revive", revive, "herdr_server", read,
			"chosen", choice.Backend, "in_place", choice.InPlace,
		}
		if err != nil {
			fields = append(fields, "place_err", err.Error())
		}
		obs.Logger(ctx).InfoContext(ctx, "chat.placement", fields...)
	}()
	if requested != BackendAuto && requested != BackendTmux && requested != BackendHerdr {
		return BackendChoice{}, fmt.Errorf("backend %q: want auto, tmux or herdr", requested)
	}
	if requested == BackendTmux {
		// Asked for in words: born detached, from anywhere.
		return BackendChoice{Backend: BackendTmux}, nil
	}
	if caller.Backend == BackendHerdr && (callerErr == nil || errors.Is(callerErr, resolve.ErrNotRegistered)) {
		if caller.SocketPath == "" || caller.Pane == "" || caller.HerdrWorkspace == "" {
			return BackendChoice{}, Refusal(door, "the caller has an incomplete Herdr location")
		}
		return BackendChoice{
			Backend:        BackendHerdr,
			HerdrSocket:    caller.SocketPath,
			HerdrPane:      caller.Pane,
			HerdrWorkspace: caller.HerdrWorkspace,
			InPlace:        errors.Is(callerErr, resolve.ErrNotRegistered) && caller.Fronted,
		}, nil
	}
	if requested == BackendAuto && ownsTerminal(door) && provenOutside(caller, callerErr) {
		// The terminal a person sits at outside Herdr keeps its own chat.
		return BackendChoice{Backend: BackendTmux}, nil
	}
	sockets := []string{recorded, server.Socket}
	if recorded == "" || recorded == server.Socket {
		sockets = sockets[1:]
	}
	if err := ctx.Err(); err != nil {
		return BackendChoice{}, fmt.Errorf("place the chat: %w", err)
	}
	var downs []error
	for _, socket := range sockets {
		if socket == server.Socket && server.SocketErr != nil {
			read = server.SocketErr.Error()
			return BackendChoice{}, Refusal(door, "cannot tell whether Herdr runs: %w", server.SocketErr)
		}
		upErr := server.Up(ctx, socket)
		switch {
		case upErr == nil:
			read = "running at " + socket
			return BackendChoice{Backend: BackendHerdr, HerdrSocket: socket}, nil
		case errors.Is(upErr, herdr.ErrServerDown):
			downs = append(downs, upErr)
		default:
			read = upErr.Error()
			return BackendChoice{}, Refusal(door, "cannot tell whether Herdr runs: %w", upErr)
		}
	}
	down := errors.Join(downs...)
	read = "down: " + strings.ReplaceAll(down.Error(), "\n", "; ")
	if requested == BackendHerdr && !revive {
		// A new chat asked into Herdr cannot be; a stored chat a revive wakes
		// resumes in tmux, as every revive does while no Herdr server runs.
		return BackendChoice{}, Refusal(door, "herdr backend asked for, but %s",
			strings.ReplaceAll(down.Error(), "\n", "; "))
	}
	// No Herdr server runs: tmux is legal.
	return BackendChoice{Backend: BackendTmux}, nil
}

// lookPath resolves the request's non-Claude native engine binary to an absolute
// path through its Runner; a nil Runner is the real one.
func (request HerdrRequest) lookPath() (string, error) {
	runner := request.Runner
	if runner == nil {
		runner = deps.RealRunner{}
	}
	binary, err := runner.LookPath(request.Run.Binary)
	if err != nil {
		return "", err
	}
	return filepath.Abs(binary)
}
