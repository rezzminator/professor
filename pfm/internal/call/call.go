// Package call implements guarded, socket-scoped live chat delivery.
//
// Entry points: Engine.Call and Engine.CallTarget deliver a message (engine.go), Engine.DeliverThen rides
// out a turn before each --then steer (then.go), Engine.PrepareForResume builds a dormant chat's resume body
// (body.go), and Engine.ShareSender resolves one command's sender once (here).
//
// Files, one line each:
//   - engine.go is the Engine: target resolution, the guarded delivery sequence and the sender's signature.
//   - types.go declares the Request, Result, Target, Sender and Options types and the receipt codes.
//   - refusal.go words each refusal of a chat function once, as both its CLI and its MCP door answer it.
//   - body.go prepares the exact text a pane or a dormant transcript receives, a long body spilled to a file.
//   - guards.go holds the message guards: harness and /compact commands, the composer and the exit dialog.
//   - composer_wait.go waits for a booting chat's composer before anything is typed, and cuts a capture to its composer.
//   - agentpanel.go reads Claude Code's agents panel holding keyboard focus.
//   - trust_dialog.go refuses a pane held at Claude Code's folder-trust dialog.
//   - modal.go selects a modal's choice through the target's backend-aware key door.
//   - screen.go is the Screen door other packages press keys in, type into and read a pane through.
//   - settled.go is the one wait for the turn a pane is running now to be over.
//   - then.go, armed.go and announce.go run the --then chain: its waiter, its armed record, its first log line.
//   - lock.go is the per-pane call lock, named as chat.sh names it.
//   - tmux.go invokes tmux against an explicit socket path.
//   - herdr.go, herdr_delivery.go and herdr_proof.go deliver into a Herdr pane and prove its turn and supervisor.
//   - herdr_keys.go, herdr_opencode.go and herdr_startup.go hold Herdr's named keys, OpenCode's composer there
//     and the wait for a reserved pane's address.
//   - herdr_bridge.go proves a pfm tmux chat client in a Herdr pane's foreground.
//   - env.go reads the CHAT_CALL_* and CHAT_THEN_* knobs; obs.go records a coordinator's state trail.
package call

import "context"

// unsignedKey marks a context resolving the sender of a message pfm never signs.
type unsignedKey struct{}

// ShareSender resolves this process's sender once and pins it in the context it returns, so every delivery
// and resume body a command makes under it signs as one sender and reports a failed identity once. A message
// pfm never signs (a harness command, or any message with signatures disabled) reports none.
func (engine *Engine) ShareSender(ctx context.Context, message string) context.Context {
	if isHarnessCommand(message) || engine.options.DisableSignature {
		ctx = context.WithValue(ctx, unsignedKey{}, true)
	}
	return withSender(ctx, engine.sender(ctx))
}

// senderWarnf reports a failed sender identity, unless it was resolved for a message pfm never signs.
func (engine *Engine) senderWarnf(ctx context.Context, format string, args ...any) {
	if unsigned, _ := ctx.Value(unsignedKey{}).(bool); !unsigned {
		engine.warnf(format, args...)
	}
}
