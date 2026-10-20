package call

import (
	"context"
	"encoding/json"
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

func herdrCapture(ctx context.Context, target Target, lines int) (string, string, error) {
	return herdrCaptureSource(ctx, target, lines, "recent", "")
}

func herdrCaptureFormat(ctx context.Context, target Target, lines int, format string) (string, string, error) {
	return herdrCaptureSource(ctx, target, lines, "visible", format)
}

func herdrCaptureSource(ctx context.Context, target Target, lines int, source, format string) (string, string, error) {
	if target.SocketPath == "" || target.Pane == "" {
		return "", "", fmt.Errorf("herdr capture: socket and pane are required")
	}
	target, err := ProveHerdrTarget(ctx, target)
	if err != nil {
		return "", "", err
	}
	limit := lines
	if limit <= 0 || limit > herdrRecentLines {
		limit = herdrRecentLines
	}
	var stripANSI *bool
	if format == "ansi" {
		strip := false
		stripANSI = &strip
	}
	result, err := herdr.SeatRequest(ctx, target.SocketPath, "pane.read", struct {
		Pane      string `json:"pane_id"`
		Source    string `json:"source"`
		Lines     int    `json:"lines"`
		Format    string `json:"format,omitempty"`
		StripANSI *bool  `json:"strip_ansi,omitempty"`
	}{target.Pane, source, limit, format, stripANSI})
	if err != nil {
		return "", "", fmt.Errorf("herdr capture %q on %q: %w", target.Pane, target.SocketPath, err)
	}
	var response struct {
		Type string `json:"type"`
		Read *struct {
			Format    string  `json:"format"`
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
	if format != "" && response.Read.Format != format {
		return "", "", fmt.Errorf(
			"herdr capture %q: requested format %q, received %q", target.Pane, format, response.Read.Format,
		)
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

type herdrAgentSession struct {
	Source string `json:"source"`
	Agent  string `json:"agent"`
	Kind   string `json:"kind"`
	Value  string `json:"value"`
}

type herdrAgentResult struct {
	Type  string `json:"type"`
	Agent *struct {
		Pane           string             `json:"pane_id"`
		Terminal       string             `json:"terminal_id"`
		Status         string             `json:"agent_status"`
		StateChangeSeq uint64             `json:"state_change_seq"`
		AgentSession   *herdrAgentSession `json:"agent_session"`
		CompletionSeq  *uint64            `json:"completion_seq"`
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
	if kind == "agent_prompted" && response.Agent.Terminal == "" {
		return response, fmt.Errorf("herdr %s for %q: missing terminal identity", kind, target.Pane)
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

// callHerdrRaw preserves the existing composer guard and confirmation without
// relying on native agent detection for an engine whose capability is unproven.

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
	SocketPath     string             `json:"socket_path"`
	Pane           string             `json:"pane"`
	StateChangeSeq uint64             `json:"state_change_seq"`
	CompletionSeq  *uint64            `json:"completion_seq"`
	SawWorking     bool               `json:"saw_working"`
	Raw            bool               `json:"raw,omitempty"`
	Terminal       string             `json:"terminal_id,omitempty"`
	AgentSession   *herdrAgentSession `json:"agent_session,omitempty"`
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
	if err := engine.waitHerdrTurn(bounded, target, turn); err != nil {
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

func (engine *Engine) waitHerdrTurn(ctx context.Context, target Target, turn herdrTurn) error {
	if turn.Raw {
		id, err := pfmengine.Parse(target.Engine)
		if err != nil {
			return fmt.Errorf("raw Herdr turn engine: %w", err)
		}
		for attempt := 0; attempt < engine.options.ThenBusyTries+engine.options.ThenIdleTries; attempt++ {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("raw Herdr turn for %q: %w", target.Pane, err)
			}
			screen, err := herdrRawCapture(ctx, target, "")
			if err != nil {
				return fmt.Errorf("raw Herdr turn capture: %w", err)
			}
			if SelectorLine(screen) != "" {
				return fmt.Errorf("raw Herdr target %q is blocked by a menu", target.Pane)
			}
			if IsFooterBusy(id, screen) {
				turn.SawWorking = true
			} else if turn.SawWorking && ComposerRowShown(screen) {
				redrawn := false
				empty, _, err := ComposerEmpty(screen, func() (string, error) {
					styled, err := herdrRawCapture(ctx, target, "ansi")
					if err != nil {
						return "", err
					}
					if !ComposerRowShown(styled) || SelectorLine(styled) != "" || IsFooterBusy(id, styled) {
						redrawn = true
						return "", fmt.Errorf("raw Herdr composer changed while checking styling")
					}
					return styled, nil
				})
				// A redraw is an unsettled observation, not a failed read.
				// Retry within the existing budget; genuine capture errors stay visible.
				if err != nil && !redrawn {
					return fmt.Errorf("raw Herdr turn composer: %w", err)
				}
				if empty {
					return nil
				}
			}
			engine.sleepContext(ctx, engine.options.ThenIdlePoll)
		}
		return fmt.Errorf("raw Herdr turn boundary for %q was not established", target.Pane)
	}

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
		var err error
		target, err = ProveHerdrTarget(ctx, target)
		if err != nil {
			return err
		}
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
		if (turn.Terminal != "" && agent.Agent.Terminal != turn.Terminal) ||
			(turn.Terminal != "" || turn.AgentSession != nil) &&
				!sameHerdrSession(turn.AgentSession, agent.Agent.AgentSession) {
			return fmt.Errorf("herdr turn identity changed for %q", target.Pane)
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
		// completion_seq uses the producer's global event sequence. An old
		// completion (even newer than another old completion) is not this turn.
		advanced := agent.Agent.CompletionSeq != nil && *agent.Agent.CompletionSeq > turn.StateChangeSeq
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
		_, err := herdrSendText(ctx, target, text, "pane.send_text")
		return err
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

func sameHerdrSession(expected, actual *herdrAgentSession) bool {
	// Older detached records may have no identity. New records preserve the
	// complete validation snapshot, including a later disappearance of identity.
	return (expected == nil && actual == nil) || (expected != nil && actual != nil && *expected == *actual)
}
