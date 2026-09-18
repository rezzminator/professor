package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
)

// compHooks is the component every `pfm internal` dispatch records under.
const compHooks = "hooks"

// hookCaptureBytes bounds what Hook keeps of a hook's stdout to read its
// JSON answer: a hook's decision is a few hundred bytes; a picker listing or
// a statusline render is passed through and never kept beyond this head.
const hookCaptureBytes = 4096

// Hook is the hooks door (spec § Middleware, `hooks`): it wraps one
// `pfm internal <name>` dispatch. The returned writer replaces the hook's
// stdout — everything still reaches the caller, and a head is kept so the
// hook's own JSON answer (`decision`/`reason`, or Claude Code's PreToolUse
// `permissionDecision`/`permissionDecisionReason`) can be read back. The
// returned function is called once with the exit code and writes one record:
// hook, decision, reason, exit, dur_ms. Without a JSON answer the exit code
// decides — 0 allow, 2 block (Claude Code's blocking code), anything else an
// error, at ERROR. Stdin — the hook's payload, a prompt or a transcript —
// is never read here.
func Hook(ctx context.Context, name string, stdout io.Writer) (io.Writer, func(exitCode int)) {
	started := current(ctx).timing.Now()
	capture := &headCapture{next: stdout}
	return capture, func(exitCode int) {
		decision, reason := hookAnswer(capture.head.Bytes())
		level := slog.LevelInfo
		if decision == "" {
			switch exitCode {
			case 0:
				decision = "allow"
			case 2:
				decision = "block"
			default:
				decision, level = "error", slog.LevelError
			}
		}
		attrs := []slog.Attr{
			slog.String("hook", name),
			slog.String("decision", decision),
			slog.Int(FieldExit, exitCode),
		}
		if reason != "" {
			attrs = append(attrs, slog.String("reason", reason))
		}
		record(ctx, compHooks, "hooks.run", level, started, nil, attrs...)
	}
}

// hookAnswer reads a hook's JSON answer out of its stdout head; anything
// that is not one — a listing, a render, nothing — yields no fields at all,
// so a fragment of output can never pose as a reason.
func hookAnswer(head []byte) (decision, reason string) {
	var answer struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
		Specific struct {
			Decision string `json:"permissionDecision"`
			Reason   string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(head), &answer); err != nil {
		return "", ""
	}
	if answer.Decision != "" {
		return answer.Decision, answer.Reason
	}
	return answer.Specific.Decision, answer.Specific.Reason
}

// headCapture passes every write through and keeps the first
// hookCaptureBytes of them.
type headCapture struct {
	next io.Writer
	head bytes.Buffer
}

func (capture *headCapture) Write(data []byte) (int, error) {
	if room := hookCaptureBytes - capture.head.Len(); room > 0 {
		capture.head.Write(data[:min(room, len(data))])
	}
	return capture.next.Write(data)
}
