package tmux

import (
	"context"
	"fmt"
	"sync"
)

// FakeCall is one Fake invocation recorded on its ledger: the socket, the
// subcommand (the first argument — "capture-pane", "list-sessions", ...)
// and the full argument list Run was given.
type FakeCall struct {
	Socket     string
	Subcommand string
	Args       []string
}

// FakeResult is what a scripted tmux subcommand answers with: stdout
// exactly the way FormatSplit expects a -F line to look, or an error — a
// missing server, a dead socket, tmux itself absent (CouldNotRun-shaped).
type FakeResult struct {
	Stdout []byte
	Err    error
}

// Fake scripts the tmux façade's command surface — capture-pane,
// list-sessions/list-windows/list-panes (-F), send-keys, display-message,
// respawn-pane and kill-server — the subcommands reload, inject, gather and
// statusline drive a real tmux server on a chat's socket through in
// production. It records every call on a ledger for assertions; the real
// façade (Command, Invocation, FormatSplit/Join, CouldNotRun) is unchanged
// by this file. A subcommand or socket with nothing scripted answers
// ErrUnscripted — never a silent empty result — the same discipline
// deps.FakeRunner holds for an unscripted argv. The zero value is ready to
// script, the same zero-value-usable shape deps.FakeRunner already takes —
// one Fake idiom, not two.
type Fake struct {
	mu      sync.Mutex
	scripts map[string]FakeResult
	calls   []FakeCall
}

// Script registers result for every call to subcommand ("capture-pane",
// "list-sessions", "list-windows", "list-panes", "send-keys",
// "display-message", "respawn-pane", "kill-server"), on any socket.
func (f *Fake) Script(subcommand string, result FakeResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setScriptLocked(scriptKey(subcommand, ""), result)
}

// ScriptFor registers result for subcommand calls made against socket
// specifically — a fixture with per-server variance (one socket healthy,
// one stale) scripts here instead of with Script, and a socket-specific
// script always wins over a subcommand-wide one.
func (f *Fake) ScriptFor(subcommand, socket string, result FakeResult) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setScriptLocked(scriptKey(subcommand, socket), result)
}

// setScriptLocked lazily allocates the scripts map — a zero-value Fake
// (&Fake{}, no constructor needed) is ready to script.
func (f *Fake) setScriptLocked(key string, result FakeResult) {
	if f.scripts == nil {
		f.scripts = map[string]FakeResult{}
	}
	f.scripts[key] = result
}

func scriptKey(subcommand, socket string) string { return subcommand + "\x00" + socket }

// Run answers args on socket the way a real tmux -S socket <args...> would,
// from whatever was scripted, and records the call on the ledger first —
// so a call that turns out unscripted still counts as a call for
// assertions like Calls().
func (f *Fake) Run(_ context.Context, socket string, args ...string) ([]byte, error) {
	var subcommand string
	if len(args) > 0 {
		subcommand = args[0]
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, FakeCall{Socket: socket, Subcommand: subcommand, Args: append([]string(nil), args...)})
	if result, ok := f.scripts[scriptKey(subcommand, socket)]; ok {
		return result.Stdout, result.Err
	}
	if result, ok := f.scripts[scriptKey(subcommand, "")]; ok {
		return result.Stdout, result.Err
	}
	return nil, ErrUnscripted{Subcommand: subcommand, Socket: socket, Args: append([]string(nil), args...)}
}

// CapturePane runs "capture-pane" plus args on socket.
func (f *Fake) CapturePane(ctx context.Context, socket string, args ...string) ([]byte, error) {
	return f.Run(ctx, socket, append([]string{"capture-pane"}, args...)...)
}

// ListSessions runs "list-sessions" plus args (typically "-F", format) on socket.
func (f *Fake) ListSessions(ctx context.Context, socket string, args ...string) ([]byte, error) {
	return f.Run(ctx, socket, append([]string{"list-sessions"}, args...)...)
}

// ListWindows runs "list-windows" plus args on socket.
func (f *Fake) ListWindows(ctx context.Context, socket string, args ...string) ([]byte, error) {
	return f.Run(ctx, socket, append([]string{"list-windows"}, args...)...)
}

// ListPanes runs "list-panes" plus args on socket.
func (f *Fake) ListPanes(ctx context.Context, socket string, args ...string) ([]byte, error) {
	return f.Run(ctx, socket, append([]string{"list-panes"}, args...)...)
}

// SendKeys runs "send-keys" plus args on socket.
func (f *Fake) SendKeys(ctx context.Context, socket string, args ...string) ([]byte, error) {
	return f.Run(ctx, socket, append([]string{"send-keys"}, args...)...)
}

// DisplayMessage runs "display-message" plus args on socket.
func (f *Fake) DisplayMessage(ctx context.Context, socket string, args ...string) ([]byte, error) {
	return f.Run(ctx, socket, append([]string{"display-message"}, args...)...)
}

// RespawnPane runs "respawn-pane" plus args on socket.
func (f *Fake) RespawnPane(ctx context.Context, socket string, args ...string) ([]byte, error) {
	return f.Run(ctx, socket, append([]string{"respawn-pane"}, args...)...)
}

// KillServer runs "kill-server" on socket.
func (f *Fake) KillServer(ctx context.Context, socket string) ([]byte, error) {
	return f.Run(ctx, socket, "kill-server")
}

// Calls returns every call Run recorded, in call order.
func (f *Fake) Calls() []FakeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := make([]FakeCall, len(f.calls))
	for index, call := range f.calls {
		calls[index] = FakeCall{
			Socket:     call.Socket,
			Subcommand: call.Subcommand,
			Args:       append([]string(nil), call.Args...),
		}
	}
	return calls
}

// ErrUnscripted names the subcommand, socket and args a Fake received with
// nothing registered for either.
type ErrUnscripted struct {
	Subcommand string
	Socket     string
	Args       []string
}

func (err ErrUnscripted) Error() string {
	return fmt.Sprintf("tmux: Fake: unscripted %q call for socket %q (args %q)", err.Subcommand, err.Socket, err.Args)
}
