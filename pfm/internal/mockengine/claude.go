package mockengine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"uuid"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// claudeProjectSlug is the projects/<dir> Claude Code files a cwd's transcripts
// under, as pfm spells it when it LOCATES a transcript from a cwd
// (cmd/pfm/chat_satellite_command.go:664). NAMED GAP: the indexer's priority
// filter (internal/index/incremental.go:32) replaces only the separator, so
// the two disagree on a cwd containing a dot; pfm never encodes a cwd to
// discover files (index/walk.go is dir-agnostic).
var claudeProjectSlug = strings.NewReplacer("/", "-", ".", "-").Replace

func (session *claudeSession) mcpEnvironment() ([]string, error) {
	return session.proc.actionEnvironment(append(session.proc.terminalEnvironment(session.environ),
		"CLAUDE_CODE_SESSION_ID="+session.sessionID))
}

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
	continueLatest       bool
	fork                 bool
	name                 string
	remoteName           string
	jsonSchema           string
	noSessionPersistence bool
	prompt               string
	subcommand           string
}

const (
	flagMCPConfig   = "--mcp-config"
	flagSettings    = "--settings"
	flagForkSession = "--fork-session"
)

var (
	claudeValueFlags = map[string]bool{
		flagSettings: true, flagModel: true, "--effort": true, "--system-prompt-file": true, "--system-prompt": true,
		"--append-system-prompt": true, "--session-id": true, "--name": true, "--output-format": true,
		"--json-schema": true, "--tools": true, "--setting-sources": true, "--permission-mode": true,
		"--add-dir": true, flagMCPConfig: true, "--agent": true, "--input-format": true,
	}
	claudeOptionalValueFlags = map[string]bool{"--resume": true, "-r": true, "--remote-control": true}
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
		case "--session-id":
			call.sessionID = value
		case "--resume", "-r":
			call.resumed = true
			if value != "" {
				call.resumeID = strings.TrimSuffix(filepath.Base(value), ".jsonl")
			}
		case "--continue", "-c":
			call.resumed, call.continueLatest = true, true
		case flagForkSession:
			call.fork = true
		case "--name":
			call.name = value
		case "--remote-control":
			call.remoteName = value
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
	proc             *process
	call             claudeInvocation
	configDir        string
	sessionID        string
	transcript       string
	model            string
	title            string
	hooks            hookSet
	statusCmd        string
	environ          []string
	seat             *seat
	lastReply        string
	pendingToolID    string
	agentID          string
	agentType        string
	approvalDecision string
	turnUsage        Tokens
	responseSeen     bool
	backgroundModels []claudeBackgroundModel
}

func newClaudeSession(proc *process, call claudeInvocation) (*claudeSession, error) {
	configDir := claudeConfigDir(proc)
	if call.continueLatest {
		id, err := latestClaudeSession(filepath.Join(configDir, "projects", claudeProjectSlug(proc.cwd)))
		if err != nil {
			return nil, err
		}
		call.resumeID = id
	}
	hooks, statusCmd, err := loadClaudeHooks(configDir, proc.cwd, call.settings)
	if err != nil {
		return nil, err
	}
	session := &claudeSession{
		proc: proc, call: call, configDir: configDir, hooks: hooks, statusCmd: statusCmd,
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
			if err := seedClaudeFork(parent, session.transcript, session.sessionID); err != nil {
				return nil, err
			}
		}
	}
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
	defer func() {
		if err := session.removePresence(); err != nil {
			warn(proc.stderr, "remove native session: %v", err)
		}
	}()
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

func (session *claudeSession) recordUser(text string) error {
	return session.write(session.userRecord(text, false))
}

func (session *claudeSession) recordAssistant(reply string, usage Tokens) error {
	session.lastReply = reply
	session.turnUsage = usage
	session.responseSeen = true
	return session.write(session.assistantRecord([]contentPart{{Type: blockText, Text: reply}}, usage, false))
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

// usageCost is a small positive receipt proportional to the tokens — pfm reads
// the number, never its tariff.
func usageCost(usage Tokens) float64 {
	return float64(usage.Input+usage.CacheRead+usage.CacheCreation+usage.Output)*0.000001 + 0.0001
}

func (usage Tokens) contextTokens() int64 { return usage.Input + usage.CacheRead + usage.CacheCreation }

func (session *claudeSession) userRecord(text string, meta bool) claudeRecord {
	return claudeRecord{
		Type:         roleUser,
		UUID:         uuid.NewV4().String(),
		SessionID:    session.sessionID,
		Timestamp:    stamp(time.Now()),
		CWD:          session.proc.cwd,
		IsMeta:       meta,
		Entrypoint:   sourceCLI,
		PromptSource: "typed",
		Message:      &claudeMessage{Role: roleUser, Content: text},
	}
}

func (session *claudeSession) assistantRecord(parts []contentPart, usage Tokens, sidechain bool) claudeRecord {
	reason := "end_turn"
	for index := range parts {
		if parts[index].Type == blockToolUse {
			reason = blockToolUse
			break
		}
	}
	return claudeRecord{
		Type: roleAssistant, UUID: uuid.NewV4().String(), SessionID: session.sessionID, Timestamp: stamp(time.Now()),
		CWD: session.proc.cwd, IsSidechain: sidechain,
		Message: &claudeMessage{
			Role:       roleAssistant,
			StopReason: reason,
			Model:      session.model,
			Content:    parts,
			Usage: &claudeUsage{
				Input:         usage.Input,
				CacheCreation: usage.CacheCreation,
				CacheRead:     usage.CacheRead,
				Output:        usage.Output,
			},
		},
	}
}

func (session *claudeSession) write(record any) error {
	if session.proc.script.NoTranscript {
		return nil
	}
	if message, ok := record.(claudeRecord); ok && session.agentID != "" {
		message.AgentID, message.IsSidechain = session.agentID, true
		record = message
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode transcript record: %w", err)
	}
	return appendLine(session.transcript, encoded)
}
