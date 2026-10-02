package mockengine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// pfm registers hooks through internal/claudelaunch/hooks.go HookTemplates.
// A project may also register Stop, which the mock fires at every turn end.
const (
	hookSessionStart     = "SessionStart"
	hookUserPromptSubmit = "UserPromptSubmit"
	hookPreToolUse       = "PreToolUse"
	hookPostToolUse      = "PostToolUse"
	hookSessionEnd       = "SessionEnd"
	hookStop             = "Stop"
)

// hookTimeout bounds a handler that never answers; a real engine's default is
// longer, but a test hook that hangs must fail the test, not stall it.
const hookTimeout = 30 * time.Second

// hookEntry is one `{"matcher": …, "hooks": [{"type":"command","command": …}]}`
// row of settings.json / --settings (internal/claudelaunch/render.go hookSettings).
type hookEntry struct {
	matcher  *regexp.Regexp
	handlers []hookHandler
	timeout  time.Duration
}

type hookHandler struct {
	command string
	async   bool
}

// hookSet is every registered hook keyed by event.
type hookSet map[string][]hookEntry

func (set hookSet) merge(project hookSet) {
	for event, entries := range project {
		set[event] = append(set[event], entries...)
	}
}

// hookPayload carries the session identity and the event's prompt or tool.
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
	ToolUseID      string          `json:"tool_use_id,omitempty"`
	ToolResponse   json.RawMessage `json:"tool_response,omitempty"`
	DurationMS     *int64          `json:"duration_ms,omitempty"`
}

func (session *claudeSession) payload(event string) hookPayload {
	return hookPayload{
		SessionID: session.sessionID, TranscriptPath: session.transcript, CWD: session.proc.cwd,
		PermissionMode: "bypassPermissions", Event: event,
	}
}

func (session *claudeSession) fire(event, subject string, payload hookPayload) ([]hookAnswer, error) {
	if session.proc.script.Quiet {
		return nil, nil
	}
	return session.hooks.fire(
		session.proc.ctx, event, subject, payload, session.proc.cwd, session.environ, session.proc.stderr,
	)
}

// foldContext writes a meta user record every pfm transcript reader skips.
func (session *claudeSession) foldContext(answer *hookAnswer) error {
	reminder := answer.Specific.AdditionalContext
	if reminder == "" {
		return nil
	}
	return session.write(session.userRecord("<system-reminder>\n"+reminder+"\n</system-reminder>", true))
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

func (session *claudeSession) stop() error {
	if session.proc.script.Quiet || len(session.hooks[hookStop]) == 0 {
		return nil
	}
	answers, fireErr := session.fire(hookStop, "", session.payload(hookStop))
	type recordedAnswer struct {
		ExitCode int        `json:"exit_code"`
		Stderr   string     `json:"stderr,omitempty"`
		Answer   hookAnswer `json:"answer"`
	}
	record := struct {
		Answers []recordedAnswer `json:"answers"`
		Error   string           `json:"error,omitempty"`
	}{}
	for index := range answers {
		record.Answers = append(
			record.Answers,
			recordedAnswer{answers[index].exitCode, answers[index].stderr, answers[index]},
		)
	}
	if fireErr != nil {
		record.Error = fireErr.Error()
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return errors.Join(fireErr, fmt.Errorf("encode Stop hook answers: %w", err))
	}
	return errors.Join(fireErr, session.proc.recorder.write("hook-Stop.json", string(encoded)+"\n"))
}

// hookAnswer is what a handler said back: its exit code, stderr, and the JSON
// decision on stdout when it printed one.
type hookAnswer struct {
	exitCode int
	stderr   string
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	Specific struct {
		Event                    string `json:"hookEventName"`
		AdditionalContext        string `json:"additionalContext"`
		PermissionDecision       string `json:"permissionDecision"`
		PermissionDecisionReason string `json:"permissionDecisionReason"`
	} `json:"hookSpecificOutput"`
	SystemMessage string `json:"systemMessage"`
}

// hookDocument is the settings.json / hooks.json subset the mock reads.
type hookDocument struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type    string  `json:"type"`
			Command string  `json:"command"`
			Timeout float64 `json:"timeout"`
			Async   bool    `json:"async"`
		} `json:"hooks"`
	} `json:"hooks"`
	StatusLine struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"statusLine"`
}

// loadHookDocument reads a settings.json. A missing file is an
// empty registration; a present file that does not parse is an error.
func loadHookDocument(path string) (hookSet, string, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return hookSet{}, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	return decodeHookDocument(content, path)
}

func decodeHookDocument(content []byte, source string) (hookSet, string, error) {
	var document hookDocument
	if err := json.Unmarshal(content, &document); err != nil {
		return nil, "", fmt.Errorf("decode %s: %w", source, err)
	}
	set := hookSet{}
	for event, entries := range document.Hooks {
		for _, raw := range entries {
			entry := hookEntry{timeout: hookTimeout}
			if raw.Matcher != "" && raw.Matcher != "*" {
				pattern, err := regexp.Compile(raw.Matcher)
				if err != nil {
					return nil, "", fmt.Errorf("%s: %s matcher %q: %w", source, event, raw.Matcher, err)
				}
				entry.matcher = pattern
			}
			for _, handler := range raw.Hooks {
				if handler.Type != "command" || handler.Command == "" {
					continue
				}
				entry.handlers = append(entry.handlers, hookHandler{command: handler.Command, async: handler.Async})
				if handler.Timeout > 0 {
					entry.timeout = time.Duration(handler.Timeout * float64(time.Second))
				}
			}
			if len(entry.handlers) > 0 {
				set[event] = append(set[event], entry)
			}
		}
	}
	statusCommand := ""
	if document.StatusLine.Type == "command" {
		statusCommand = document.StatusLine.Command
	}
	return set, statusCommand, nil
}

// loadClaudeHooks applies Claude's account, project and launch settings layers.
func loadClaudeHooks(configDir, cwd, settings string) (hookSet, string, error) {
	hooks, status, err := loadHookDocument(filepath.Join(configDir, "settings.json"))
	if err != nil {
		return nil, "", err
	}
	project, projectStatus, err := loadHookDocument(filepath.Join(cwd, ".claude", "settings.json"))
	if err != nil {
		return nil, "", err
	}
	hooks.merge(project)
	if projectStatus != "" {
		status = projectStatus
	}
	if settings == "" {
		return hooks, status, nil
	}
	value := strings.TrimSpace(settings)
	var inline hookSet
	var inlineStatus string
	if strings.HasPrefix(value, "{") {
		inline, inlineStatus, err = decodeHookDocument([]byte(settings), flagSettings)
	} else {
		content, readErr := os.ReadFile(settings)
		if readErr != nil {
			return nil, "", fmt.Errorf("read %s %s: %w", flagSettings, settings, readErr)
		}
		inline, inlineStatus, err = decodeHookDocument(content, flagSettings+" "+settings)
	}
	if err != nil {
		return nil, "", err
	}
	hooks.merge(inline)
	if inlineStatus != "" {
		status = inlineStatus
	}
	return hooks, status, nil
}

// fire runs every handler registered for event whose matcher accepts subject
// (the tool name for PreToolUse, "" elsewhere), feeding each the payload on
// stdin, and returns their answers in registration order. A handler that
// cannot be started is an error; one that exits non-zero is an answer whose
// exit code the caller interprets (2 blocks, the rest are noise).
func (set hookSet) fire(
	ctx context.Context,
	event, subject string,
	payload any,
	cwd string,
	environ []string,
	warnings io.Writer,
) ([]hookAnswer, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", event, err)
	}
	answers := make([]hookAnswer, 0)
	seen := map[string]bool{}
	var failures error
	for _, entry := range set[event] {
		if entry.matcher != nil && !entry.matcher.MatchString(subject) {
			continue
		}
		for _, handler := range entry.handlers {
			if seen[handler.command] {
				continue
			}
			seen[handler.command] = true
			answer, err := runHookCommand(ctx, handler.command, encoded, cwd, environ, entry.timeout)
			if err != nil {
				failure := fmt.Errorf("%s hook %q: %w", event, handler.command, err)
				if handler.async {
					// Real Claude runs an async handler in the background and
					// ignores its outcome: the failure is reported, never fatal.
					warn(warnings, "async %v", failure)
					continue
				}
				if event != hookStop {
					return answers, failure
				}
				failures = errors.Join(failures, failure)
				continue
			}
			if !handler.async {
				answers = append(answers, answer)
			}
		}
	}
	return answers, failures
}

// hookWaitDelay bounds output pipes held open after the hook exits or is killed.
const hookWaitDelay = 250 * time.Millisecond

func runHookCommand(
	ctx context.Context,
	command string,
	payload []byte,
	cwd string,
	environ []string,
	timeout time.Duration,
) (hookAnswer, error) {
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(bounded, deps.Executable("sh"), "-c", command)
	cmd.Dir = cwd
	cmd.Env = environ
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = hookWaitDelay
	err := cmd.Run()
	answer := hookAnswer{stderr: strings.TrimSpace(stderr.String())}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.Is(err, exec.ErrWaitDelay):
	case errors.As(err, &exitErr):
		answer.exitCode = exitErr.ExitCode()
	default:
		return hookAnswer{}, fmt.Errorf("run: %w", err)
	}
	if errors.Is(bounded.Err(), context.DeadlineExceeded) {
		return hookAnswer{}, fmt.Errorf("timed out after %s", timeout)
	}
	if text := strings.TrimSpace(stdout.String()); text != "" && strings.HasPrefix(text, "{") {
		if err := json.Unmarshal([]byte(text), &answer); err != nil {
			return hookAnswer{}, fmt.Errorf("decode stdout %q: %w", text, err)
		}
	}
	return answer, nil
}

// blocks reports whether a UserPromptSubmit answer stops the prompt, and why.
func (answer hookAnswer) blocks() (bool, string) {
	if answer.Decision == "block" {
		return true, answer.Reason
	}
	if answer.exitCode == 2 {
		return true, answer.stderr
	}
	return false, ""
}

// denies reports whether a PreToolUse answer refuses the tool, and why.
func (answer hookAnswer) denies() (bool, string) {
	if answer.Specific.PermissionDecision == "deny" {
		return true, answer.Specific.PermissionDecisionReason
	}
	if answer.exitCode == 2 {
		return true, answer.stderr
	}
	return false, ""
}

// invokeStatusLine feeds the statusLine command its input and returns the first
// line it printed — the line the pane shows under the composer.
func invokeStatusLine(ctx context.Context, command string, input any, cwd string, environ []string) (string, error) {
	if command == "" {
		return "", nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode statusline input: %w", err)
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, deps.Executable("sh"), "-c", command)
	cmd.Dir = cwd
	cmd.Env = environ
	cmd.Stdin = bytes.NewReader(encoded)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("statusline %q: %w: %s", command, err, strings.TrimSpace(stderr.String()))
	}
	line, _, _ := strings.Cut(stdout.String(), "\n")
	return line, nil
}

// warn writes a non-fatal complaint to the engine's stderr.
func warn(stderr io.Writer, format string, values ...any) {
	fmt.Fprintf(stderr, "mock-engine: "+format+"\n", values...)
}
