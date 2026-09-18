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
	"regexp"
	"strings"
	"time"

	"hostops/pfm/internal/deps"
)

// Hook events the mock fires. pfm registers handlers on exactly these four
// (internal/installer/expected_hooks.go:90-103); there is no Stop hook.
const (
	hookSessionStart     = "SessionStart"
	hookUserPromptSubmit = "UserPromptSubmit"
	hookPreToolUse       = "PreToolUse"
	hookSessionEnd       = "SessionEnd"
)

// hookTimeout bounds a handler that never answers; a real engine's default is
// longer, but a test hook that hangs must fail the test, not stall it.
const hookTimeout = 30 * time.Second

// hookEntry is one `{"matcher": …, "hooks": [{"type":"command","command": …}]}`
// row of settings.json / hooks.json (installer/settings.go:730-743).
type hookEntry struct {
	matcher  *regexp.Regexp
	commands []string
	timeout  time.Duration
}

// hookSet is every registered hook keyed by event.
type hookSet map[string][]hookEntry

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
		} `json:"hooks"`
	} `json:"hooks"`
	StatusLine struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	} `json:"statusLine"`
}

// loadHookDocument reads a settings.json or hooks.json. A missing file is an
// empty registration; a present file that does not parse is an error.
func loadHookDocument(path string) (hookSet, string, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return hookSet{}, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("read %s: %w", path, err)
	}
	var document hookDocument
	if err := json.Unmarshal(content, &document); err != nil {
		return nil, "", fmt.Errorf("decode %s: %w", path, err)
	}
	set := hookSet{}
	for event, entries := range document.Hooks {
		for _, raw := range entries {
			entry := hookEntry{timeout: hookTimeout}
			if raw.Matcher != "" {
				pattern, err := regexp.Compile(raw.Matcher)
				if err != nil {
					return nil, "", fmt.Errorf("%s: %s matcher %q: %w", path, event, raw.Matcher, err)
				}
				entry.matcher = pattern
			}
			for _, handler := range raw.Hooks {
				if handler.Type != "command" || handler.Command == "" {
					continue
				}
				entry.commands = append(entry.commands, handler.Command)
				if handler.Timeout > 0 {
					entry.timeout = time.Duration(handler.Timeout * float64(time.Second))
				}
			}
			if len(entry.commands) > 0 {
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
) ([]hookAnswer, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s payload: %w", event, err)
	}
	answers := make([]hookAnswer, 0)
	for _, entry := range set[event] {
		if entry.matcher != nil && !entry.matcher.MatchString(subject) {
			continue
		}
		for _, command := range entry.commands {
			answer, err := runHookCommand(ctx, command, encoded, cwd, environ, entry.timeout)
			if err != nil {
				return answers, fmt.Errorf("%s hook %q: %w", event, command, err)
			}
			answers = append(answers, answer)
		}
	}
	return answers, nil
}

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
	err := cmd.Run()
	answer := hookAnswer{stderr: strings.TrimSpace(stderr.String())}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
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
