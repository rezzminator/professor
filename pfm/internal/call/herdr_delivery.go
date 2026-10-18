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
	result, err := herdr.SeatRequest(ctx, target.SocketPath, "agent.get", map[string]string{"target": target.Pane})
	if err != nil {
		return base, fmt.Errorf("herdr read target %q on %q: %w", target.Pane, target.SocketPath, err)
	}
	agent, err := decodeHerdrAgent(result, "agent_info", target)
	if err != nil {
		base.Code = CodeUndelivered
		base.Message = err.Error()
		return base, nil
	}
	switch agent.Agent.Status {
	case herdrWorking:
		base.Busy = true
		base.Code = CodeBusy
		base.Message = "ABORT: target pane is busy; nothing was typed"
		return base, nil
	case herdrBlocked, herdrUnknown:
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf("ABORT: target pane is %s; nothing was typed", agent.Agent.Status)
		return base, nil
	}
	screen, _, err := herdrCaptureFormat(ctx, target, herdrRecentLines, "")
	if err != nil {
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf("ABORT: native composer could not be checked; nothing was typed: %v", err)
		return base, nil
	}
	if !ComposerRowShown(screen) || SelectorLine(screen) != "" || pfmengine.ClaudeTrustDialog(screen) ||
		IsFooterBusy(id, screen) {
		base.Code = CodeUndelivered
		base.Status = statusUndelivered
		base.Message = "ABORT: native composer is not empty and ready; nothing was typed, existing draft/menu preserved"
		return base, nil
	}
	empty, _, err := ComposerEmpty(screen, func() (string, error) {
		styled, _, readErr := herdrCaptureFormat(ctx, target, herdrRecentLines, "ansi")
		if readErr != nil {
			return "", readErr
		}
		if !ComposerRowShown(styled) || SelectorLine(styled) != "" || pfmengine.ClaudeTrustDialog(styled) ||
			IsFooterBusy(id, styled) || !ComposerBoxEmpty(styled) {
			return "", fmt.Errorf("herdr composer changed while checking styling")
		}
		return styled, nil
	})
	if err != nil || !empty || !ComposerBoxEmpty(screen) {
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
	result, err = herdr.SeatRequest(ctx, target.SocketPath, "agent.prompt", struct {
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
	turn := &herdrTurn{
		SocketPath: target.SocketPath, Pane: target.Pane,
		StateChangeSeq: prompted.Agent.StateChangeSeq, CompletionSeq: prompted.Agent.CompletionSeq,
		Terminal: prompted.Agent.Terminal, AgentSession: prompted.Agent.AgentSession,
	}
	base.Status = statusDelivered
	base.Message = fmt.Sprintf("called LIVE into %q — Herdr agent_prompted", target.Pane)
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
	if IsFooterBusy(id, before) {
		base.Busy, base.Code, base.Message = true, CodeBusy, "ABORT: target pane is busy; nothing was typed"
		return base, nil
	}
	if !ComposerRowShown(before) || SelectorLine(before) != "" {
		base.Status, base.Code, base.Message = statusUndelivered, CodeUndelivered, "ABORT: Herdr composer is not empty and ready; nothing was typed, existing draft/menu preserved"
		return base, nil
	}
	empty, _, err := ComposerEmpty(before, func() (string, error) {
		styled, readErr := herdrRawCapture(ctx, target, "ansi")
		if readErr != nil {
			return "", readErr
		}
		if !ComposerRowShown(styled) || SelectorLine(styled) != "" || IsFooterBusy(id, styled) {
			return "", fmt.Errorf("herdr composer changed while checking styling")
		}
		return styled, nil
	})
	if err != nil {
		base.Code, base.Message = CodeUndelivered, fmt.Sprintf(
			"ABORT: Herdr composer styling could not be checked; nothing was typed: %v", err,
		)
		return base, nil
	}
	if !empty {
		base.Status, base.Code = statusUndelivered, CodeUndelivered
		base.Message = "ABORT: Herdr composer is not empty and ready; nothing was typed, existing draft/menu preserved"
		return base, nil
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
	if err := herdrSendText(ctx, target, prepared.Message, "pane.send_input"); err != nil {
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

func herdrSendText(ctx context.Context, target Target, text, method string) error {
	result, err := herdr.SeatRequest(
		ctx,
		target.SocketPath,
		method,
		map[string]any{"pane_id": target.Pane, "text": text},
	)
	if err != nil {
		return fmt.Errorf("send literal to Herdr pane %s: %w", target.Pane, err)
	}
	var ack struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(result, &ack); err != nil {
		return fmt.Errorf("decode Herdr literal acknowledgement for %s: %w", target.Pane, err)
	}
	if ack.Type != "ok" {
		return fmt.Errorf("herdr literal acknowledgement for %s: unexpected type %q", target.Pane, ack.Type)
	}
	return nil
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
