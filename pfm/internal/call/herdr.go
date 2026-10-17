package call

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/resolve"
)

const (
	herdrRecentLines       = 1000
	HerdrBackend           = "herdr"
	backendHerdr           = HerdrBackend
	backendTmux            = "tmux"
	herdrBlocked           = "blocked"
	herdrDone              = "done"
	herdrIdle              = "idle"
	herdrWorking           = "working"
	herdrUnknown           = "unknown"
	statusDelivered        = "delivered"
	statusTypedUnconfirmed = "typed_unconfirmed"
)

// SendKey sends one named key to an already resolved seat. The target's address,
// never the calling process's environment, selects the backend.
func (engine *Engine) SendKey(ctx context.Context, target Target, key string) error {
	if target.SocketPath == "" || target.Pane == "" || key == "" {
		return fmt.Errorf("send key: socket, pane and key are required")
	}
	switch target.Backend {
	case "", backendTmux:
		return engine.tmux.SendKey(ctx, target.SocketPath, target.Pane, key)
	case backendHerdr:
		result, err := herdr.SeatRequest(ctx, target.SocketPath, "pane.send_keys", struct {
			Pane string   `json:"pane_id"`
			Keys []string `json:"keys"`
		}{target.Pane, []string{key}})
		if err != nil {
			return fmt.Errorf("herdr send key %q to %q: %w", key, target.Pane, err)
		}
		var ack struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(result, &ack); err != nil {
			return fmt.Errorf("decode herdr send key to %q: %w", target.Pane, err)
		}
		if ack.Type != "ok" {
			return fmt.Errorf("herdr send key to %q: unexpected result type %q", target.Pane, ack.Type)
		}
		return nil
	default:
		return fmt.Errorf("send key: unsupported seat backend %q", target.Backend)
	}
}

func herdrCapture(ctx context.Context, target Target, lines int) (string, string, error) {
	if target.SocketPath == "" || target.Pane == "" {
		return "", "", fmt.Errorf("herdr capture: socket and pane are required")
	}
	limit := lines
	if limit <= 0 || limit > herdrRecentLines {
		limit = herdrRecentLines
	}
	result, err := herdr.SeatRequest(ctx, target.SocketPath, "pane.read", struct {
		Pane   string `json:"pane_id"`
		Source string `json:"source"`
		Lines  int    `json:"lines"`
	}{target.Pane, "recent", limit})
	if err != nil {
		return "", "", fmt.Errorf("herdr capture %q on %q: %w", target.Pane, target.SocketPath, err)
	}
	var response struct {
		Type string `json:"type"`
		Read *struct {
			Pane      string  `json:"pane_id"`
			Workspace string  `json:"workspace_id"`
			Text      *string `json:"text"`
			Truncated *bool   `json:"truncated"`
		} `json:"read"`
	}
	if err := json.Unmarshal(result, &response); err != nil {
		return "", "", fmt.Errorf("decode herdr capture %q: %w", target.Pane, err)
	}
	if response.Type != "pane_read" || response.Read == nil || response.Read.Text == nil ||
		response.Read.Truncated == nil {
		return "", "", fmt.Errorf("herdr capture %q: malformed pane_read result (type %q)", target.Pane, response.Type)
	}
	if response.Read.Pane != target.Pane && response.Read.Workspace+":"+response.Read.Pane != target.Pane {
		return "", "", fmt.Errorf("herdr capture %q: result belongs to pane %q", target.Pane, response.Read.Pane)
	}
	detail := ""
	if lines <= 0 || lines > herdrRecentLines || *response.Read.Truncated {
		detail = fmt.Sprintf("Herdr recent snapshot limited to %d lines; truncated=%t", limit, *response.Read.Truncated)
	}
	return *response.Read.Text, detail, nil
}

type herdrAgentResult struct {
	Type  string `json:"type"`
	Agent *struct {
		Pane           string  `json:"pane_id"`
		Terminal       string  `json:"terminal_id"`
		Status         string  `json:"agent_status"`
		StateChangeSeq uint64  `json:"state_change_seq"`
		CompletionSeq  *uint64 `json:"completion_seq"`
	} `json:"agent"`
}

func decodeHerdrAgent(result json.RawMessage, kind string, target Target) (herdrAgentResult, error) {
	var response herdrAgentResult
	if err := json.Unmarshal(result, &response); err != nil {
		return response, fmt.Errorf("decode herdr %s for %q: %w", kind, target.Pane, err)
	}
	if response.Type != kind || response.Agent == nil {
		return response, fmt.Errorf(
			"herdr %s for %q: unexpected result type %q or missing agent",
			kind,
			target.Pane,
			response.Type,
		)
	}
	if response.Agent.Pane != target.Pane && response.Agent.Terminal != target.Pane {
		return response, fmt.Errorf(
			"herdr %s for %q: result belongs to pane %q (%q)",
			kind,
			target.Pane,
			response.Agent.Pane,
			response.Agent.Terminal,
		)
	}
	switch response.Agent.Status {
	case herdrIdle, herdrWorking, herdrBlocked, herdrDone, herdrUnknown:
		return response, nil
	default:
		return response, fmt.Errorf(
			"herdr %s for %q: invalid agent status %q",
			kind,
			target.Pane,
			response.Agent.Status,
		)
	}
}

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
	if err != nil || !pfmengine.MustLookup(id).HerdrNativePrompt {
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf(
			"ABORT: unsupported native Herdr call engine %q; raw-engine delivery is not implemented",
			target.Engine,
		)
		return base, nil
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
	screen, _, err := herdrCapture(ctx, target, herdrRecentLines)
	if err != nil {
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf("ABORT: native composer could not be checked; nothing was typed: %v", err)
		return base, nil
	}
	if !ComposerRowShown(screen) || SelectorLine(screen) != "" || pfmengine.ClaudeTrustDialog(screen) ||
		ComposerDraftText(screen) != "" {
		base.Code = CodeUndelivered
		base.Status = statusUndelivered
		base.Message = "ABORT: native composer is not empty and ready; nothing was typed, existing draft/menu preserved"
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
		var refusal *herdr.Refusal
		if errors.As(err, &refusal) && refusal.Code == "agent_not_ready" {
			base.Typed = false
		}
		base.Status = statusTypedUnconfirmed
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf("herdr agent.prompt for %q unconfirmed: %v", target.Pane, err)
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
		StateChangeSeq: agent.Agent.StateChangeSeq, CompletionSeq: agent.Agent.CompletionSeq,
		SawWorking: prompted.Agent.Status == herdrWorking,
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

func supportedBackend(backend string) bool {
	return backend == "" || backend == backendTmux || backend == backendHerdr
}

func backendRefusal(backend string) Result {
	return refused(
		CodeUndelivered,
		fmt.Sprintf("unsupported seat backend %q; nothing was typed", strings.TrimSpace(backend)),
	)
}

const herdrTurnEnv = "CHAT_THEN_HERDR_TURN"

// herdrTurn is evidence for one submitted native turn, not a second address
// source. The detached waiter validates its socket/pane against explicit argv.
type herdrTurn struct {
	SocketPath     string  `json:"socket_path"`
	Pane           string  `json:"pane"`
	StateChangeSeq uint64  `json:"state_change_seq"`
	CompletionSeq  *uint64 `json:"completion_seq"`
	SawWorking     bool    `json:"saw_working"`
}

func (engine *Engine) armSteers(
	ctx context.Context,
	request Request,
	target Target,
	base *Result,
	turn *herdrTurn,
) bool {
	base.SteerLog = engine.steerLogPath(target)
	if err := engine.spawner.Spawn(ctx, SteerSpawn{
		Backend: target.Backend, SocketPath: target.SocketPath, Target: target.Pane,
		Engine: target.Engine, Steers: request.Then, LogPath: base.SteerLog,
		Append: request.Chain, Sender: engine.sender(ctx), herdrTurn: turn,
	}); err != nil {
		base.Status = statusDelivered
		base.Code = CodeUndelivered
		base.Message = fmt.Sprintf(
			"delivered the primary into %q but could NOT arm the %d then steer(s): %v — deliver them yourself once the pane is idle",
			target.Pane,
			len(request.Then),
			err,
		)
		return false
	}
	base.Steers = len(request.Then)
	return true
}

func (engine *Engine) deliverHerdrThen(ctx context.Context, wait ThenWait) (result Result, err error) {
	target := Target{Backend: wait.Backend, SocketPath: wait.SocketPath, Pane: wait.Target, Engine: wait.Engine}
	fail := func(cause error) (Result, error) {
		return Result{
			Status:     statusUndelivered,
			Code:       CodeUndelivered,
			SocketPath: target.SocketPath,
			Pane:       target.Pane,
			Message: fmt.Sprintf(
				"then steer NOT delivered to Herdr pane %q: %v; steer kept in the log: %q",
				target.Pane,
				cause,
				wait.Steers[0],
			),
		}, nil
	}
	armed := armedPathFor(engine.steerLogPath(target))
	if err := engine.claimArmed(armed, wait.Steers[0]); err != nil {
		return fail(fmt.Errorf("read armed steer record: %w", err))
	}
	defer func() { engine.releaseArmed(armed, err == nil && result.Code == 0 && result.Steers > 0) }()
	var turn herdrTurn
	if err := json.Unmarshal([]byte(engine.env.Get(herdrTurnEnv)), &turn); err != nil {
		return fail(fmt.Errorf("read native turn evidence: %w", err))
	}
	if turn.SocketPath != target.SocketPath || turn.Pane != target.Pane {
		return fail(fmt.Errorf("native turn evidence does not match explicit target socket/pane"))
	}
	budget := engine.options.ThenMin + time.Duration(engine.options.ThenBusyTries)*engine.options.Poll +
		time.Duration(engine.options.ThenIdleTries)*engine.options.ThenIdlePoll + engine.options.ThenSettle
	bounded, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if err := waitHerdrTurn(bounded, target, turn); err != nil {
		return fail(err)
	}
	if checked, ok := engine.checkSteerChain(
		Request{Message: wait.Steers[0], Then: wait.Steers[1:], Chain: true},
	); !ok {
		return checked, nil
	}
	return engine.callResolved(
		ctx,
		Request{Target: target.Pane, Message: wait.Steers[0], Then: wait.Steers[1:], Chain: true},
		target,
		"",
	)
}

func waitHerdrTurn(ctx context.Context, target Target, turn herdrTurn) error {
	until := []string{herdrIdle, herdrDone, herdrBlocked}
	// At most three snapshots: stale pre-turn idle, observed working, settled.
	// A server response that ignores the requested transition is a protocol
	// failure, never permission to spin on or submit into a stale idle sample.
	for attempt := 0; attempt < 3; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("herdr agent.wait for %q: %w", target.Pane, err)
		}
		deadline, ok := ctx.Deadline()
		if !ok {
			return fmt.Errorf("herdr agent.wait for %q: missing deadline", target.Pane)
		}
		remaining := time.Until(deadline)
		result, err := herdr.SeatRequest(ctx, target.SocketPath, "agent.wait", struct {
			Target    string   `json:"target"`
			Until     []string `json:"until"`
			TimeoutMS int64    `json:"timeout_ms"`
		}{target.Pane, until, max(int64(1), remaining.Milliseconds())})
		if err != nil {
			return fmt.Errorf("herdr agent.wait for %q: %w", target.Pane, err)
		}
		agent, err := decodeHerdrAgent(result, "agent_info", target)
		if err != nil {
			return err
		}
		state := agent.Agent.Status
		if state == herdrBlocked || state == herdrUnknown {
			return fmt.Errorf("herdr target %q is %s", target.Pane, state)
		}
		matched := false
		for _, allowed := range until {
			matched = matched || state == allowed
		}
		if !matched {
			return fmt.Errorf(
				"herdr turn boundary for %q: agent.wait returned %q outside requested states %v",
				target.Pane,
				state,
				until,
			)
		}
		if state == herdrWorking {
			turn.SawWorking = true
			until = []string{herdrIdle, herdrDone, herdrBlocked}
			continue
		}
		advanced := agent.Agent.StateChangeSeq > turn.StateChangeSeq ||
			(turn.CompletionSeq != nil && agent.Agent.CompletionSeq != nil && *agent.Agent.CompletionSeq > *turn.CompletionSeq)
		if turn.SawWorking || advanced {
			return nil
		}
		until = []string{herdrWorking, herdrBlocked}
	}
	return fmt.Errorf("herdr turn boundary for %q was not established", target.Pane)
}

func seatFromTarget(target Target) resolve.Seat {
	return resolve.Seat{
		SocketPath: target.SocketPath, Pane: target.Pane, Engine: target.Engine,
		Name: target.Name, ID: target.ID, Session: target.Session,
		Backend: target.Backend, SeatID: target.SeatID, HerdrWorkspace: target.HerdrWorkspace,
	}
}

func targetFromSeat(seat resolve.Seat) Target {
	return Target{
		SocketPath: seat.SocketPath, Pane: seat.Pane, Engine: seat.Engine,
		Name: seat.Name, ID: seat.ID, Session: seat.Session,
		Backend: seat.Backend, SeatID: seat.SeatID, HerdrWorkspace: seat.HerdrWorkspace,
	}
}

// SendLiteral writes literal input to the explicitly resolved seat.
func (engine *Engine) SendLiteral(ctx context.Context, target Target, text string) error {
	switch target.Backend {
	case "", backendTmux:
		return engine.tmux.SendLiteral(ctx, target.SocketPath, target.Pane, text)
	case backendHerdr:
		result, err := herdr.SeatRequest(
			ctx,
			target.SocketPath,
			"pane.send_text",
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
	default:
		return fmt.Errorf("unsupported seat backend %q", target.Backend)
	}
}

// CaptureTarget reads the explicitly resolved seat without resolving its name again.
func (engine *Engine) CaptureTarget(ctx context.Context, target Target, tailLines int) (string, error) {
	if target.Backend == backendHerdr {
		text, detail, err := herdrCapture(ctx, target, tailLines)
		if err != nil {
			return "", err
		}
		if detail != "" {
			text = "[" + detail + "]\n" + text
		}
		return text, nil
	}
	if tailLines == FullScrollback && target.Backend != backendHerdr && supportedBackend(target.Backend) {
		return engine.tmux.Capture(ctx, target.SocketPath, target.Pane, true, FullScrollback)
	}
	return engine.capturePane(ctx, target, tailLines)
}
