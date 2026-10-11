package hookentry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

// tracerNudgeFreeCalls is how many untraced calls pass silently: the intake
// and a quick overview.
const tracerNudgeFreeCalls = 6

const tracerNudgeText = "TRACER-NUDGE: %d tool calls and no tracer yet. Finish the overview (file and symbol names, no body read), then send tracer your numbered questions (step 2 Trace); a brief's TRACE run: drill its nodes; nothing to trace: write TRACE skipped — {reason} to run.md."

const tracerNudgeBashTool = "Bash"

var tracerNudgeKey = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

type tracerNudgeHookInput struct {
	AgentID   string          `json:"agent_id"`
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
}

type tracerNudgeResponse struct {
	HookSpecificOutput struct {
		HookEventName     string `json:"hookEventName"`
		AdditionalContext string `json:"additionalContext"`
	} `json:"hookSpecificOutput"`
}

// TracerNudgeStateDir is the directory holding every key's call count and
// traced mark: a PFM_HOME jail's own tmp, else the host temp directory
// (paths has no temp location of its own), uid-scoped like its siblings
// because Linux's /tmp is shared.
func TracerNudgeStateDir(env paths.Env) string {
	base := os.TempDir()
	if home := env.Get(paths.EnvHome); home != "" {
		base = filepath.Join(home, "tmp")
	}
	return filepath.Join(base, "pfm-tracer-nudge-"+strconv.Itoa(os.Getuid()))
}

// TracerNudge is the fail-open PostToolUse hook wired into the flights-foreman
// frontmatter. It counts an agent's calls until it has sent tracer (or worked
// a tracer run, or declared TRACE skipped) and, from the 7th untraced call,
// adds a reminder to the agent's context. Every path returns 0; an error is
// named on stderr and in the additionalContext, never silence.
func TracerNudge(input io.Reader, stdout, stderr io.Writer, stateDir string) int {
	context, err := tracerNudgeContext(input, stateDir)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal tracer-nudge: %v\n", err)
		context = "TRACER-NUDGE-ERROR: " + err.Error()
	}
	if context == "" {
		return 0
	}
	var response tracerNudgeResponse
	response.HookSpecificOutput.HookEventName = "PostToolUse"
	response.HookSpecificOutput.AdditionalContext = context
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(response); err != nil {
		fmt.Fprintf(stderr, "pfm internal tracer-nudge: write response: %v\n", err)
	}
	return 0
}

func tracerNudgeContext(input io.Reader, stateDir string) (string, error) {
	raw, err := io.ReadAll(input)
	if err != nil {
		return "", fmt.Errorf("read hook payload: %w", err)
	}
	var hook tracerNudgeHookInput
	if err := json.Unmarshal(raw, &hook); err != nil {
		return "", fmt.Errorf("decode hook payload: %w", err)
	}
	key := hook.AgentID
	if key == "" {
		key = hook.SessionID
	}
	if !tracerNudgeKey.MatchString(key) {
		return "", fmt.Errorf("key %q (agent_id, else session_id) is not [A-Za-z0-9_-]{1,128}", key)
	}
	// A real directory owned by this uid, mode 0700: one another user planted
	// in a shared /tmp would let them redirect or silence every write below.
	if err := usagehook.EnsurePrivateDir(stateDir); err != nil {
		return "", fmt.Errorf("private state directory %s: %w", stateDir, err)
	}
	tracedPath := filepath.Join(stateDir, key+".traced")
	if info, err := os.Lstat(tracedPath); err == nil {
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("read traced mark: %s is not a regular file", tracedPath)
		}
		return "", nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("read traced mark: %w", err)
	}
	if tracerNudgeTraces(hook) {
		// O_EXCL makes the mark atomic: concurrent hooks of one agent all
		// succeed or find it already there, and none can unmake it.
		file, err := os.OpenFile(tracedPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600)
		if err == nil {
			err = file.Close()
		} else if errors.Is(err, fs.ErrExist) {
			err = nil
		}
		if err != nil {
			return "", fmt.Errorf("write traced mark: %w", err)
		}
		return "", nil
	}
	count, err := tracerNudgeCount(filepath.Join(stateDir, key+".calls"))
	if err != nil {
		return "", err
	}
	if count <= tracerNudgeFreeCalls {
		return "", nil
	}
	return fmt.Sprintf(tracerNudgeText, count), nil
}

// tracerNudgeCount appends one byte to the key's calls file and returns the
// file's size. Concurrent appends can make the count off by one, never lose a
// call's byte. The open follows no symlink and never blocks on a FIFO.
func tracerNudgeCount(path string) (int, error) {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return 0, fmt.Errorf("open call count: %w", err)
	}
	if _, err := file.Write([]byte{'.'}); err != nil {
		_ = file.Close()
		return 0, fmt.Errorf("append call count: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return 0, fmt.Errorf("stat call count: %w", err)
	}
	if err := file.Close(); err != nil {
		return 0, fmt.Errorf("close call count: %w", err)
	}
	return int(info.Size()), nil
}

// tracerNudgeTraces reports whether the call is the agent tracing: a tracer
// spawn, a tracetree.py command, or a TRACE skipped declaration. tool_input
// is any JSON; one that is not the expected object is simply not a trace.
func tracerNudgeTraces(hook tracerNudgeHookInput) bool {
	if strings.Contains(string(hook.ToolInput), "TRACE skipped") {
		return true
	}
	var input struct {
		SubagentType string `json:"subagent_type"`
		Command      string `json:"command"`
	}
	if json.Unmarshal(hook.ToolInput, &input) != nil {
		return false
	}
	if hook.ToolName == "Agent" || hook.ToolName == "Task" {
		return input.SubagentType == "tracer"
	}
	return hook.ToolName == tracerNudgeBashTool && strings.Contains(input.Command, "tracetree.py")
}
