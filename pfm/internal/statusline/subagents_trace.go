package statusline

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The trace is explicitly opt-in: Claude Code discards successful commands'
// stderr. Two rotating 4 MiB generations keep it bounded across chats and
// installs. Records contain only row identity, counters and timing, never raw
// stdin, labels, descriptions, cwd, transcript paths, or rendered content.
// Pointer counters preserve the distinction between an absent field and zero.
// Bound strings before writing so even malformed input cannot exceed the cap
// with one record. One task per record avoids an unbounded task-list record.
type subagentTraceTask struct {
	ID                string `json:"id"`
	Type              string `json:"type,omitempty"`
	Status            string `json:"status,omitempty"`
	Model             string `json:"model,omitempty"`
	ContextWindowSize *int64 `json:"contextWindowSize,omitempty"`
	TokenCount        *int64 `json:"tokenCount,omitempty"`
}

type subagentTraceRecord struct {
	Event     string             `json:"event"`
	Time      time.Time          `json:"ts"`
	PID       int                `json:"pid"`
	Stage     string             `json:"stage,omitempty"`
	Bytes     int                `json:"bytes,omitempty"`
	Count     int                `json:"count"`
	ElapsedMS int64              `json:"elapsed_ms"`
	Valid     bool               `json:"valid"`
	Task      *subagentTraceTask `json:"task,omitempty"`
}

type subagentTrace struct {
	writer  io.WriteCloser
	stderr  io.Writer
	started time.Time
	stage   string
	rows    int
	failed  bool
}

func startSubagentTrace(home string, env paths.Env, stderr io.Writer) *subagentTrace {
	if env.Get(claudelaunch.EnvSubagentStatuslineTrace) != "1" {
		return nil
	}
	path := paths.SubagentStatuslineTrace(home)
	writer, err := obs.OpenRotatingLog(path, 2, 4, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "pfm statusline --subagents: open trace %s: %v\n", path, err)
		return nil
	}
	trace := &subagentTrace{writer: writer, stderr: stderr, started: clock.Real.Now(), stage: "input"}
	trace.record(subagentTraceRecord{Event: "start", Valid: true})
	return trace
}

func (trace *subagentTrace) record(record subagentTraceRecord) {
	if trace == nil || trace.failed {
		return
	}
	record.Time, record.PID = clock.Real.Now(), os.Getpid()
	if err := json.NewEncoder(trace.writer).Encode(record); err != nil {
		trace.failed = true
		fmt.Fprintf(trace.stderr, "pfm statusline --subagents: write structural trace: %v\n", err)
	}
}

func (trace *subagentTrace) input(raw []byte) {
	if trace == nil {
		return
	}
	var data struct {
		Tasks []subagentTraceTask `json:"tasks"`
	}
	err := json.Unmarshal(raw, &data)
	trace.record(subagentTraceRecord{Event: "input", Bytes: len(raw), Count: len(data.Tasks), Valid: err == nil})
	if err != nil {
		return // malformed input is recorded as invalid, never copied into the trace
	}
	for _, task := range data.Tasks {
		task.ID, task.Type = traceText(task.ID), traceText(task.Type)
		task.Status, task.Model = traceText(task.Status), traceText(task.Model)
		trace.record(subagentTraceRecord{Event: "task", Task: &task, Valid: true})
	}
}

func traceText(value string) string {
	const limit = 128
	if len(value) > limit {
		return value[:limit] + "..."
	}
	return value
}

func (trace *subagentTrace) step(stage string) {
	if trace != nil {
		trace.stage = stage
	}
}

func (trace *subagentTrace) finish() {
	trace.record(subagentTraceRecord{
		Event: "result", Stage: trace.stage, Count: trace.rows,
		ElapsedMS: clock.Real.Now().Sub(trace.started).Milliseconds(), Valid: trace.stage == "done",
	})
	if err := trace.writer.Close(); err != nil {
		fmt.Fprintf(trace.stderr, "pfm statusline --subagents: close structural trace: %v\n", err)
	}
}

// subagentTraceOutput observes only successful output bytes. The serving path
// writes its complete JSON-lines response in one write, including fault rows.
// The rendered body is deliberately not decoded or retained here.
type subagentTraceOutput struct {
	next  io.Writer
	trace *subagentTrace
}

func (output subagentTraceOutput) Write(body []byte) (int, error) {
	n, err := output.next.Write(body)
	for line := range strings.SplitSeq(string(bytes.TrimSpace(body[:n])), "\n") {
		if line == "" {
			continue
		}
		var row struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(line), &row) == nil && row.ID != "" {
			output.trace.rows++
			output.trace.record(subagentTraceRecord{
				Event: "row", Task: &subagentTraceTask{ID: traceText(row.ID)}, Valid: true,
			})
		}
	}
	return n, err
}
