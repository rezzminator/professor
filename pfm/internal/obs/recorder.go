package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"hostops/pfm/internal/clock"
)

// Record is one decoded activity line: the level and message slog wrote, and
// every field that survived Scrub. Tests assert on these, never on a format.
type Record struct {
	Level   string
	Message string
	Fields  map[string]any
}

// Field returns the value key carries and whether the record had it at all —
// so a test can tell "the field is absent" from "the field is empty".
func (record Record) Field(key string) (any, bool) {
	value, found := record.Fields[key]
	return value, found
}

// Recorder is the buffer destination obs.Test installs: it runs the SAME JSON
// handler and the SAME Scrub rule the file does, so what a test asserts on is
// byte-for-byte what the home's pfm.jsonl would have received.
type Recorder struct {
	mutex  sync.Mutex
	buffer bytes.Buffer
}

// Write appends one encoded record.
func (recorder *Recorder) Write(line []byte) (int, error) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.buffer.Write(line)
}

// Raw is everything written so far, as the file would hold it.
func (recorder *Recorder) Raw() string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return recorder.buffer.String()
}

// Records decodes what was written. A line that does not decode is reported
// as a record whose Message names the failure rather than being dropped: a
// malformed record is evidence, not absence.
func (recorder *Recorder) Records() []Record {
	decoded := make([]Record, 0, 4)
	for _, line := range strings.Split(strings.TrimSuffix(recorder.Raw(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := map[string]any{}
		if err := json.Unmarshal([]byte(line), &fields); err != nil {
			decoded = append(decoded, Record{Message: "undecodable activity record: " + err.Error()})
			continue
		}
		record := Record{Fields: fields}
		if level, ok := fields[slog.LevelKey].(string); ok {
			record.Level = level
		}
		if message, ok := fields[slog.MessageKey].(string); ok {
			record.Message = message
		}
		decoded = append(decoded, record)
	}
	return decoded
}

// TestOption configures obs.Test beyond its defaults.
type TestOption func(*scope)

// WithTestClock overrides the clock obs.Test's installed scope carries — a
// clock.Fake, so a door that reads its clock through ctx (obs.Clock, and
// every middleware built on top of it, the tmux door among them: exec.go's
// `clock: obs.Clock(ctx)`) advances on the test's own schedule instead of
// the wall clock. Without this obs.Test hardcoded clock.Real, so no test
// could ever tell the two apart. Named apart from store.WithClock (C17: one
// free function per name across packages) — an unrelated sqlitedb.OpenOption.
func WithTestClock(timing clock.Clock) TestOption {
	return func(installed *scope) { installed.timing = timing }
}

// Test installs a Recorder as the process logger for the length of t and
// returns the context carrying it. The previous process scope is restored on
// cleanup, so one test's logger never leaks into the next.
func Test(t *testing.T, opts ...TestOption) (context.Context, *Recorder) {
	t.Helper()
	recorder := &Recorder{}
	handler := slog.NewJSONHandler(recorder, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: Scrub})
	installed := &scope{logger: slog.New(handler), timing: clock.Real}
	for _, opt := range opts {
		opt(installed)
	}
	processMutex.Lock()
	previous := process
	process = installed
	processMutex.Unlock()
	t.Cleanup(func() {
		processMutex.Lock()
		process = previous
		processMutex.Unlock()
	})
	return context.WithValue(context.Background(), contextKey{}, installed), recorder
}
