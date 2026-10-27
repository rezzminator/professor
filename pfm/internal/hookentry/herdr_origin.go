package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/tmux"
)

func reportToHerdr(args []string, stdin io.Reader, stderr io.Writer, deps herdrDeps) int {
	reporter := &herdrReporter{deps: deps, stderr: stderr, seq: uint64(deps.now().UnixNano())}
	options, err := parseHerdrOptions(args)
	if err != nil {
		reporter.warn("pfm internal herdr-state: %v\n%s", err, herdrStateUsage)
		return 1
	}
	reporter.options = options
	reporter.session = options.session
	var payload herdrPayload
	if !options.event.FiredByTmux() {
		// Read every identity field before reporting, even after a large response.
		payload, err = readHerdrPayload(stdin)
		if err != nil {
			const decodeFailure = "pfm internal herdr-state %s engine=%s: decode hook payload (skipped): %v"
			reporter.warn(decodeFailure, options.event, options.engine, err)
			return 0
		}
		if threadWithoutRollout(options.engine, payload) {
			return 0
		}
	}
	if payload.AgentID != "" && options.event != herdr.EventAgentStart && options.event != herdr.EventAgentStop {
		return 0
	}
	if options.event == herdr.EventAgentStart || options.event == herdr.EventAgentStop {
		if options.engine != pfmengine.Claude || payload.SessionID == "" || payload.AgentID == "" {
			reporter.warn("Claude subagent lifecycle requires session_id and agent_id (skipped)")
			return 0
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), herdrDeadline)
	defer cancel()
	if reporter.codexSubagent(ctx, payload) {
		return 0
	}
	if err := reporter.runActivity(ctx, payload); err != nil {
		reporter.warn("pfm internal herdr-state %s: %v (fail-open)", options.event, err)
	}
	return 0
}

// tmuxOrigin settles the tmux server and pane this run reports through. A
// tmux-fired verb names its socket; an engine-fired event reads $TMUX
// ("socket,server pid,session") and $TMUX_PANE. Codex never comes here: its
// hooks can run in the shared daemon's environment (runCodex).
func (reporter *herdrReporter) tmuxOrigin() {
	reporter.tmuxPane = reporter.deps.getenv("TMUX_PANE")
	if reporter.options.socket == "" {
		reporter.options.socket = tmux.SocketPath(reporter.deps.getenv("TMUX"))
	}
}

// herdrHookBytes bounds the whole stream, including discarded tool responses.
// Decoder.Token skips unknown values without retaining the whole payload. A
// single token and the selected fields are still bounded by this byte budget.
const (
	herdrHookBytes = 64 << 20
	herdrSourceKey = "source"
)

func readHerdrPayload(input io.Reader) (payload herdrPayload, returnErr error) {
	limited := &io.LimitedReader{R: input, N: herdrHookBytes + 1}
	defer func() {
		if limited.N == 0 {
			returnErr = fmt.Errorf("hook payload exceeds %d bytes (skipped)", herdrHookBytes)
		}
	}()
	decoder := json.NewDecoder(limited)
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return payload, fmt.Errorf("empty or unreadable hook object: %w", err)
	}
	if token != json.Delim('{') {
		return payload, fmt.Errorf("hook payload must be an object")
	}
	fields := map[string]any{
		"hook_event_name": &payload.HookEventName, "session_id": &payload.SessionID,
		"agent_id": &payload.AgentID, herdrSourceKey: &payload.Source, "reason": &payload.Reason,
		"notification_type": &payload.NotificationType, "message": &payload.Message,
		"tool_name": &payload.ToolName, "tool_input": &payload.ToolInput,
		"is_interrupt": &payload.IsInterrupt, "title": &payload.Title, "prompt": &payload.Prompt,
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return payload, fmt.Errorf("read hook field: %w", err)
		}
		name, ok := key.(string)
		if !ok {
			return payload, fmt.Errorf("hook field is not a string: %v", key)
		}
		name = strings.ToLower(name)
		if name == "transcript_path" {
			var path *string
			if err := decoder.Decode(&path); err != nil {
				return payload, fmt.Errorf("read hook transcript_path: %w", err)
			}
			payload.TranscriptNull = path == nil
			payload.TranscriptPath = ""
			if path != nil {
				payload.TranscriptPath = *path
			}
		} else if field, known := fields[name]; known {
			if err := decoder.Decode(field); err != nil {
				return payload, fmt.Errorf("read hook %s: %w", name, err)
			}
		} else if err := skipHerdrValue(decoder, 0); err != nil {
			return payload, fmt.Errorf("skip hook %s: %w", name, err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return payload, fmt.Errorf("close hook object: %w", err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return payload, fmt.Errorf("after hook object: %w", errors.Join(err, errors.New("expected end of input")))
	}
	return payload, nil
}

func skipHerdrValue(decoder *json.Decoder, depth int) error {
	if depth > 1000 {
		return fmt.Errorf("hook nesting exceeds 1000 levels")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delim, ok := token.(json.Delim); ok && (delim == '{' || delim == '[') {
		for decoder.More() {
			if delim == '{' {
				if _, err := decoder.Token(); err != nil {
					return fmt.Errorf("read skipped key: %w", err)
				}
			}
			if err := skipHerdrValue(decoder, depth+1); err != nil {
				return err
			}
		}
		if _, err := decoder.Token(); err != nil {
			return fmt.Errorf("close skipped value: %w", err)
		}
	}
	return nil
}

// Only Codex's internal memory thread explicitly names a null rollout; an
// absent key or an empty string is not that thread.
func threadWithoutRollout(engine pfmengine.ID, payload herdrPayload) bool {
	return engine == pfmengine.Codex && payload.TranscriptNull
}

// realProcInfo reads a process's parent and command name through the ProcFS
// (the kernel's table on Linux, sysctl on macOS, a fixture root in a jail).
func realProcInfo(procfs gather.ProcFS) func(int) (int, string, error) {
	return func(pid int) (int, string, error) {
		stat, err := procfs.Stat(pid)
		if err != nil {
			return 0, "", fmt.Errorf("read parent of pid %d: %w", pid, err)
		}
		if identity, ok := procfs.(gather.ProcIdentity); ok {
			process, err := identity.ProcessIdentity(pid)
			if err != nil {
				return 0, "", fmt.Errorf("read command of pid %d: %w", pid, err)
			}
			return stat.ParentPID, process.Command, nil
		}
		argv, err := procfs.Cmdline(pid)
		if err != nil || len(argv) == 0 {
			return 0, "", fmt.Errorf("read command of pid %d: %w", pid, errors.Join(err, errors.New("no argv")))
		}
		return stat.ParentPID, argv[0], nil
	}
}

// tmuxReadFailure distinguishes a late SessionEnd from a failed live read.
// Killing a chat server disconnects all of its clients. Herdr clears the
// agent on foreground exit; stale inherited pane names cannot safely release it.
func (reporter *herdrReporter) tmuxReadFailure(readErr error) error {
	if reporter.options.event != herdr.EventEnd || !gather.IsChatSocketName(filepath.Base(reporter.options.socket)) {
		return readErr
	}
	_, statErr := os.Stat(reporter.options.socket)
	if statErr == nil {
		return readErr
	}
	if !os.IsNotExist(statErr) {
		return errors.Join(readErr, fmt.Errorf("confirm ended tmux socket %s: %w", reporter.options.socket, statErr))
	}
	message := fmt.Sprintf(
		"pfm internal herdr-state end: tmux server %s already gone; foreground client exited: %v",
		reporter.options.socket,
		readErr,
	)
	fmt.Fprintln(reporter.stderr, "INFO", message)
	if reporter.deps.logInfo != nil {
		if err := reporter.deps.logInfo(herdrNoSession, reporter.deps.now(), message); err != nil {
			return fmt.Errorf("persist ended-server info: %w", err)
		}
	}
	return nil
}
