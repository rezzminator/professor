// Package pluginlog is the shared plugin-log library: level files under one directory per plugin, rows bounded and
// keyed in one order, rotation past a cap, and rows held in memory across a failed write. It imports the standard
// library only.
//
// This file is a vendored copy of the plugin-log canonical source, kept byte-identical in every plugin by a sync
// check: change the canonical source, then re-vendor every copy.
package pluginlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// CapBytes is the size past which a level file moves aside to .1, older generations shifting up.
	CapBytes = 20971520
	// Keep is how many rotated generations each level file keeps (.1 newest … .5).
	Keep = 5
	// RowMaxBytes bounds one row in bytes, its newline included.
	RowMaxBytes = 4096
	// HoldRows is how many unwritten rows a Logger holds for its next write; past it the oldest go, counted.
	HoldRows = 2000
)

var levelFiles = map[string]string{"error": "errors.jsonl", "warning": "warnings.jsonl", "info": "info.jsonl", "debug": "debug.jsonl"}

// Row is one level row. A Logger fills an empty TS with the writing time and an empty Plugin with its own name.
// Detail carries operational metadata only, never prompt, transcript or tool bodies.
type Row struct {
	TS      string `json:"ts"`
	Plugin  string `json:"plugin"`
	Level   string `json:"level"`
	Event   string `json:"event"`
	Msg     string `json:"msg"`
	Where   string `json:"where"`
	Session string `json:"session,omitempty"`
	Detail  any    `json:"detail,omitempty"`
}

type truncated struct {
	Truncated bool `json:"truncated"`
	Bytes     int  `json:"bytes"`
}

// Dir is $PLUGIN_LOG_DIR/{plugin} when override is set, else {home}/.local/state/{plugin}/logs; "" is logging off.
func Dir(override, home, plugin string) string {
	if override != "" {
		return strings.TrimRight(override, "/") + "/" + plugin
	}
	if home != "" {
		return strings.TrimRight(home, "/") + "/.local/state/" + plugin + "/logs"
	}
	return ""
}

// OnScreen reports whether a row of level may reach the screen: errors only.
func OnScreen(level string) bool {
	return level == "error"
}

// Encode renders one row with its newline, at most RowMaxBytes: an oversized row's Detail becomes
// {truncated, bytes} (bytes: the whole row's), then Msg, Session, Where, Event and Plugin shorten in that order.
func Encode(row Row) ([]byte, error) {
	if _, ok := levelFiles[row.Level]; !ok {
		return nil, fmt.Errorf("plugin log invalid level: %q", row.Level)
	}
	raw, err := marshal(row)
	if err != nil || len(raw) <= RowMaxBytes {
		return raw, err
	}
	row.Detail = truncated{Truncated: true, Bytes: len(raw)}
	for _, field := range []*string{&row.Msg, &row.Session, &row.Where, &row.Event, &row.Plugin} {
		whole := []rune(*field)
		low, high := 0, len(whole)
		for low < high {
			middle := (low + high + 1) / 2
			*field = string(whole[:middle])
			candidate, err := marshal(row)
			if err != nil {
				return nil, err
			}
			if len(candidate) <= RowMaxBytes {
				low = middle
			} else {
				high = middle - 1
			}
		}
		*field = string(whole[:low])
		if raw, err = marshal(row); err != nil || len(raw) <= RowMaxBytes {
			return raw, err
		}
	}
	return raw, nil
}

func marshal(row Row) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(row); err != nil {
		return nil, fmt.Errorf("encode plugin log row %s: %w", row.Event, err)
	}
	return buf.Bytes(), nil
}

type held struct {
	row Row
	raw []byte
}

// Logger writes one plugin's rows under one directory. A row that cannot be written is held and written first by the
// next Record; past HoldRows the oldest go, and the next write that succeeds carries a plugin-log-rows-dropped error
// row counting them. The screen gets one line per failing streak, re-armed by a success, and never a row.
type Logger struct {
	mu      sync.Mutex
	plugin  string
	dir     string
	screen  io.Writer
	now     func() time.Time
	pending []held
	lost    int
	failing bool
}

// New is a Logger for plugin under dir ("" is logging off); screen nil is os.Stderr, now nil is time.Now.
func New(plugin, dir string, screen io.Writer, now func() time.Time) *Logger {
	if now == nil {
		now = time.Now
	}
	return &Logger{plugin: plugin, dir: dir, screen: screen, now: now}
}

var loggers = struct {
	sync.Mutex
	by map[[2]string]*Logger
}{by: map[[2]string]*Logger{}}

// For is the process's one Logger for plugin under dir, so its held rows and failing streak span every caller;
// screen replaces the one it had.
func For(plugin, dir string, screen io.Writer) *Logger {
	loggers.Lock()
	defer loggers.Unlock()
	key := [2]string{plugin, dir}
	logger := loggers.by[key]
	if logger == nil {
		logger = New(plugin, dir, screen, nil)
		loggers.by[key] = logger
		return logger
	}
	logger.mu.Lock()
	logger.screen = screen
	logger.mu.Unlock()
	return logger
}

// Record writes row after every row held before it; it never fails its caller.
func (l *Logger) Record(row Row) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dir == "" {
		if !l.failing {
			l.failing = true
			l.say("plugin log off: no PLUGIN_LOG_DIR and no HOME")
		}
		return
	}
	if row.Plugin == "" {
		row.Plugin = l.plugin
	}
	if row.TS == "" {
		row.TS = l.now().UTC().Format("2006-01-02T15:04:05.000Z")
	}
	raw, err := Encode(row)
	if err != nil {
		// A row that cannot be encoded never can be: it is said, not held.
		l.fail(err)
		return
	}
	l.pending = append(l.pending, held{row: row, raw: raw})
	if over := len(l.pending) - HoldRows; over > 0 {
		l.pending = l.pending[over:]
		l.lost += over
	}
	l.flush()
}

// Held is how many rows wait for the next write.
func (l *Logger) Held() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.pending)
}

func (l *Logger) flush() {
	if l.lost > 0 && len(l.pending) > 0 {
		drop := Row{TS: l.pending[0].row.TS, Plugin: l.plugin, Level: "error", Event: "plugin-log-rows-dropped", Msg: fmt.Sprintf("%d unwritten %s dropped past the %d-row hold", l.lost, plural(l.lost, "row was", "rows were"), HoldRows), Where: "internal/pluginlog/pluginlog.go#Logger.flush", Detail: map[string]int{"rows": l.lost}}
		raw, err := Encode(drop)
		if err == nil {
			err = l.write(held{row: drop, raw: raw})
		}
		if err != nil {
			l.fail(err)
			return
		}
		// Counted until a write takes its row: the count is never itself a held row the hold could drop.
		l.lost = 0
		l.failing = false
	}
	for len(l.pending) > 0 {
		if err := l.write(l.pending[0]); err != nil {
			l.fail(err)
			return
		}
		l.pending = l.pending[1:]
		l.failing = false
	}
}

// write rotates a full level file (and a full warnings file first) before appending, each move told by a log-capped row.
// Writers in other processes take no lock with it: a rotation they race at worst shifts one generation more.
func (l *Logger) write(entry held) error {
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return fmt.Errorf("create the log directory: %w", err)
	}
	name := levelFiles[entry.row.Level]
	path := filepath.Join(l.dir, name)
	rotated, err := rotate(path)
	if err != nil {
		return err
	}
	if rotated {
		if name != "warnings.jsonl" {
			full, err := rotate(filepath.Join(l.dir, "warnings.jsonl"))
			if err != nil {
				return err
			}
			if full {
				if err := l.capped(entry.row, "warnings.jsonl"); err != nil {
					return err
				}
			}
		}
		if err := l.capped(entry.row, name); err != nil {
			return err
		}
	}
	return appendFile(path, entry.raw)
}

func (l *Logger) capped(source Row, file string) error {
	raw, err := Encode(Row{TS: source.TS, Plugin: l.plugin, Level: "warning", Event: "log-capped", Msg: fmt.Sprintf("level file exceeded %d bytes; moved aside", CapBytes), Where: "internal/pluginlog/pluginlog.go#Logger.write", Session: source.Session, Detail: map[string]string{"file": file}})
	if err != nil {
		return err
	}
	return appendFile(filepath.Join(l.dir, "warnings.jsonl"), raw)
}

func rotate(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect %s: %w", path, err)
	}
	if info.Size() <= CapBytes {
		return false, nil
	}
	for n := Keep - 1; n >= 1; n-- {
		from := fmt.Sprintf("%s.%d", path, n)
		if err := os.Rename(from, fmt.Sprintf("%s.%d", path, n+1)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("rotate %s: %w", from, err)
		}
	}
	if err := os.Rename(path, path+".1"); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Another writer moved it aside first.
			return false, nil
		}
		return false, fmt.Errorf("rotate %s: %w", path, err)
	}
	return true, nil
}

func appendFile(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	n, writeErr := file.Write(raw)
	if writeErr == nil && n != len(raw) {
		writeErr = io.ErrShortWrite
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	return nil
}

func (l *Logger) fail(err error) {
	if !l.failing {
		n := len(l.pending)
		l.say(fmt.Sprintf("plugin log %s failed: %s; %d %s kept for the next write", l.dir, strings.NewReplacer("\r", " ", "\n", "; ").Replace(err.Error()), n, plural(n, "row is", "rows are")))
	}
	l.failing = true
}

func (l *Logger) say(line string) {
	screen := l.screen
	if screen == nil {
		screen = os.Stderr
	}
	// The screen is the last place left to tell; a write it refuses has nowhere else to go.
	_, _ = fmt.Fprintf(screen, "%s: %s\n", l.plugin, line)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
