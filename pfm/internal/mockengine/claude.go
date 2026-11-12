package mockengine

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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
	mcpConfig            string
	systemPrompt         string
	systemPromptFile     string
	sessionID            string
	resumeID             string
	resumed              bool
	fork                 bool
	name                 string
	jsonSchema           string
	noSessionPersistence bool
	prompt               string
	subcommand           string
}

const (
	flagMCPConfig   = "--mcp-config"
	flagSettings    = "--settings"
	flagForkSession = "--fork-session"
	flagSessionID   = "--session-id"
	flagResume      = "--resume"
	flagContinue    = "--continue"
)

var (
	claudeValueFlags = map[string]bool{
		flagSettings: true, flagModel: true, "--effort": true, "--system-prompt-file": true, "--system-prompt": true,
		"--append-system-prompt": true, "--session-id": true, "--name": true, "--output-format": true,
		"--json-schema": true, "--tools": true, "--setting-sources": true, "--permission-mode": true,
		"--add-dir": true, flagMCPConfig: true, "--agent": true, "--input-format": true,
	}
	// The flags whose value is optional, taken only when the next word is no flag: --resume's id and
	// --remote-control's chat name (internal/claudelaunch/remote_control.go), never a launch prompt.
	claudeOptionalValueFlags = map[string]bool{"--resume": true, "-r": true, "--remote-control": true}
)

func parseClaudeArgs(args []string) claudeInvocation {
	var call claudeInvocation
	for index := 0; index < len(args); {
		argument := args[index]
		name, inline, hasInline := strings.Cut(argument, "=")
		if !strings.HasPrefix(argument, "-") {
			if call.subcommand == "" && call.prompt == "" && (argument == "agents" || argument == claudeBGSpare) {
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
		} else if claudeOptionalValueFlags[name] && !hasInline {
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
		case flagSettings:
			call.settings = value
		case flagMCPConfig:
			call.mcpConfig = value
		case "--system-prompt":
			call.systemPrompt = value
		case "--system-prompt-file":
			call.systemPromptFile = value
		case flagSessionID:
			call.sessionID = value
		case flagResume, "-r":
			call.resumed = true
			if value != "" {
				call.resumeID = strings.TrimSuffix(filepath.Base(value), ".jsonl")
			}
		case flagForkSession:
			call.fork = true
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
	status     statusLineSetting
	// statusShown is the line the last landed statusline run printed.
	statusShown string
	environ     []string
	seat        *seat
	turnUsage   Tokens
	// asking is the tool call whose permission dialog is open (claude_permission.go).
	asking *permissionAsk
	// fork is a fork's transcript still unwritten before its first prompt (claude_resume.go).
	fork *pendingFork
	// snapshot is the tool shells' zsh snapshot, taken at the first Bash call (claude_shell_snapshot.go).
	snapshot string
	// job is set once the chat moved to a background job (claude_background_job.go).
	job *claudeJob
}

func newClaudeSession(proc *process, call claudeInvocation) (*claudeSession, error) {
	configDir := claudeConfigDir(proc)
	hooks, status, err := loadClaudeHooks(configDir, proc.cwd, call.settings)
	if err != nil {
		return nil, err
	}
	session := &claudeSession{
		proc: proc, call: call, configDir: configDir, hooks: hooks, status: status,
		model: proc.script.Model, title: call.name, environ: append(os.Environ(), "CLAUDE_PROJECT_DIR="+proc.cwd),
	}
	if call.resumed {
		parentID := call.resumeID
		if parentID == "" {
			parentID = call.sessionID
		}
		if !call.fork && parentID != "" {
			session.call.sessionID = parentID
			call.sessionID = parentID
		}
		if parentID != "" && !proc.script.NoTranscript {
			parent := filepath.Join(configDir, "projects", claudeProjectSlug(proc.cwd), parentID+".jsonl")
			if session.title == "" {
				session.title, err = claudeTranscriptTitle(parent)
				if err != nil {
					return nil, err
				}
			}
		}
	}
	if call.model != "" {
		session.model = call.model
	}
	session.sessionID = call.sessionID
	if call.resumed && call.fork && session.sessionID == "" {
		session.sessionID = uuid.NewV4().String()
	}
	if session.sessionID == "" {
		session.sessionID = proc.script.SessionID
	}
	if session.sessionID == "" {
		session.sessionID = uuid.NewV4().String()
	}
	session.transcript = filepath.Join(configDir, "projects", claudeProjectSlug(proc.cwd), session.sessionID+".jsonl")
	if call.resumed && call.fork && !proc.script.NoTranscript {
		parentID := call.resumeID
		if parentID == "" {
			parentID = call.sessionID
		}
		if parentID != "" {
			parent := filepath.Join(configDir, "projects", claudeProjectSlug(proc.cwd), parentID+".jsonl")
			session.fork = &pendingFork{parent: parent}
		}
	}
	return session, nil
}

// claudeSessionIDRefusal is the installed Claude Code's (2.1.322) line for
// --session-id with --resume and no --fork-session, read from its own run.
const claudeSessionIDRefusal = "Error: " + flagSessionID + " can only be used with " + flagContinue + " or " +
	flagResume + " if " + flagForkSession + " is also specified."

// launchCrashed is the exit code of a Claude pane the scenario's launch_crash ends at launch.
const launchCrashed = 1

// launchCrashBoot is how long a Claude Code that dies at launch runs first:
// its runtime loads before the fault it prints, so the server around its pane
// is up long enough for a launcher to keep the dead pane's last screen.
const launchCrashBoot = 500 * time.Millisecond

func serveClaude(proc *process) int {
	call := parseClaudeArgs(proc.args)
	if call.subcommand == "agents" {
		// internal/reap/busy.go:103 asks `claude agents --json` for the
		// background agents a chat runs; the mock has none.
		fmt.Fprintln(proc.stdout, "[]")
		return 0
	}
	if call.subcommand == claudeBGSpare {
		return serveBGSpare(proc)
	}
	if call.sessionID != "" && call.resumed && !call.fork {
		// Claude Code's own refusal: a resume keeps its session id unless it forks.
		fmt.Fprintln(proc.stderr, claudeSessionIDRefusal)
		return 1
	}
	if proc.script.LaunchCrash != "" && !call.headless {
		// A Claude Code that dies at launch prints its last words and exits before
		// any hook, record or prompt.
		sleepOrCancel(proc.ctx, launchCrashBoot)
		fmt.Fprintln(proc.stderr, proc.script.LaunchCrash)
		return launchCrashed
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

func (session *claudeSession) composerGlyph() string { return "❯" }

func (session *claudeSession) busyLine(elapsed time.Duration, usage Tokens) string {
	return claudeBusyLine(elapsed, usage)
}

// compactedLine is the receipt internal/call/guards.go:20 matches
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
	if !session.proc.script.NoTranscript && session.fork == nil {
		if err := touchFile(session.transcript); err != nil {
			return err
		}
	}
	seat, err := bindSeat(session.proc, engineClaude, session.transcript)
	if err != nil {
		return err
	}
	session.seat = seat
	// Claude carries a chat's title into its /clear successor's transcript.
	if session.title != "" && (source == sourceStartup || source == sourceClear || session.call.fork) {
		if err := session.write(
			claudeRecord{Type: claudeCustomTitle, CustomTitle: session.title, SessionID: session.sessionID},
		); err != nil {
			return err
		}
	}
	payload := session.payload(hookSessionStart)
	payload.Source = source
	// Claude matches a SessionStart hook against its source: startup, resume, clear or compact.
	answers, err := session.fire(hookSessionStart, source, payload)
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

func (session *claudeSession) recordUser(text string) error {
	if err := session.landFork(); err != nil {
		return err
	}
	return session.write(session.userRecord(text, false))
}

func (session *claudeSession) recordAssistant(reply string, usage Tokens) error {
	session.turnUsage = usage
	return session.write(session.assistantRecord([]contentPart{{Type: blockText, Text: reply}}, usage, false))
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
	return session.write(claudeRecord{Type: claudeCustomTitle, CustomTitle: name, SessionID: session.sessionID})
}

// taskNotification submits the prompt Claude Code opens a background agent's result turn with: its
// UserPromptSubmit carries the <task-notification> prompt and no other mark (internal/hookentry
// herdr_seat.go taskNotificationPrompt).
func (session *claudeSession) taskNotification(name string) error {
	_, err := session.prompt("<task-notification>\n<task-id>" + name + "</task-id>\n<status>completed</status>\n" +
		"<summary>Agent \"" + name + "\" completed</summary>\n</task-notification>")
	return err
}

// background starts a sidechain. Its records carry isSidechain and live in
// the session's subagents/ file, below the two levels index/walk.go:56-63
// descends, so no reader counts them; the pane shows the agent row
// internal/call/guards.go:24 exempts from the composer.
func (session *claudeSession) background(step Step) (string, error) {
	agentID := "agent-" + uuid.NewV4().String()[:8]
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
		if !session.proc.script.NoTranscript {
			if err := appendLine(sidechain, encoded); err != nil {
				return "", err
			}
		}
	}
	status := step.Status
	if status == "" {
		status = "running"
	}
	return "❯ ● " + step.Name + "  " + status + " (background)", nil
}

func (session *claudeSession) mcp(step Step) error {
	if step.Tool == "" {
		return fmt.Errorf("claude MCP client calls are not scripted here: pfm registers its servers for Claude " +
			"through the launch registry (internal/claudelaunch/render.go) and no pfm reader consumes the handshake")
	}
	input := step.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	use := contentPart{Type: "tool_use", ID: "toolu_" + uuid.NewV4().String()[:8], Name: step.Tool, Input: input}
	if err := session.write(
		session.assistantRecord([]contentPart{use}, session.proc.script.Tokens, false),
	); err != nil {
		return err
	}
	answer, callErr := session.callMCP(step, input)
	if callErr != nil {
		answer = &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: callErr.Error()}}}
	}
	encoded, err := json.Marshal(answer)
	if err != nil {
		return fmt.Errorf("encode MCP %s result: %w", step.Tool, err)
	}
	if err := session.proc.recorder.write("mcp-call-"+step.Tool+".json", string(encoded)+"\n"); err != nil {
		return err
	}
	result := contentPart{Type: blockToolResult, ToolUseID: use.ID, Content: string(encoded), IsError: answer.IsError}
	return session.write(claudeRecord{
		Type: roleUser, SessionID: session.sessionID, Timestamp: stamp(time.Now()), CWD: session.proc.cwd,
		Message: &claudeMessage{Role: roleUser, Content: []contentPart{result}},
	})
}

func (session *claudeSession) callMCP(step Step, input json.RawMessage) (*mcp.CallToolResult, error) {
	name := step.Server
	if name == "" {
		name = pfmconfig.MCPRegistrationName(pfmconfig.MCPServerChat)
	}
	config := session.call.mcpConfig
	content := []byte(config)
	if !strings.HasPrefix(strings.TrimSpace(config), "{") {
		path := config
		if !filepath.IsAbs(path) && path != "" {
			path = filepath.Join(session.proc.cwd, path)
		}
		var err error
		content, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %s %q: %w", flagMCPConfig, path, err)
		}
	}
	var document struct {
		Servers map[string]struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("decode %s %q: %w", flagMCPConfig, config, err)
	}
	server, present := document.Servers[name]
	if !present || server.Command == "" || (server.Type != "" && server.Type != "stdio") {
		return nil, fmt.Errorf("%s %q has no stdio mcpServers.%s command", flagMCPConfig, config, name)
	}
	bounded, cancel := mcpBoundedContext(session.proc.ctx)
	defer cancel()
	command := exec.CommandContext(bounded, server.Command, server.Args...)
	command.Dir = session.proc.cwd
	command.Env = append(append([]string(nil), session.environ...),
		"TMUX="+session.proc.env("TMUX"), "TMUX_PANE="+session.proc.env("TMUX_PANE"),
		"CLAUDE_CODE_SESSION_ID="+session.sessionID)
	client := mcp.NewClient(&mcp.Implementation{Name: "mock-engine", Version: session.proc.script.Version}, nil)
	connection, err := client.Connect(
		bounded,
		discoveringTransport{&mcp.CommandTransport{Command: command}},
		claudeMCPHandshake,
	)
	if err != nil {
		return nil, fmt.Errorf("initialize MCP %s with %s: %w", name, server.Command, err)
	}
	defer func() {
		if err := connection.Close(); err != nil {
			warn(session.proc.stderr, "close MCP %s session: %v", name, err)
		}
	}()
	answer, err := connection.CallTool(bounded, &mcp.CallToolParams{Name: step.Tool, Arguments: input})
	if err != nil {
		return nil, fmt.Errorf("tools/call %s on MCP %s: %w", step.Tool, name, err)
	}
	return answer, nil
}

func (session *claudeSession) clear() error {
	if err := session.finish("clear"); err != nil {
		return err
	}
	session.sessionID = uuid.NewV4().String()
	session.transcript = filepath.Join(
		session.configDir,
		"projects",
		claudeProjectSlug(session.proc.cwd),
		session.sessionID+".jsonl",
	)
	return session.begin(sourceClear)
}

// statusLine feeds the statusLine command the struct
// internal/statusline/render.go:41-84 decodes.
func (session *claudeSession) statusLine(usage Tokens) (string, error) {
	if session.proc.script.Quiet {
		return "", nil
	}
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
	if session.job != nil {
		input.SessionID, input.TranscriptPath = session.job.hostSession, session.job.hostTranscript
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
	line, landed, err := invokeStatusLine(session.proc.ctx, session.status.command, input, session.proc.cwd,
		session.environ, statusRunBound(session.status.refresh))
	if err != nil || !landed {
		// An aborted run keeps the line the last landed run painted; a fresh process has none.
		return session.statusShown, err
	}
	session.statusShown = line
	return line, nil
}

func (session *claudeSession) statusRefresh() time.Duration { return session.status.refresh }

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
	refusal, err := session.askAPI(prompt)
	if err != nil {
		warn(proc.stderr, "%v", err)
		return ExitUsage
	}
	if refusal != "" {
		return session.headlessAPIError(prompt, Step{Type: StepAPIError, Reply: refusal})
	}
	started := time.Now()
	running := proc.script.forPrompt(prompt)
	step := running.next()
	for !step.terminal() {
		if step.sideEffecting() {
			warn(proc.stderr, "-p fast-forwards past the scripted %s step — no hook fires, nothing is recorded "+
				"for it in headless mode", step.Type)
		}
		step = running.next()
	}
	if step.Type == StepCrash {
		return step.ExitCode
	}
	if step.Type == StepAPIError {
		return session.headlessAPIError(prompt, step)
	}
	reply, busyMS, usage := running.turnReply(step)
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
	if step.Type == StepTurn {
		if err := session.stop(); err != nil {
			warn(proc.stderr, "Stop: %v", err)
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
	if len(step.Structured) > 0 && (session.call.jsonSchema != "" || running != proc.script) {
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
