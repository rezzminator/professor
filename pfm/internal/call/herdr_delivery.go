package call

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
)

func (engine *Engine) callHerdr(
	ctx context.Context,
	request Request,
	target Target,
	base Result,
	lock *targetLock,
) (Result, error) {
	if target.SocketPath == "" || target.Pane == "" {
		base.Code = CodeUndelivered
		base.Message = "ABORT: Herdr call requires an explicit socket and pane; nothing was typed"
		return base, nil
	}
	id, err := pfmengine.Parse(target.Engine)
	if err != nil {
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf(
			"ABORT: unsupported Herdr call engine %q",
			target.Engine,
		)
		return base, nil
	}
	if !pfmengine.MustLookup(id).HerdrNativePrompt {
		return engine.callHerdrRaw(ctx, request, target, base, lock, id)
	}
	target, agent, screen, err := engine.awaitHerdrNative(ctx, target, &base, lock)
	if err != nil {
		var unsent *herdr.InputNotSentError
		if errors.As(err, &unsent) {
			base.Status, base.Code = statusUndelivered, CodeUndelivered
			base.Message = fmt.Sprintf("ABORT: Herdr ownership could not be proved; nothing was typed: %v", err)
			return base, nil
		}
		if errors.Is(err, ErrStartupSplash) {
			if agent.Agent.Status != herdrUnknown {
				base.Status = statusUndelivered
			}
			base.Code = CodeUndelivered
			base.Message = fmt.Sprintf(
				"ABORT: native startup is unknown or has no composer; nothing was typed: %v",
				err,
			)
			return base, nil
		}
		return base, err
	}
	base.SocketPath, base.Pane = target.SocketPath, target.Pane
	if base.Code != 0 {
		return base, nil
	}
	switch agent.Agent.Status {
	case herdrWorking:
		if request.RequireIdle {
			base.Busy, base.Code = true, CodeBusy
			base.Message = "native agent is working; idle-only retry typed nothing"
			return base, nil
		}
		// agent.prompt admits idle agents only; a working agent owns a safe
		// composer queue, which only an explicit force_now may interrupt.
		base.Busy = true
		return engine.callHerdrRaw(ctx, request, target, base, lock, id)
	case herdrBlocked:
		base.Code = CodeUndelivered
		base.Message = "ABORT: target pane is blocked; nothing was typed"
		return base, nil
	}
	if IsFooterBusy(id, screen) {
		base.Busy = true
		return engine.callHerdrRaw(ctx, request, target, base, lock, id)
	}
	empty, err := herdrComposerEmpty(ctx, target, id, screen)
	if err != nil || !empty {
		base.Status, base.Code = statusUndelivered, CodeUndelivered
		base.Message = "ABORT: native composer is not empty and ready; nothing was typed, existing draft/menu preserved"
		if err != nil {
			base.Message = fmt.Sprintf(
				"ABORT: native composer styling could not be checked; nothing was typed: %v",
				err,
			)
		}
		return base, nil
	}
	prepared, err := engine.prepareLiveMessage(ctx, target, request.Message, false)
	if err != nil {
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf("prepare Herdr call: %v", err)
		return base, nil
	}
	base.Unsigned = prepared.Unsigned
	base.AutoFilePath = prepared.AutoFilePath
	target, err = ProveHerdrTarget(ctx, target)
	if err != nil {
		base.Status, base.Code = statusUndelivered, CodeUndelivered
		base.Message = fmt.Sprintf("ABORT: Herdr ownership could not be proved; nothing was typed: %v", err)
		return base, nil
	}
	base.SocketPath, base.Pane = target.SocketPath, target.Pane
	result, err := herdr.SeatRequest(ctx, target.SocketPath, "agent.prompt", struct {
		Target string `json:"target"`
		Text   string `json:"text"`
	}{target.Pane, prepared.Message})
	// Once prompt was attempted, neither a timeout nor a malformed response may
	// authorize a retry: the producer may already have submitted the message.
	base.Typed = true
	if err != nil {
		base.Typed = !herdrInputUnsent(err)
		base.Status = statusTypedUnconfirmed
		if !base.Typed {
			base.Status = statusUndelivered
		}
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf("herdr agent.prompt for %q unconfirmed: %v", target.Pane, err)
		if !base.Typed {
			base.Message = fmt.Sprintf(
				"herdr agent.prompt for %q refused before input; nothing was typed: %v",
				target.Pane,
				err,
			)
		}
		return base, nil
	}
	prompted, err := decodeHerdrAgent(result, "agent_prompted", target)
	if err != nil {
		base.Status = statusTypedUnconfirmed
		base.Code = CodeUndelivered
		base.Message = err.Error()
		return base, nil
	}
	// A syntactically valid acknowledgement can still carry the old idle
	// snapshot. Only this agent working, or a new completion event, proves
	// that a turn started; idle state-change sequence alone is not a turn.
	boundary := agent.Agent.StateChangeSeq
	if agent.Agent.CompletionSeq != nil && *agent.Agent.CompletionSeq > boundary {
		boundary = *agent.Agent.CompletionSeq
	}
	started := prompted.Agent.Status == herdrWorking ||
		prompted.Agent.CompletionSeq != nil && *prompted.Agent.CompletionSeq > boundary
	turn := &herdrTurn{
		SocketPath: target.SocketPath, Pane: target.Pane,
		StateChangeSeq: prompted.Agent.StateChangeSeq, CompletionSeq: prompted.Agent.CompletionSeq,
		Terminal: prompted.Agent.Terminal, AgentSession: prompted.Agent.AgentSession,
	}
	base.Status = statusDelivered
	base.Message = fmt.Sprintf("called LIVE into %q — Herdr agent_prompted", target.Pane)
	if !started {
		base.Status = statusDeliveredUnproven
		base.Message += " — acknowledged input but proved no started turn"
	}
	for _, warning := range prepared.Warnings {
		base.Message += " — WARNING: " + warning
	}
	if len(request.Then) > 0 {
		lock.release()
		if engine.armSteers(ctx, request, target, &base, turn) {
			base.Message += fmt.Sprintf(" — %d then steer(s) queued (log: %s)", base.Steers, base.SteerLog)
		}
	}
	return base, nil
}

func (engine *Engine) callHerdrRaw(
	ctx context.Context,
	request Request,
	target Target,
	base Result,
	lock *targetLock,
	id pfmengine.ID,
) (Result, error) {
	before, err := herdrRawCapture(ctx, target, "")
	if err != nil {
		base.Code, base.Message = CodeUndelivered, fmt.Sprintf(
			"ABORT: Herdr composer could not be checked; nothing was typed: %v",
			err,
		)
		return base, nil
	}
	base.Busy = base.Busy || IsFooterBusy(id, before)
	busy := base.Busy
	if request.RequireIdle && busy {
		base.Code, base.Message = CodeBusy, "target is busy; idle-only retry typed nothing"
		return base, nil
	}
	for attempt := 0; ; attempt++ {
		// Check before Escape as well as after each redraw: force_now must
		// never discard or type over an existing draft, menu or blocked UI.
		empty, checkErr := herdrComposerEmpty(ctx, target, id, before)
		if checkErr != nil || !empty {
			base.Status, base.Code = statusUndelivered, CodeUndelivered
			base.Message = "ABORT: Herdr composer is not empty and ready; nothing was typed, existing draft/menu preserved"
			if checkErr != nil {
				base.Message = fmt.Sprintf(
					"ABORT: Herdr composer styling could not be checked; nothing was typed: %v",
					checkErr,
				)
			}
			return base, nil
		}
		if !request.ForceNow || !busy || attempt >= engine.options.InterruptTries {
			break
		}
		if err := lock.beat(); err != nil {
			return lockLost(base, target.Pane, err), nil
		}
		if err := engine.SendKey(ctx, target, "Escape"); err != nil {
			base.Status, base.Code = statusUndelivered, CodeUndelivered
			base.Message = fmt.Sprintf("ABORT: Herdr interrupt unconfirmed; nothing was typed: %v", err)
			return base, nil
		}
		base.Interrupted = true
		engine.sleepContext(ctx, engine.options.Poll)
		before, err = herdrRawCapture(ctx, target, "")
		if err != nil {
			base.Status, base.Code = statusUndelivered, CodeUndelivered
			base.Message = fmt.Sprintf("ABORT: Herdr capture failed while interrupting; nothing was typed: %v", err)
			return base, nil
		}
		busy = IsFooterBusy(id, before)
	}
	prepared, err := engine.prepareLiveMessage(ctx, target, request.Message, false)
	if err != nil {
		base.Code, base.Message = CodeUndelivered, fmt.Sprintf("prepare Herdr call: %v", err)
		return base, nil
	}
	base.Unsigned, base.AutoFilePath = prepared.Unsigned, prepared.AutoFilePath
	// A failed acknowledgement cannot prove the bytes were not admitted. Never
	// retry the paste: it could already be waiting in the recipient's composer.
	base.Typed = true
	sent, err := herdrSendText(ctx, target, prepared.Message, "pane.send_input")
	if err != nil {
		base.Typed = !herdrInputUnsent(err)
		base.Status, base.Code, base.Message = statusTypedUnconfirmed, CodeUndelivered, fmt.Sprintf(
			"Herdr raw paste unconfirmed: %v",
			err,
		)
		if !base.Typed {
			base.Status = statusUndelivered
			base.Message = fmt.Sprintf("Herdr raw input refused before input; nothing was typed: %v", err)
		}
		return base, nil
	}
	target = sent
	base.SocketPath, base.Pane = target.SocketPath, target.Pane
	prefix := headRunes(normalizeSpace(prepared.Message), 24)
	for attempt := 1; attempt <= engine.options.EnterTries; attempt++ {
		if err := ctx.Err(); err != nil {
			base.Status, base.Code, base.Message = statusTypedUnconfirmed, CodeUndelivered, fmt.Sprintf(
				"Herdr raw submit cancelled: %v",
				err,
			)
			return base, nil
		}
		if err := lock.beat(); err != nil {
			return lockLost(base, target.Pane, err), nil
		}
		base.SubmitRetries = attempt
		if err := engine.SendKey(ctx, target, "Enter"); err != nil {
			base.Status, base.Code, base.Message = statusTypedUnconfirmed, CodeUndelivered, fmt.Sprintf(
				"Herdr raw Enter unconfirmed: %v",
				err,
			)
			return base, nil
		}
		engine.sleepContext(ctx, engine.options.EnterSettle)
		after, err := herdrRawCapture(ctx, target, "")
		if err != nil {
			base.Status, base.Code, base.Message = statusTypedUnconfirmed, CodeUndelivered, fmt.Sprintf(
				"Herdr raw confirmation failed: %v",
				err,
			)
			return base, nil
		}
		input := composerLastLine(after)
		if HasPastePlaceholder(input) || (input != "" && strings.Contains(normalizeSpace(input), prefix)) {
			continue
		}
		if id == pfmengine.OpenCode && strings.Contains(before, "╹") &&
			strings.Count(normalizeSpace(after), prefix) <= strings.Count(normalizeSpace(before), prefix) {
			break
		}
		if !ComposerRowShown(after) && !deliveryProven(id, before, after, prepared.Message, false, request.FileBacked) {
			break
		}
		base.Status, base.Code = statusDelivered, 0
		base.Message = fmt.Sprintf("called LIVE into %q — Herdr raw composer confirmed", target.Pane)
		base.Proof = lastNonEmptyLines(after, engine.options.ProofLines)
		for _, warning := range prepared.Warnings {
			base.Message += " — WARNING: " + warning
		}
		if len(request.Then) > 0 {
			lock.release()
			if engine.armSteers(
				ctx,
				request,
				target,
				&base,
				&herdrTurn{
					SocketPath: target.SocketPath,
					Pane:       target.Pane,
					Raw:        true,
					SawWorking: IsFooterBusy(id, after),
				},
			) {
				base.Message += fmt.Sprintf(" — %d then steer(s) queued (log: %s)", base.Steers, base.SteerLog)
			}
		}
		return base, nil
	}
	base.Status, base.Code, base.Message = statusTypedUnconfirmed, CodeUndelivered, "typed Herdr raw text but could not confirm submission; do not retry blindly"
	return base, nil
}

// A busy footer does not make the composer unsafe: it is the engine's queue.
// Native Claude still requires the full bounded box, including wrapped lines.
func herdrComposerEmpty(ctx context.Context, target Target, id pfmengine.ID, screen string) (bool, error) {
	ready := func(capture string) bool {
		return ComposerRowShown(capture) && SelectorLine(capture) == "" &&
			!pfmengine.ClaudeTrustDialog(capture) &&
			(!pfmengine.MustLookup(id).HerdrNativePrompt || ComposerBoxEmpty(capture))
	}
	if !ready(screen) {
		return false, nil
	}
	empty, _, err := ComposerEmpty(screen, func() (string, error) {
		styled, readErr := herdrRawCapture(ctx, target, "ansi")
		if readErr != nil {
			return "", readErr
		}
		if !ready(styled) {
			return "", fmt.Errorf("herdr composer changed while checking styling")
		}
		return styled, nil
	})
	return empty, err
}

// herdrSendText proves the seat, then sends text to the pane it holds now,
// returning that proved target so a receipt names where the text went.
func herdrSendText(ctx context.Context, target Target, text, method string) (Target, error) {
	target, err := ProveHerdrTarget(ctx, target)
	if err != nil {
		return target, &herdr.InputNotSentError{Cause: err}
	}
	result, err := herdr.SeatRequest(
		ctx,
		target.SocketPath,
		method,
		map[string]any{"pane_id": target.Pane, "text": text},
	)
	if err != nil {
		return target, fmt.Errorf("send literal to Herdr pane %s: %w", target.Pane, err)
	}
	var ack struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(result, &ack); err != nil {
		return target, fmt.Errorf("decode Herdr literal acknowledgement for %s: %w", target.Pane, err)
	}
	if ack.Type != "ok" {
		return target, fmt.Errorf("herdr literal acknowledgement for %s: unexpected type %q", target.Pane, ack.Type)
	}
	return target, nil
}

func herdrInputUnsent(err error) bool {
	var unsent *herdr.InputNotSentError
	if errors.As(err, &unsent) {
		return true
	}
	var tooLarge *herdr.RequestTooLargeError
	if errors.As(err, &tooLarge) {
		return true
	}
	var refusal *herdr.Refusal
	if errors.As(err, &refusal) {
		switch refusal.Code {
		case "agent_not_ready", "agent_blocked", "empty_agent_prompt", "agent_not_found", "pane_not_found":
			return true
		}
	}
	return false
}
