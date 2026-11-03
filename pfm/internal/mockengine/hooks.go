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
	permissionAllow      = "allow"
	permissionDeny       = "deny"
	toolResultDone       = "done"
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

// hookEntry is one `{"matcher": …, "hooks": [{"type":"command",keyCommand: …}]}`
// row of settings.json / --settings (internal/claudelaunch/render.go hookSettings).
type hookEntry struct {
	matcher  *regexp.Regexp
	handlers []hookHandler
}

type hookHandler struct {
	command string
	async   bool
	timeout time.Duration
}

const hookGateCompleted = "completed"

type hookGate struct {
	Event     string `json:"event"`
	Command   string `json:"command"`
	UntilGone string `json:"until_gone"`
}

func (gate *hookGate) record(rec recorder, payload []byte, phase string, exitCode int, runErr error) error {
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	record := struct {
		Event     string          `json:"event"`
		Command   string          `json:"command"`
		UntilGone string          `json:"until_gone"`
		Phase     string          `json:"phase"`
		Input     json.RawMessage `json:"input"`
		ExitCode  int             `json:"exit_code"`
		Error     string          `json:"error"`
	}{gate.Event, gate.Command, gate.UntilGone, phase, payload, exitCode, message}
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return rec.write("hook-gates.jsonl", string(raw)+"\n")
}

// hookSet is every registered hook keyed by event.
type hookSet map[string][]hookEntry

func (set hookSet) merge(project hookSet) {
	for event, entries := range project {
		set[event] = append(set[event], entries...)
	}
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
		Decision                 struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message"`
		} `json:"decision"`
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
			entry := hookEntry{}
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
				timeout := hookTimeout
				if handler.Timeout > 0 {
					timeout = time.Duration(handler.Timeout * float64(time.Second))
				}
				entry.handlers = append(
					entry.handlers,
					hookHandler{command: handler.Command, async: handler.Async, timeout: timeout},
				)
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
	proc *process,
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
			var gate *hookGate
			rec := recorder{}
			if proc != nil && proc.script != nil {
				rec = proc.recorder
				for index := range proc.script.HookGates {
					candidate := &proc.script.HookGates[index]
					if candidate.Event == event && candidate.Command == handler.command {
						gate = candidate
						break
					}
				}
			}
			if handler.async {
				background, cancel := context.WithCancel(ctx)
				done := make(chan struct{})
				var answer hookAnswer
				var runErr error
				proc.backgroundMu.Lock()
				proc.background = append(proc.background, shellTask{cancel: func() {
					cancel()
					<-done
					// Serialize diagnostics with the session's foreground writes at teardown.
					if runErr != nil {
						warn(warnings, "async %s hook %q: %v", event, handler.command, runErr)
					} else if answer.exitCode != 0 {
						warn(
							warnings,
							"async %s hook %q: exit %d: %s",
							event,
							handler.command,
							answer.exitCode,
							answer.stderr,
						)
					}
				}, done: done})
				proc.backgroundMu.Unlock()
				go func() {
					defer close(done)
					defer cancel()
					answer, runErr = runHookCommand(
						background,
						handler.command,
						encoded,
						cwd,
						environ,
						handler.timeout,
						gate,
						rec,
					)
				}()
				continue
			}
			answer, err := runHookCommand(ctx, handler.command, encoded, cwd, environ, handler.timeout, gate, rec)
			if err != nil {
				failure := fmt.Errorf("%s hook %q: %w", event, handler.command, err)
				if event != hookStop {
					return answers, failure
				}
				failures = errors.Join(failures, failure)
				continue
			}
			answers = append(answers, answer)
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
	gate *hookGate,
	rec recorder,
) (answer hookAnswer, runErr error) {
	shell, err := deps.Resolve("sh")
	if err != nil {
		return hookAnswer{}, fmt.Errorf("sh not found: %w", err)
	}
	bounded, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if gate != nil {
		if err := gate.record(rec, payload, "entered", -1, nil); err != nil {
			return hookAnswer{}, err
		}
		phase := "cancelled"
		defer func() {
			code := -1
			if phase == hookGateCompleted {
				code = answer.exitCode
			}
			runErr = errors.Join(runErr, gate.record(rec, payload, phase, code, runErr))
		}()
		if err := waitHold(bounded, gate.UntilGone); err != nil {
			return hookAnswer{}, fmt.Errorf("hook gate: %w", err)
		}
		phase = hookGateCompleted
	}
	result, err := (deps.RealRunner{}).Run(bounded, []string{shell, "-c", command}, deps.RunOptions{
		Dir: cwd, Env: environ, Stdin: payload, WaitDelay: hookWaitDelay, ProcessGroup: true,
	})
	answer = hookAnswer{exitCode: result.ExitCode, stderr: strings.TrimSpace(string(result.Stderr))}
	if errors.Is(err, exec.ErrWaitDelay) {
		answer.exitCode = 0
	}
	if err != nil && !errors.Is(err, exec.ErrWaitDelay) && bounded.Err() == nil {
		return hookAnswer{}, fmt.Errorf("run: %w", err)
	}
	if errors.Is(bounded.Err(), context.DeadlineExceeded) {
		return hookAnswer{}, fmt.Errorf("timed out after %s", timeout)
	}
	if text := strings.TrimSpace(string(result.Stdout)); text != "" && strings.HasPrefix(text, "{") {
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
	if answer.Specific.PermissionDecision == permissionDeny {
		return true, answer.Specific.PermissionDecisionReason
	}
	if answer.exitCode == 2 {
		return true, answer.stderr
	}
	return false, ""
}

// invokeStatusLine feeds the statusLine command its input and returns the first
// line it printed — the line the pane shows under the composer.
func invokeStatusLine(
	ctx context.Context,
	command string,
	input any,
	cwd string,
	environ []string,
	rec recorder,
) (string, error) {
	if command == "" {
		return "", nil
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", fmt.Errorf("encode statusline input: %w", err)
	}
	shell, err := deps.Resolve("sh")
	if err != nil {
		return "", fmt.Errorf("statusline: sh not found: %w", err)
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, shell, "-c", command)
	cmd.Dir = cwd
	cmd.Env = environ
	cmd.Stdin = bytes.NewReader(encoded)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = hookWaitDelay
	runErr := cmd.Run()
	callback := statuslineCallback{
		Input:    encoded,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: -1,
		Argv:     cmd.Args,
	}
	if cmd.Process != nil {
		callback.PID = cmd.Process.Pid
	}
	if cmd.ProcessState != nil {
		callback.ExitCode = cmd.ProcessState.ExitCode()
	}
	for _, variable := range cmd.Environ() {
		if value, ok := strings.CutPrefix(variable, "CLAUDE_CONFIG_DIR="); ok {
			callback.ConfigDir = value
		}
	}
	receipt, err := json.Marshal(callback)
	if err != nil {
		return "", fmt.Errorf("encode statusline callback: %w", err)
	}
	recordErr := rec.write("statusline-callbacks.jsonl", string(receipt)+"\n")
	if runErr != nil {
		return "", errors.Join(
			fmt.Errorf("statusline %q: %w: %s", command, runErr, strings.TrimSpace(stderr.String())),
			recordErr,
		)
	}
	if recordErr != nil {
		return "", recordErr
	}
	line, _, _ := strings.Cut(stdout.String(), "\n")
	return line, nil
}

// warn writes a non-fatal complaint to the engine's stderr.
func warn(stderr io.Writer, format string, values ...any) {
	fmt.Fprintf(stderr, "mock-engine: "+format+"\n", values...)
}
