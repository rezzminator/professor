package mockengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	pfmengine "hostops/pfm/internal/engine"
)

// claudeProjectSlug is the projects/<dir> Claude Code files a cwd's transcripts
// under, as pfm spells it when it LOCATES a transcript from a cwd
// (cmd/pfm/chat_satellite_command.go:664). NAMED GAP: the indexer's priority
// filter (internal/index/incremental.go:32) replaces only the separator, so
// the two disagree on a cwd containing a dot; pfm never encodes a cwd to
// discover files (index/walk.go is dir-agnostic).
var claudeProjectSlug = strings.NewReplacer("/", "-", ".", "-").Replace

// claudeInvocation is the Claude command line the mock understands: every
// flag pfm's spawn template, headless runner and reload path emit
// (internal/action/claude_spawn.go, internal/headless/run/run.go:520-549,
// internal/reload/reload.go:577), plus the positional prompt.
type claudeInvocation struct {
	headless             bool
	outputFormat         string
	model, effort        string
	settings             string
	systemPrompt         string
	systemPromptFile     string
	sessionID            string
	resumed              bool
	name                 string
	jsonSchema           string
	noSessionPersistence bool
	prompt               string
	subcommand           string
}

var (
	claudeValueFlags = map[string]bool{
		"--settings": true, flagModel: true, "--effort": true, "--system-prompt-file": true, "--system-prompt": true,
		"--append-system-prompt": true, "--session-id": true, "--name": true, "--output-format": true,
		"--json-schema": true, "--tools": true, "--setting-sources": true, "--permission-mode": true,
		"--add-dir": true, "--mcp-config": true, "--agent": true, "--input-format": true,
	}
	claudeResumeFlags = map[string]bool{"--resume": true, "-r": true}
)

func parseClaudeArgs(args []string) claudeInvocation {
	var call claudeInvocation
	for index := 0; index < len(args); {
		argument := args[index]
		name, inline, hasInline := strings.Cut(argument, "=")
		if !strings.HasPrefix(argument, "-") {
			if call.subcommand == "" && call.prompt == "" && argument == "agents" {
				call.subcommand = argument
			} else if call.prompt == "" {
				call.prompt = argument
			}
			index++
			continue
		}
		value := inline
		next := index + 1
		if claudeValueFlags[name] && !hasInline {
			if next < len(args) {
				value = args[next]
				next++
			}
		} else if claudeResumeFlags[name] && !hasInline {
			if next < len(args) && !strings.HasPrefix(args[next], "-") {
				value = args[next]
				next++
			}
		}
		switch name {
		case "-p", "--print":
			call.headless = true
		case "--output-format":
			call.outputFormat = value
		case flagModel:
			call.model = value
		case "--effort":
			call.effort = value
		case "--settings":
			call.settings = value
		case "--system-prompt":
			call.systemPrompt = value
		case "--system-prompt-file":
			call.systemPromptFile = value
		case "--session-id":
			call.sessionID = value
		case "--resume", "-r":
			call.resumed = true
			if value != "" {
				call.sessionID = strings.TrimSuffix(filepath.Base(value), ".jsonl")
			}
		case "--name":
			call.name = value
		case "--json-schema":
			call.jsonSchema = value
		case "--no-session-persistence":
			call.noSessionPersistence = true
		}
		index = next
	}
	return call
}

// claudeSession is the Claude engine behind the shared pane loop: one
// session's transcript, hooks, statusline and jail bindings.
type claudeSession struct {
	proc       *process
	call       claudeInvocation
	configDir  string
	sessionID  string
	transcript string
	model      string
	title      string
	hooks      hookSet
	statusCmd  string
	environ    []string
	seat       *seat
	turnUsage  Tokens
}

func newClaudeSession(proc *process, call claudeInvocation) (*claudeSession, error) {
	configDir := proc.env("CLAUDE_CONFIG_DIR")
	if configDir == "" {
		configDir = filepath.Join(proc.env("HOME"), ".claude")
	}
	hooks, statusCmd, err := loadHookDocument(filepath.Join(configDir, "settings.json"))
	if err != nil {
		return nil, err
	}
	session := &claudeSession{
		proc: proc, call: call, configDir: configDir, hooks: hooks, statusCmd: statusCmd,
		model: proc.script.Model, title: call.name, environ: os.Environ(),
	}
	if call.model != "" {
		session.model = call.model
	}
	session.sessionID = call.sessionID
	if session.sessionID == "" {
		session.sessionID = proc.script.SessionID
	}
	if session.sessionID == "" {
		session.sessionID = newUUID()
	}
	session.transcript = filepath.Join(configDir, "projects", claudeProjectSlug(proc.cwd), session.sessionID+".jsonl")
	return session, nil
}

func serveClaude(proc *process) int {
	call := parseClaudeArgs(proc.args)
	if call.subcommand == "agents" {
		// internal/reap/busy.go:103 asks `claude agents --json` for the
		// background agents a chat runs; the mock has none.
		fmt.Fprintln(proc.stdout, "[]")
		return 0
	}
	session, err := newClaudeSession(proc, call)
	if err != nil {
		warn(proc.stderr, "%v", err)
		return ExitUsage
	}
	if call.headless {
		return session.headless()
	}
	return runPane(proc, session, call.prompt, call.resumed)
}

// hookPayload is the envelope every Claude Code hook receives, with the
// per-event fields the handler table reads (map § 2a): prompt, tool_name and
// tool_input, source, reason.
type hookPayload struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	CWD            string          `json:"cwd"`
	PermissionMode string          `json:"permission_mode"`
	Event          string          `json:"hook_event_name"`
	Source         string          `json:"source,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Prompt         string          `json:"prompt,omitempty"`
	ToolName       string          `json:"tool_name,omitempty"`
	ToolInput      json.RawMessage `json:"tool_input,omitempty"`
}

func (session *claudeSession) payload(event string) hookPayload {
	return hookPayload{
		SessionID: session.sessionID, TranscriptPath: session.transcript, CWD: session.proc.cwd,
		PermissionMode: "bypassPermissions", Event: event,
	}
}

func (session *claudeSession) fire(event, subject string, payload hookPayload) ([]hookAnswer, error) {
	return session.hooks.fire(session.proc.ctx, event, subject, payload, session.proc.cwd, session.environ)
}

func (session *claudeSession) composerGlyph() string { return "❯" }

func (session *claudeSession) busyLine(elapsed time.Duration, usage Tokens) string {
	return claudeBusyLine(elapsed, usage)
}

// compactedLine is the receipt internal/inject/guards.go:20 matches
// ("compacted (" — Claude Code's own wording beyond that regex is not pinned).
func (session *claudeSession) compactedLine() string {
	return "✻ Conversation compacted (ctrl+o for history)"
}

func (session *claudeSession) start(resumed bool) error {
	source := sourceStartup
	if resumed {
		source = sourceResume
	}
	return session.begin(source)
}

// begin opens (or reopens) the session: the seat bindings, the title record,
// then SessionStart with its additionalContext folded into the transcript.
func (session *claudeSession) begin(source string) error {
	if err := touchFile(session.transcript); err != nil {
		return err
	}
	seat, err := bindSeat(session.proc, engineClaude, session.transcript)
	if err != nil {
		return err
	}
	session.seat = seat
	if session.title != "" && source == sourceStartup {
		if err := session.write(
			claudeRecord{Type: "custom-title", CustomTitle: session.title, SessionID: session.sessionID},
		); err != nil {
			return err
		}
	}
	payload := session.payload(hookSessionStart)
	payload.Source = source
	answers, err := session.fire(hookSessionStart, "", payload)
	if err != nil {
		return err
	}
	for index := range answers {
		if err := session.foldContext(&answers[index]); err != nil {
			return err
		}
	}
	return nil
}

// foldContext writes a hook's additionalContext the way Claude Code carries
// it: a meta user record every reader skips (internal/transcript/meta.go:150,
// internal/naming.IsJunkPrompt on the <system-reminder> prefix).
func (session *claudeSession) foldContext(answer *hookAnswer) error {
	context := answer.Specific.AdditionalContext
	if context == "" {
		return nil
	}
	return session.write(session.userRecord("<system-reminder>\n"+context+"\n</system-reminder>", true))
}

func (session *claudeSession) prompt(text string) (string, error) {
	payload := session.payload(hookUserPromptSubmit)
	payload.Prompt = text
	answers, err := session.fire(hookUserPromptSubmit, "", payload)
	if err != nil {
		return "", err
	}
	for index := range answers {
		if blocked, reason := answers[index].blocks(); blocked {
			return reason, nil
		}
		if err := session.foldContext(&answers[index]); err != nil {
			return "", err
		}
	}
	return "", nil
}

func (session *claudeSession) recordUser(text string) error {
	return session.write(session.userRecord(text, false))
}

func (session *claudeSession) recordAssistant(reply string, usage Tokens) error {
	session.turnUsage = usage
	return session.write(session.assistantRecord([]contentPart{{Type: blockText, Text: reply}}, usage, false))
}

func (session *claudeSession) tool(step Step) (string, error) {
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	payload := session.payload(hookPreToolUse)
	payload.ToolName, payload.ToolInput = step.Tool, input
	answers, err := session.fire(hookPreToolUse, step.Tool, payload)
	if err != nil {
		return "", err
	}
	use := contentPart{Type: "tool_use", ID: "toolu_" + newUUID()[:8], Name: step.Tool, Input: input}
	if err := session.write(
		session.assistantRecord([]contentPart{use}, session.proc.script.Tokens, false),
	); err != nil {
		return "", err
	}
	result := contentPart{Type: "tool_result", ToolUseID: use.ID, Content: "done"}
	denied := ""
	for index := range answers {
		if refused, reason := answers[index].denies(); refused {
			denied = reason
			result.Content, result.IsError = reason, true
		}
	}
	record := claudeRecord{
		Type: roleUser, SessionID: session.sessionID, Timestamp: stamp(time.Now()), CWD: session.proc.cwd,
		Message: &claudeMessage{Role: roleUser, Content: []contentPart{result}},
	}
	return denied, session.write(record)
}

// compact writes what Claude Code leaves after /compact: the boundary record
// internal/transcript/meta.go:161 reads for postTokens, then the summary as
// an isCompactSummary user turn every reader skips.
func (session *claudeSession) compact(step Step) error {
	post := step.PostTokens
	if post == 0 {
		post = 1000
	}
	boundary := claudeRecord{
		Type:      "system",
		Subtype:   "compact_boundary",
		SessionID: session.sessionID,
		Timestamp: stamp(time.Now()),
		CompactMetadata: &compactMetadata{
			Trigger:    "manual",
			PreTokens:  session.turnUsage.contextTokens(),
			PostTokens: post,
		},
	}
	if err := session.write(boundary); err != nil {
		return err
	}
	summary := session.userRecord("Summary of the conversation so far (fixture).", false)
	summary.IsCompactSummary = true
	return session.write(summary)
}

func (session *claudeSession) rename(name string) error {
	session.title = name
	return session.write(claudeRecord{Type: "custom-title", CustomTitle: name, SessionID: session.sessionID})
}

// background starts a sidechain. Its records carry isSidechain and live in
// the session's subagents/ file, below the two levels index/walk.go:56-63
// descends, so no reader counts them; the pane shows the agent row
// internal/inject/guards.go:24 exempts from the composer.
func (session *claudeSession) background(step Step) (string, error) {
	agentID := "agent-" + newUUID()[:8]
	sidechain := filepath.Join(strings.TrimSuffix(session.transcript, ".jsonl"), "subagents", agentID+".jsonl")
	ask := session.userRecord("Sidechain task for "+step.Name, false)
	ask.IsSidechain, ask.AgentID = true, agentID
	reply := session.assistantRecord(
		[]contentPart{{Type: blockText, Text: step.Status}},
		session.proc.script.Tokens,
		true,
	)
	reply.AgentID = agentID
	for _, record := range []*claudeRecord{&ask, &reply} {
		encoded, err := json.Marshal(record)
		if err != nil {
			return "", fmt.Errorf("encode sidechain record: %w", err)
		}
		if err := appendLine(sidechain, encoded); err != nil {
			return "", err
		}
	}
	status := step.Status
	if status == "" {
		status = "running"
	}
	return "❯ ● " + step.Name + "  " + status + " (background)", nil
}

func (session *claudeSession) mcp(Step) error {
	return fmt.Errorf("claude MCP client calls are not scripted here: pfm registers its servers for Claude " +
		"through mcpServers (internal/installer/mcp_accounts.go:230) and no pfm reader consumes the handshake")
}

func (session *claudeSession) clear() error {
	if err := session.finish("clear"); err != nil {
		return err
	}
	session.sessionID = newUUID()
	session.transcript = filepath.Join(
		session.configDir,
		"projects",
		claudeProjectSlug(session.proc.cwd),
		session.sessionID+".jsonl",
	)
	return session.begin("clear")
}

func (session *claudeSession) finish(reason string) error {
	payload := session.payload(hookSessionEnd)
	payload.Reason = reason
	_, err := session.fire(hookSessionEnd, "", payload)
	if session.seat != nil {
		err = errors.Join(err, session.seat.release())
		session.seat = nil
	}
	return err
}

// statusLine feeds the statusLine command the struct
// internal/statusline/render.go:41-84 decodes.
func (session *claudeSession) statusLine(usage Tokens) (string, error) {
	window := usage.ContextWindow
	if window == 0 {
		window = session.proc.script.Tokens.ContextWindow
	}
	used := usage.contextTokens()
	percent := 0.0
	if window > 0 {
		percent = float64(used) / float64(window) * 100
	}
	input := statusInput{
		SessionID:      session.sessionID,
		TranscriptPath: session.transcript,
		CWD:            session.proc.cwd,
		SessionName:    session.title,
	}
	input.Model.ID, input.Model.DisplayName = session.model, claudeDisplayName(session.model)
	input.Workspace.CurrentDir = session.proc.cwd
	input.ContextWindow.UsedPercentage = percent
	input.ContextWindow.TotalInputTokens, input.ContextWindow.TotalOutputTokens = used, usage.Output
	input.ContextWindow.CurrentUsage.InputTokens = usage.Input
	input.ContextWindow.CurrentUsage.CacheReadInputTokens = usage.CacheRead
	input.ContextWindow.CurrentUsage.CacheCreationInputTokens = usage.CacheCreation
	input.Cost.TotalCostUSD = usageCost(usage)
	input.Effort.Level = session.call.effort
	return invokeStatusLine(session.proc.ctx, session.statusCmd, input, session.proc.cwd, session.environ)
}

// claudeDisplayName is the family word Claude Code shows for a model id.
func claudeDisplayName(model string) string {
	lower := strings.ToLower(model)
	for _, family := range []string{"Opus", "Sonnet", "Haiku"} {
		if strings.Contains(lower, strings.ToLower(family)) {
			return family
		}
	}
	return pfmengine.MustLookup(pfmengine.Claude).Short
}

type statusInput struct {
	Model struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
	} `json:"model"`
	Workspace struct {
		CurrentDir  string `json:"current_dir"`
		GitWorktree string `json:"git_worktree,omitempty"`
	} `json:"workspace"`
	CWD           string `json:"cwd"`
	ContextWindow struct {
		UsedPercentage    float64 `json:"used_percentage"`
		TotalInputTokens  int64   `json:"total_input_tokens"`
		TotalOutputTokens int64   `json:"total_output_tokens"`
		CurrentUsage      struct {
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			InputTokens              int64 `json:"input_tokens"`
		} `json:"current_usage"`
	} `json:"context_window"`
	Cost struct {
		TotalCostUSD      float64 `json:"total_cost_usd"`
		TotalDurationMS   int64   `json:"total_duration_ms"`
		TotalLinesAdded   int64   `json:"total_lines_added"`
		TotalLinesRemoved int64   `json:"total_lines_removed"`
	} `json:"cost"`
	Effort struct {
		Level string `json:"level"`
	} `json:"effort"`
	SessionName    string `json:"session_name,omitempty"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
}

// usageCost is a small positive receipt proportional to the tokens — pfm reads
// the number, never its tariff.
func usageCost(usage Tokens) float64 {
	return float64(usage.Input+usage.CacheRead+usage.CacheCreation+usage.Output)*0.000001 + 0.0001
}

func (usage Tokens) contextTokens() int64 { return usage.Input + usage.CacheRead + usage.CacheCreation }

// headless is `claude -p`: the prompt from argv or stdin, one turn, the JSON
// envelope internal/headless/run/run.go:677-683 decodes.
func (session *claudeSession) headless() int {
	proc := session.proc
	prompt := session.call.prompt
	if prompt == "" {
		content, err := io.ReadAll(io.LimitReader(proc.stdin, 1<<20))
		if err != nil {
			warn(proc.stderr, "read headless prompt: %v", err)
			return ExitUsage
		}
		prompt = strings.TrimSpace(string(content))
	}
	started := time.Now()
	step := proc.script.next()
	for !step.terminal() {
		if step.sideEffecting() {
			warn(proc.stderr, "-p fast-forwards past the scripted %s step — no hook fires, nothing is recorded "+
				"for it in headless mode", step.Type)
		}
		step = proc.script.next()
	}
	if step.Type == StepCrash {
		return step.ExitCode
	}
	reply, busyMS, usage := proc.script.turnReply(step)
	if !sleepOrCancel(proc.ctx, time.Duration(busyMS)*time.Millisecond) {
		return ExitUsage
	}
	if !session.call.noSessionPersistence {
		if err := session.recordUser(prompt); err != nil {
			warn(proc.stderr, "%v", err)
			return ExitUsage
		}
		if err := session.recordAssistant(reply, usage); err != nil {
			warn(proc.stderr, "%v", err)
			return ExitUsage
		}
	}
	if session.call.outputFormat != "json" {
		fmt.Fprintln(proc.stdout, reply)
		return 0
	}
	envelope := headlessEnvelope{
		Type:         "result",
		Subtype:      "success",
		DurationMS:   time.Since(started).Milliseconds(),
		NumTurns:     1,
		Result:       reply,
		SessionID:    session.sessionID,
		TotalCostUSD: usageCost(usage),
		Usage: claudeUsage{
			Input:         usage.Input,
			CacheCreation: usage.CacheCreation,
			CacheRead:     usage.CacheRead,
			Output:        usage.Output,
		},
		ModelUsage: map[string]modelUsage{session.model: {
			Input: usage.Input, Output: usage.Output, CacheRead: usage.CacheRead, CacheCreation: usage.CacheCreation,
		}},
	}
	if session.call.jsonSchema != "" && len(step.Structured) > 0 {
		envelope.StructuredOutput = step.Structured
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		warn(proc.stderr, "encode envelope: %v", err)
		return ExitUsage
	}
	fmt.Fprintln(proc.stdout, string(encoded))
	return 0
}

type headlessEnvelope struct {
	Type             string                `json:"type"`
	Subtype          string                `json:"subtype"`
	IsError          bool                  `json:"is_error"`
	DurationMS       int64                 `json:"duration_ms"`
	NumTurns         int                   `json:"num_turns"`
	Result           string                `json:"result"`
	StructuredOutput json.RawMessage       `json:"structured_output,omitempty"`
	SessionID        string                `json:"session_id"`
	TotalCostUSD     float64               `json:"total_cost_usd"`
	Usage            claudeUsage           `json:"usage"`
	ModelUsage       map[string]modelUsage `json:"modelUsage"`
}

type modelUsage struct {
	Input         int64 `json:"inputTokens"`
	Output        int64 `json:"outputTokens"`
	CacheRead     int64 `json:"cacheReadInputTokens"`
	CacheCreation int64 `json:"cacheCreationInputTokens"`
}

// Transcript records, in the keys internal/transcript/transcript.go:49-75,
// meta.go:47-84 and internal/index/claude.go:13-25 read.
type claudeRecord struct {
	Type             string           `json:"type"`
	UUID             string           `json:"uuid,omitempty"`
	SessionID        string           `json:"sessionId,omitempty"`
	Timestamp        string           `json:"timestamp,omitempty"`
	CWD              string           `json:"cwd,omitempty"`
	IsSidechain      bool             `json:"isSidechain"`
	IsMeta           bool             `json:"isMeta,omitempty"`
	IsCompactSummary bool             `json:"isCompactSummary,omitempty"`
	AgentID          string           `json:"agentId,omitempty"`
	Entrypoint       string           `json:"entrypoint,omitempty"`
	PromptSource     string           `json:"promptSource,omitempty"`
	CustomTitle      string           `json:"customTitle,omitempty"`
	Subtype          string           `json:"subtype,omitempty"`
	CompactMetadata  *compactMetadata `json:"compactMetadata,omitempty"`
	Message          *claudeMessage   `json:"message,omitempty"`
}

type compactMetadata struct {
	Trigger    string `json:"trigger"`
	PreTokens  int64  `json:"preTokens"`
	PostTokens int64  `json:"postTokens"`
}

type claudeMessage struct {
	Role    string       `json:"role"`
	Model   string       `json:"model,omitempty"`
	Content any          `json:"content"`
	Usage   *claudeUsage `json:"usage,omitempty"`
}

type claudeUsage struct {
	Input         int64 `json:"input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	Output        int64 `json:"output_tokens"`
}

type contentPart struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

func (session *claudeSession) userRecord(text string, meta bool) claudeRecord {
	return claudeRecord{
		Type:         roleUser,
		UUID:         newUUID(),
		SessionID:    session.sessionID,
		Timestamp:    stamp(time.Now()),
		CWD:          session.proc.cwd,
		IsMeta:       meta,
		Entrypoint:   "cli",
		PromptSource: "typed",
		Message:      &claudeMessage{Role: roleUser, Content: text},
	}
}

func (session *claudeSession) assistantRecord(parts []contentPart, usage Tokens, sidechain bool) claudeRecord {
	return claudeRecord{
		Type: roleAssistant, UUID: newUUID(), SessionID: session.sessionID, Timestamp: stamp(time.Now()),
		CWD: session.proc.cwd, IsSidechain: sidechain,
		Message: &claudeMessage{
			Role:    roleAssistant,
			Model:   session.model,
			Content: parts,
			Usage: &claudeUsage{
				Input:         usage.Input,
				CacheCreation: usage.CacheCreation,
				CacheRead:     usage.CacheRead,
				Output:        usage.Output,
			},
		},
	}
}

func (session *claudeSession) write(record claudeRecord) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode transcript record: %w", err)
	}
	return appendLine(session.transcript, encoded)
}
