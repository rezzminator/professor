package hookentry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// callmeterPayload is the part of a hook's stdin callmeter reads, across the
// seven events it is registered on (docs/design/hooks/callmeter.md § What the
// harness gives a hook).
type callmeterPayload struct {
	HookEventName       string          `json:"hook_event_name"`
	SessionID           string          `json:"session_id"`
	TranscriptPath      string          `json:"transcript_path"`
	Cwd                 string          `json:"cwd"`
	AgentID             string          `json:"agent_id"`
	AgentType           string          `json:"agent_type"`
	AgentTranscriptPath string          `json:"agent_transcript_path"`
	ToolName            string          `json:"tool_name"`
	ToolUseID           string          `json:"tool_use_id"`
	ToolInput           json.RawMessage `json:"tool_input"`
	ToolResponse        json.RawMessage `json:"tool_response"`
	DurationMS          *float64        `json:"duration_ms"`
	Error               *string         `json:"error"`
	ToolCalls           []struct {
		ToolUseID    string          `json:"tool_use_id"`
		ToolResponse json.RawMessage `json:"tool_response"`
	} `json:"tool_calls"`
}

// calls names the tool calls this payload is about, for a failure that loses
// its whole record: the tool_use_id, or a batch's ids joined by commas.
func (p callmeterPayload) calls() string {
	if p.ToolUseID != "" || len(p.ToolCalls) == 0 {
		return p.ToolUseID
	}
	ids := make([]string, 0, len(p.ToolCalls))
	for _, call := range p.ToolCalls {
		ids = append(ids, call.ToolUseID)
	}
	return strings.Join(ids, ",")
}

// callmeterResponse is the part of a PostToolUse tool_response callmeter
// reads for itself: the output sizes and an Agent call's sub-agent totals. The
// file columns are callmeter.FileColumnsFromResult's.
type callmeterResponse struct {
	Stdout              *string         `json:"stdout"`
	PersistedOutputPath *string         `json:"persistedOutputPath"`
	PersistedOutputSize *float64        `json:"persistedOutputSize"`
	Content             json.RawMessage `json:"content"`
	File                *struct {
		Content *string `json:"content"`
	} `json:"file"`
	AgentID           string   `json:"agentId"`
	AgentType         string   `json:"agentType"`
	TotalTokens       *float64 `json:"totalTokens"`
	TotalToolUseCount *float64 `json:"totalToolUseCount"`
	ResolvedModel     *string  `json:"resolvedModel"`
}

// callmeterRun is one `pfm internal callmeter` invocation: one payload, one
// store, one clock reading.
type callmeterRun struct {
	ctx     context.Context
	stderr  io.Writer
	store   *callmeter.Store
	now     int64
	payload callmeterPayload
}

// Callmeter is the async hook that records each hook event into the callmeter
// store under $HOME. It exits 0 on every path and writes nothing to stdout —
// it changes nothing the model sees — and a failure to record is said on
// stderr, in pfm's log with the session and tool_use_id, and as a faults row
// whenever the store itself is reachable.
func Callmeter(input io.Reader, stderr io.Writer, env paths.Env) int {
	ctx := obs.Component(context.Background(), "hooks")
	home, err := paths.HomeFrom(env)
	if err != nil {
		logCallmeterFailure(ctx, stderr, callmeter.StageStore, "", "", fmt.Errorf("resolve home directory: %w", err))
		return 0
	}
	return runCallmeter(ctx, input, stderr, callmeter.DefaultPath(home), clock.Real)
}

func runCallmeter(ctx context.Context, input io.Reader, stderr io.Writer, storePath string, timing clock.Clock) int {
	run := &callmeterRun{ctx: ctx, stderr: stderr, now: timing.Now().UnixMilli()}
	raw, readErr := io.ReadAll(input)
	var payloadErr error
	if readErr != nil {
		payloadErr = fmt.Errorf("read hook payload: %w", readErr)
	} else if err := json.Unmarshal(raw, &run.payload); err != nil {
		payloadErr = fmt.Errorf("decode hook payload (%d bytes): %w", len(raw), err)
	}
	store, err := callmeter.OpenDB(ctx, storePath)
	if err != nil {
		run.fault(callmeter.StageStore, run.payload.calls(), errors.Join(payloadErr, err))
		return 0
	}
	run.store = store
	defer func() {
		if err := store.Close(); err != nil {
			run.store = nil // closed: the fault is said, never written
			run.fault(callmeter.StageStore, run.payload.calls(), err)
		}
	}()
	if payloadErr != nil {
		run.fault(callmeter.StagePayload, run.payload.ToolUseID, payloadErr)
		return 0
	}
	run.record()
	return 0
}

func (run *callmeterRun) record() {
	p := run.payload
	if p.SessionID == "" {
		run.fault(callmeter.StagePayload, p.ToolUseID, fmt.Errorf("%q payload carries no session_id", p.HookEventName))
		return
	}
	switch p.HookEventName {
	case "PreToolUse":
		run.recordStartCwd()
	case "PostToolUse", "PostToolUseFailure":
		run.recordCall(p.HookEventName == "PostToolUseFailure")
	case "PostToolBatch":
		run.recordBatch()
	case "SubagentStart", "SubagentStop":
		run.recordAgent(p.HookEventName == "SubagentStop")
	case "Stop":
		run.resolvePending("", p.TranscriptPath)
	default:
		run.fault(
			callmeter.StagePayload,
			p.ToolUseID,
			fmt.Errorf("event %q is not one callmeter records", p.HookEventName),
		)
	}
}

// base is the columns every hook-written call carries.
func (run *callmeterRun) base(toolUseID string) callmeter.Call {
	p := run.payload
	call := callmeter.Call{
		ToolUseID: toolUseID,
		SessionID: callmeter.Ptr(p.SessionID),
		Source:    callmeter.Ptr(callmeter.SourceHook),
	}
	if p.AgentID != "" {
		call.AgentID = callmeter.Ptr(p.AgentID)
		call.AgentType = presentString(p.AgentType)
	}
	call.ConfigDir = presentString(callmeter.ConfigDirOf(p.TranscriptPath))
	return call
}

func (run *callmeterRun) recordCall(failed bool) {
	p := run.payload
	if p.ToolUseID == "" || p.ToolName == "" {
		run.fault(
			callmeter.StagePayload,
			p.ToolUseID,
			fmt.Errorf("%s payload lacks tool_use_id or tool_name", p.HookEventName),
		)
		return
	}
	call := run.base(p.ToolUseID)
	call.TS = callmeter.Ptr(run.now)
	call.Tool = callmeter.Ptr(p.ToolName)
	call.DurationMS = wholeNumber(p.DurationMS)
	call.Failed = callmeter.Ptr(failed)
	if len(p.ToolInput) > 0 {
		input, err := callmeter.SanitizeInput(p.ToolName, p.ToolInput)
		if err != nil {
			run.fault(callmeter.StagePayload, p.ToolUseID, err)
		} else {
			call.Input = &input
		}
	}
	run.fileColumns(&call)
	var agent *callmeter.Agent
	if failed {
		call.Error = p.Error
		// A failure's output reaches the model as this text: it is the size.
		if p.Error != nil {
			call.BytesReal = callmeter.Ptr(int64(len(*p.Error)))
		}
	} else if len(p.ToolResponse) > 0 {
		agent = run.responseColumns(&call)
	}
	run.write(p.ToolUseID, func(tx *callmeter.Tx) error {
		if err := tx.UpsertCall(run.ctx, call, callmeter.Overwrite); err != nil {
			return err
		}
		// This cwd is where the command left the shell; PreToolUse's, the
		// directory it started in, wins whenever it lands (recordStartCwd).
		after := callmeter.Call{ToolUseID: p.ToolUseID, Cwd: presentString(p.Cwd)}
		if err := tx.UpsertCall(run.ctx, after, callmeter.FillEmpty); err != nil {
			return err
		}
		if agent == nil {
			return nil
		}
		// The totals only fill: the agent's own transcript, summed at its
		// stop, holds every turn (recordAgent).
		totals := callmeter.Agent{AgentID: agent.AgentID, TotalTokens: agent.TotalTokens, ToolUses: agent.ToolUses}
		agent.TotalTokens, agent.ToolUses = nil, nil
		if err := tx.UpsertAgent(run.ctx, *agent, callmeter.Overwrite); err != nil {
			return err
		}
		return tx.UpsertAgent(run.ctx, totals, callmeter.FillEmpty)
	})
}

// recordStartCwd stores the directory a Bash command starts in. PostToolUse's
// cwd follows the command's own `cd`, and the parser replays that `cd` from the
// stored cwd, so only PreToolUse's is right; it overwrites whatever PostToolUse
// filled, and PostToolUse never overwrites it, in either landing order. ts is
// set so a call that never finishes still ages out of the store.
func (run *callmeterRun) recordStartCwd() {
	p := run.payload
	if p.ToolUseID == "" || p.Cwd == "" {
		run.fault(callmeter.StagePayload, p.ToolUseID, fmt.Errorf("PreToolUse payload lacks tool_use_id or cwd"))
		return
	}
	call := run.base(p.ToolUseID)
	call.Cwd = callmeter.Ptr(p.Cwd)
	start := callmeter.Call{ToolUseID: p.ToolUseID, TS: callmeter.Ptr(run.now), Tool: presentString(p.ToolName)}
	run.write(p.ToolUseID, func(tx *callmeter.Tx) error {
		if err := tx.UpsertCall(run.ctx, call, callmeter.Overwrite); err != nil {
			return err
		}
		return tx.UpsertCall(run.ctx, start, callmeter.FillEmpty)
	})
}

// fileColumns sets the file columns a file tool's input names
// (callmeter.FileColumnsFromInput) and the file's size now. An input it cannot
// read is a fault; a missing file leaves file_bytes NULL; any other stat
// failure is a fault and leaves it NULL too.
func (run *callmeterRun) fileColumns(call *callmeter.Call) {
	p := run.payload
	if err := callmeter.FileColumnsFromInput(call, p.ToolName, p.ToolInput, p.Cwd); err != nil {
		run.fault(callmeter.StagePayload, p.ToolUseID, err)
		return
	}
	if call.FilePath == nil {
		return // not a file tool
	}
	path := *call.FilePath
	info, err := os.Stat(path)
	switch {
	case err == nil:
		call.FileBytes = callmeter.Ptr(info.Size())
	case !errors.Is(err, fs.ErrNotExist):
		run.fault(callmeter.StagePayload, p.ToolUseID, fmt.Errorf("stat %s: %w", path, err))
	}
}

// responseColumns reads the tool's own result: the real output size, the
// persisted path, the file columns, and — for an Agent or
// Task call — the sub-agent it spawned.
func (run *callmeterRun) responseColumns(call *callmeter.Call) *callmeter.Agent {
	p := run.payload
	trimmed := bytes.TrimSpace(p.ToolResponse)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		size, err := callmeter.DeliveredBytes(trimmed)
		if err != nil {
			run.fault(callmeter.StagePayload, p.ToolUseID, err)
			return nil
		}
		call.BytesReal = callmeter.Ptr(size)
		return nil
	}
	var response callmeterResponse
	if err := json.Unmarshal(trimmed, &response); err != nil {
		run.fault(callmeter.StagePayload, p.ToolUseID, fmt.Errorf("decode %s tool_response: %w", p.ToolName, err))
		return nil
	}
	switch {
	case response.PersistedOutputSize != nil:
		call.BytesReal = wholeNumber(response.PersistedOutputSize)
	case response.Stdout != nil:
		call.BytesReal = callmeter.Ptr(int64(len(*response.Stdout)))
	case response.File != nil && response.File.Content != nil:
		call.BytesReal = callmeter.Ptr(int64(len(*response.File.Content)))
	case len(response.Content) > 0 && !bytes.Equal(response.Content, []byte("null")):
		size, err := callmeter.DeliveredBytes(response.Content)
		if err != nil {
			run.fault(callmeter.StagePayload, p.ToolUseID, err)
		} else {
			call.BytesReal = callmeter.Ptr(size)
		}
	default:
		call.BytesReal = callmeter.Ptr(int64(len(trimmed)))
	}
	call.PersistedPath = response.PersistedOutputPath
	if err := callmeter.FileColumnsFromResult(call, p.ToolName, trimmed); err != nil {
		run.fault(callmeter.StagePayload, p.ToolUseID, err)
	}
	if (p.ToolName != "Agent" && p.ToolName != "Task") || response.AgentID == "" {
		return nil
	}
	return &callmeter.Agent{
		AgentID:         response.AgentID,
		SessionID:       callmeter.Ptr(p.SessionID),
		AgentType:       presentString(response.AgentType),
		ParentToolUseID: callmeter.Ptr(p.ToolUseID),
		TotalTokens:     wholeNumber(response.TotalTokens),
		ToolUses:        wholeNumber(response.TotalToolUseCount),
		Model:           response.ResolvedModel,
		Source:          callmeter.Ptr(callmeter.SourceHook),
		ConfigDir:       presentString(callmeter.ConfigDirOf(p.TranscriptPath)),
	}
}

// recordBatch writes each call's delivered bytes and the model request that
// grouped them: the transcript's message when it is on disk, a provisional
// pending row when it is not yet, and a transcript fault besides when the
// transcript could not be read.
func (run *callmeterRun) recordBatch() {
	p := run.payload
	if len(p.ToolCalls) == 0 {
		run.fault(callmeter.StagePayload, "", errors.New("PostToolBatch payload carries no tool_calls"))
		return
	}
	transcript := p.TranscriptPath
	if p.AgentID != "" {
		transcript = callmeter.SubagentTranscriptPath(p.TranscriptPath, p.AgentID)
	}
	// Claude Code's own internal agents (agent_id set, agent_type empty) have
	// no SubagentStart and no transcript on disk: nothing to meter, so no
	// call, no request and no transcript fault (recordAgent's exemption,
	// shared by untypedAgentMissingTranscript).
	if untypedAgentMissingTranscript(p.AgentID, p.AgentType, transcript) {
		return
	}
	ids := make([]string, 0, len(p.ToolCalls))
	calls := make([]callmeter.Call, 0, len(p.ToolCalls))
	for _, toolCall := range p.ToolCalls {
		if toolCall.ToolUseID == "" {
			run.fault(callmeter.StagePayload, "", errors.New("PostToolBatch call carries no tool_use_id"))
			return
		}
		call := run.base(toolCall.ToolUseID)
		size, err := callmeter.DeliveredBytes(toolCall.ToolResponse)
		if err != nil {
			run.fault(callmeter.StagePayload, toolCall.ToolUseID, err)
		} else {
			call.BytesDelivered = callmeter.Ptr(size)
		}
		ids = append(ids, toolCall.ToolUseID)
		calls = append(calls, call)
	}
	found, err := callmeter.FindRequests(transcript, ids)
	if err != nil {
		run.fault(callmeter.StageTranscript, ids[0], err)
		found = nil
	}
	requests := map[string]*callmeter.Request{}
	var order []string
	provisional := ""
	for i := range calls {
		key := ""
		usage, ok := found[calls[i].ToolUseID]
		switch {
		case ok:
			key = usage.MessageID
		case provisional == "":
			provisional = callmeter.ProvisionalKey(calls[i].ToolUseID)
			key = provisional
		default:
			key = provisional
		}
		calls[i].RequestID = callmeter.Ptr(key)
		request, seen := requests[key]
		if !seen {
			request = run.request(key)
			if ok {
				request.TS = callmeter.Ptr(usage.TS)
				request.ContextTokens = callmeter.Ptr(usage.ContextTokens)
				request.OutputTokens = callmeter.Ptr(usage.OutputTokens)
				request.Pending = callmeter.Ptr(false)
			} else {
				request.TS = callmeter.Ptr(run.now)
				request.Pending = callmeter.Ptr(true)
			}
			request.Calls = callmeter.Ptr(int64(0))
			requests[key] = request
			order = append(order, key)
		}
		*request.Calls++
	}
	run.write(ids[0], func(tx *callmeter.Tx) error {
		for _, key := range order {
			if err := tx.UpsertRequest(run.ctx, *requests[key], callmeter.Overwrite); err != nil {
				return err
			}
		}
		for i := range calls {
			if err := tx.UpsertCall(run.ctx, calls[i], callmeter.Overwrite); err != nil {
				return err
			}
		}
		return nil
	})
	// An earlier batch of this chat or sub-agent may have run before its
	// request reached the disk; retry it now rather than at the Stop, which
	// for a long sub-agent is an hour away. A transcript FindRequests could
	// not read would only fail the same way again: one fault, not two.
	if err == nil {
		run.resolvePending(p.AgentID, transcript)
	}
}

// untypedAgentMissingTranscript reports whether p names one of Claude Code's
// own internal agents — agent_id set, agent_type empty — whose transcript was
// never written: no SubagentStart, nothing to meter. Shared by recordAgent
// and recordBatch so the exemption is checked once.
func untypedAgentMissingTranscript(agentID, agentType, transcript string) bool {
	if agentID == "" || agentType != "" {
		return false
	}
	_, err := os.Stat(transcript)
	return errors.Is(err, fs.ErrNotExist)
}

// request is a requests row carrying this payload's owner columns; the main
// chat's agent_id stays NULL, the key PendingRequests looks it up by.
func (run *callmeterRun) request(key string) *callmeter.Request {
	p := run.payload
	request := &callmeter.Request{
		RequestID: key,
		SessionID: callmeter.Ptr(p.SessionID),
		Source:    callmeter.Ptr(callmeter.SourceHook),
		ConfigDir: presentString(callmeter.ConfigDirOf(p.TranscriptPath)),
	}
	if p.AgentID != "" {
		request.AgentID = callmeter.Ptr(p.AgentID)
	}
	return request
}

func (run *callmeterRun) recordAgent(stopped bool) {
	p := run.payload
	if p.AgentID == "" {
		run.fault(callmeter.StagePayload, "", fmt.Errorf("%s payload carries no agent_id", p.HookEventName))
		return
	}
	agent := callmeter.Agent{
		AgentID:   p.AgentID,
		SessionID: callmeter.Ptr(p.SessionID),
		AgentType: presentString(p.AgentType),
		Source:    callmeter.Ptr(callmeter.SourceHook),
		ConfigDir: presentString(callmeter.ConfigDirOf(p.TranscriptPath)),
	}
	if !stopped {
		// An agent woken for another turn starts again: started stays its first.
		start := callmeter.Agent{AgentID: p.AgentID, Started: callmeter.Ptr(run.now)}
		run.write("", func(tx *callmeter.Tx) error {
			if err := tx.UpsertAgent(run.ctx, agent, callmeter.Overwrite); err != nil {
				return err
			}
			return tx.UpsertAgent(run.ctx, start, callmeter.FillEmpty)
		})
		return
	}
	agent.Stopped = callmeter.Ptr(run.now)
	transcript := p.AgentTranscriptPath
	if transcript == "" {
		transcript = callmeter.SubagentTranscriptPath(p.TranscriptPath, p.AgentID)
	}
	// Claude Code stops its own internal agents with no agent_type and no
	// transcript on disk (never a SubagentStart): no sub-agent to meter, so no
	// row and no fault. A typed agent missing its transcript stays a fault.
	if untypedAgentMissingTranscript(p.AgentID, p.AgentType, transcript) {
		return
	}
	agent.TranscriptPath = callmeter.Ptr(transcript)
	// The totals are the agent's own transcript summed at its latest stop:
	// every turn it ran, read from that transcript. They overwrite what an
	// earlier stop or the Agent result wrote; the model only fills, so the
	// Agent result's resolved model stays.
	var totals, model *callmeter.Agent
	if sum, err := run.settledAgentTotals(transcript); err != nil {
		run.fault(callmeter.StageTranscript, "", fmt.Errorf("agent %s totals: %w", p.AgentID, err))
	} else {
		totals = &callmeter.Agent{
			AgentID:     p.AgentID,
			TotalTokens: callmeter.Ptr(sum.TotalTokens),
			ToolUses:    callmeter.Ptr(sum.ToolUses),
		}
		model = &callmeter.Agent{AgentID: p.AgentID, Model: presentString(sum.Model)}
	}
	run.write("", func(tx *callmeter.Tx) error {
		if err := tx.UpsertAgent(run.ctx, agent, callmeter.Overwrite); err != nil {
			return err
		}
		if totals == nil {
			return nil
		}
		if err := tx.UpsertAgent(run.ctx, *totals, callmeter.Overwrite); err != nil {
			return err
		}
		return tx.UpsertAgent(run.ctx, *model, callmeter.FillEmpty)
	})
	run.resolvePending(p.AgentID, transcript)
}

// agentSettle bounds how long SubagentStop waits for the agent's final
// message: Claude Code fires the hook 20-50 ms after stamping that message and
// before flushing its line, so an immediate read sums every message but the
// last. The hook is async, so the wait never holds the model.
var agentSettle = 3 * time.Second

// settledAgentTotals reads the agent transcript until its last assistant
// entry ends the turn (AgentTotals.Final), or agentSettle has passed — a
// killed agent never writes one — and returns the last read. It waits on
// another process's write, so it runs on the wall clock, never a test's fake.
func (run *callmeterRun) settledAgentTotals(transcript string) (callmeter.AgentTotals, error) {
	deadline := clock.Real.Now().Add(agentSettle)
	for {
		sum, err := callmeter.ReadAgentTotals(transcript)
		if err != nil || sum.Final || !clock.Real.Now().Before(deadline) {
			return sum, err
		}
		if err := clock.Real.Sleep(run.ctx, 25*time.Millisecond); err != nil {
			return sum, fmt.Errorf("wait for the final message of %s: %w", transcript, err)
		}
	}
}

// resolvePending fills the pending requests of one chat or sub-agent from its
// transcript. A request still absent stays pending — the next Stop tries again.
func (run *callmeterRun) resolvePending(agentID, transcript string) {
	p := run.payload
	pending, err := run.store.PendingRequests(run.ctx, p.SessionID, agentID)
	if err != nil {
		run.fault(callmeter.StageStore, "", err)
		return
	}
	if len(pending) == 0 {
		return
	}
	var ids []string
	for _, request := range pending {
		ids = append(ids, request.CallIDs...)
	}
	found, err := callmeter.FindRequests(transcript, ids)
	if err != nil {
		run.fault(callmeter.StageTranscript, ids[0], err)
		return
	}
	run.write(ids[0], func(tx *callmeter.Tx) error {
		for _, request := range pending {
			for _, id := range request.CallIDs {
				usage, ok := found[id]
				if !ok {
					continue
				}
				resolved := callmeter.Request{
					RequestID:     usage.MessageID,
					TS:            callmeter.Ptr(usage.TS),
					ContextTokens: callmeter.Ptr(usage.ContextTokens),
					OutputTokens:  callmeter.Ptr(usage.OutputTokens),
					Source:        callmeter.Ptr(callmeter.SourceHook),
				}
				if err := tx.ResolveRequest(run.ctx, request.RequestID, resolved); err != nil {
					return err
				}
				break
			}
		}
		return nil
	})
}

// write runs one event's writes in one transaction; a failure is a store fault.
func (run *callmeterRun) write(toolUseID string, fn func(*callmeter.Tx) error) {
	if err := run.store.Batch(run.ctx, fn); err != nil {
		run.fault(callmeter.StageStore, toolUseID, err)
	}
}

// fault says a failure to record: stderr and pfm's log always, and a faults
// row when the store is open. A fault the store refuses is logged only.
func (run *callmeterRun) fault(stage, toolUseID string, cause error) {
	session := run.payload.SessionID
	logCallmeterFailure(run.ctx, run.stderr, stage, session, toolUseID, cause)
	if run.store == nil {
		return
	}
	err := run.store.AddFault(run.ctx, callmeter.Fault{
		TS: run.now, SessionID: session, ToolUseID: toolUseID, Stage: stage, Error: cause.Error(),
	})
	if err != nil {
		logCallmeterFailure(run.ctx, run.stderr, callmeter.StageStore, session, toolUseID, err)
	}
}

func logCallmeterFailure(ctx context.Context, stderr io.Writer, stage, session, toolUseID string, cause error) {
	fmt.Fprintf(stderr, "pfm internal callmeter: %s: session %q call %q: %v\n", stage, session, toolUseID, cause)
	obs.Logger(ctx).LogAttrs(ctx, slog.LevelError, "callmeter.record",
		slog.String("hook", "callmeter"),
		slog.String("step", stage),
		slog.String("session", session),
		slog.String("target", toolUseID),
		slog.String(obs.FieldErr, cause.Error()),
	)
}

func presentString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func wholeNumber(value *float64) *int64 {
	if value == nil {
		return nil
	}
	return callmeter.Ptr(int64(*value))
}
