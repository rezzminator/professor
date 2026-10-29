package pluginlog

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// spec is ../spec.json, the contract every language implementation is tested against.
type spec struct {
	CapBytes    int64             `json:"capBytes"`
	Keep        int               `json:"keep"`
	RowMaxBytes int               `json:"rowMaxBytes"`
	HoldRows    int               `json:"holdRows"`
	DirMode     os.FileMode       `json:"dirMode"`
	FileMode    os.FileMode       `json:"fileMode"`
	Files       map[string]string `json:"files"`
	OnScreen    map[string]bool   `json:"onScreen"`
	Keys        []string          `json:"keys"`
	Dirs        []struct {
		Override, Home *string
		Plugin         string
		Dir            *string
	} `json:"dirs"`
	Capped  struct{ Level, Event, Msg string } `json:"capped"`
	Dropped struct{ Level, Event, Msg string } `json:"dropped"`
	Screen  struct{ Off, Failing string }      `json:"screen"`
	Rows    []struct {
		Name         string
		NowMs        int64
		Fields       map[string]any
		Repeat       map[string][2]any
		RepeatDetail map[string][2]any
		Row          string
	} `json:"rows"`
}

func load(t *testing.T) spec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "spec.json"))
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var s spec
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("parse spec: %v", err)
	}
	return s
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func fill(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, size); err != nil {
		t.Fatal(err)
	}
}

func rows(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var out []map[string]any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 8192), 8192)
	for scanner.Scan() {
		var row map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil {
			t.Fatalf("row %q: %v", scanner.Text(), err)
		}
		out = append(out, row)
	}
	return out
}

func clock(ms int64) func() time.Time { return func() time.Time { return time.UnixMilli(ms) } }

const now = 1791991231123

func row(msg, level string) Row {
	return Row{Level: level, Event: "write-failed", Msg: msg, Where: "src/demo.go#write", Session: "s1"}
}

func TestConstants(t *testing.T) {
	s := load(t)
	if CapBytes != s.CapBytes || Keep != s.Keep || RowMaxBytes != s.RowMaxBytes || HoldRows != s.HoldRows {
		t.Fatalf("constants %d %d %d %d differ from the spec", CapBytes, Keep, RowMaxBytes, HoldRows)
	}
	for level, file := range s.Files {
		if levelFiles[level] != file {
			t.Errorf("level %s file %q, spec %q", level, levelFiles[level], file)
		}
	}
	if len(levelFiles) != len(s.Files) {
		t.Errorf("levels %v, spec %v", levelFiles, s.Files)
	}
	for level, shown := range s.OnScreen {
		if OnScreen(level) != shown {
			t.Errorf("OnScreen(%s) = %v", level, !shown)
		}
	}
}

func TestDir(t *testing.T) {
	for _, v := range load(t).Dirs {
		if got := Dir(str(v.Override), str(v.Home), v.Plugin); got != str(v.Dir) {
			t.Errorf("Dir(%q, %q) = %q, want %q", str(v.Override), str(v.Home), got, str(v.Dir))
		}
	}
}

func TestSpecRows(t *testing.T) {
	s := load(t)
	for _, v := range s.Rows {
		t.Run(v.Name, func(t *testing.T) {
			f := map[string]any{}
			for k, x := range v.Fields {
				f[k] = x
			}
			for k, r := range v.Repeat {
				f[k] = strings.Repeat(r[0].(string), int(r[1].(float64)))
			}
			if v.RepeatDetail != nil {
				detail := map[string]any{}
				for k, r := range v.RepeatDetail {
					detail[k] = strings.Repeat(r[0].(string), int(r[1].(float64)))
				}
				f["detail"] = detail
			}
			text := func(k string) string { x, _ := f[k].(string); return x }
			r := Row{TS: text("ts"), Plugin: text("plugin"), Level: text("level"), Event: text("event"), Msg: text("msg"), Where: text("where"), Session: text("session"), Detail: f["detail"]}
			dir := t.TempDir()
			var screen bytes.Buffer
			New("demo", dir, &screen, clock(v.NowMs)).Record(r)
			got, err := os.ReadFile(filepath.Join(dir, s.Files[r.Level]))
			if err != nil {
				t.Fatalf("read: %v; screen %q", err, screen.String())
			}
			if string(got) != v.Row {
				t.Fatalf("row\n got %s\nwant %s", got, v.Row)
			}
			if screen.Len() != 0 {
				t.Fatalf("screen got %q", screen.String())
			}
		})
	}
}

func TestLevelsAndModes(t *testing.T) {
	s := load(t)
	dir := filepath.Join(t.TempDir(), "logs", "demo")
	logger := New("demo", dir, &bytes.Buffer{}, clock(now))
	for level := range s.Files {
		logger.Record(row("x", level))
	}
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != s.DirMode {
		t.Fatalf("directory mode %v, %v", info, err)
	}
	for level, file := range s.Files {
		got := rows(t, filepath.Join(dir, file))
		if len(got) != 1 || got[0]["level"] != level || got[0]["plugin"] != "demo" {
			t.Errorf("%s: %v", file, got)
		}
		info, err := os.Stat(filepath.Join(dir, file))
		if err != nil || info.Mode().Perm() != s.FileMode {
			t.Errorf("%s mode %v, %v", file, info, err)
		}
	}
}

func capped(s spec, file string) map[string]any {
	return map[string]any{"ts": "2026-10-14T15:20:31.123Z", "plugin": "demo", "level": s.Capped.Level, "event": s.Capped.Event, "msg": s.Capped.Msg, "where": "internal/pluginlog/pluginlog.go#Logger.write", "session": "s1", "detail": map[string]any{"file": file}}
}

func TestRotation(t *testing.T) {
	s := load(t)
	dir := t.TempDir()
	big := filepath.Join(dir, "errors.jsonl")
	for n := 1; n <= 5; n++ {
		if err := os.WriteFile(fmt.Sprintf("%s.%d", big, n), []byte(fmt.Sprintf("generation %d\n", n)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fill(t, big, s.CapBytes+1)
	New("demo", dir, &bytes.Buffer{}, clock(now)).Record(row("new", "error"))
	if info, err := os.Stat(big + ".1"); err != nil || info.Size() != s.CapBytes+1 {
		t.Fatalf(".1: %v %v", info, err)
	}
	for n := 2; n <= 5; n++ {
		got, _ := os.ReadFile(fmt.Sprintf("%s.%d", big, n))
		if string(got) != fmt.Sprintf("generation %d\n", n-1) {
			t.Errorf(".%d = %q", n, got)
		}
	}
	if _, err := os.Stat(big + ".6"); !os.IsNotExist(err) {
		t.Errorf(".6 exists: %v", err)
	}
	if got := rows(t, big); len(got) != 1 || got[0]["msg"] != "new" {
		t.Errorf("errors.jsonl %v", got)
	}
	if got, want := rows(t, filepath.Join(dir, "warnings.jsonl")), []map[string]any{capped(s, "errors.jsonl")}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("warnings\n got %v\nwant %v", got, want)
	}
}

func TestWarningsCappedFirst(t *testing.T) {
	s := load(t)
	dir := t.TempDir()
	fill(t, filepath.Join(dir, "errors.jsonl"), s.CapBytes+1)
	fill(t, filepath.Join(dir, "warnings.jsonl"), s.CapBytes+1)
	New("demo", dir, &bytes.Buffer{}, clock(now)).Record(row("new", "error"))
	if got, want := rows(t, filepath.Join(dir, "warnings.jsonl")), []map[string]any{capped(s, "warnings.jsonl"), capped(s, "errors.jsonl")}; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("warnings\n got %v\nwant %v", got, want)
	}
}

func TestStreakHoldAndRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.WriteFile(dir, []byte("a file where the directory goes"), 0o600); err != nil {
		t.Fatal(err)
	}
	var screen bytes.Buffer
	logger := New("demo", dir, &screen, clock(now))
	logger.Record(row("a", "error"))
	logger.Record(row("b", "info"))
	want := regexp.MustCompile(`^demo: plugin log ` + regexp.QuoteMeta(dir) + ` failed: .+; 1 row is kept for the next write\n$`)
	if !want.MatchString(screen.String()) || logger.Held() != 2 {
		t.Fatalf("screen %q held %d", screen.String(), logger.Held())
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	logger.Record(row("c", "error"))
	if logger.Held() != 0 {
		t.Fatalf("held %d after recovery", logger.Held())
	}
	if got := rows(t, filepath.Join(dir, "errors.jsonl")); len(got) != 2 || got[0]["msg"] != "a" || got[1]["msg"] != "c" {
		t.Errorf("errors %v", got)
	}
	if got := rows(t, filepath.Join(dir, "info.jsonl")); len(got) != 1 || got[0]["msg"] != "b" {
		t.Errorf("info %v", got)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	screen.Reset()
	logger.Record(row("d", "error"))
	if !want.MatchString(screen.String()) {
		t.Fatalf("a new streak said %q", screen.String())
	}
}

func TestHoldDrop(t *testing.T) {
	s := load(t)
	dir := filepath.Join(t.TempDir(), "logs")
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	logger := New("demo", dir, &bytes.Buffer{}, clock(now))
	for n := 0; n < s.HoldRows+3; n++ {
		logger.Record(row(fmt.Sprint(n), "error"))
	}
	if logger.Held() != s.HoldRows {
		t.Fatalf("held %d", logger.Held())
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	logger.Record(row("last", "error"))
	got := rows(t, filepath.Join(dir, "errors.jsonl"))
	drop := map[string]any{"ts": "2026-10-14T15:20:31.123Z", "plugin": "demo", "level": s.Dropped.Level, "event": s.Dropped.Event, "msg": strings.ReplaceAll(s.Dropped.Msg, "{rows}", "4"), "where": "internal/pluginlog/pluginlog.go#Logger.flush", "detail": map[string]any{"rows": float64(4)}}
	if len(got) != s.HoldRows+1 || fmt.Sprint(got[0]) != fmt.Sprint(drop) || got[1]["msg"] != "4" || got[len(got)-1]["msg"] != "last" {
		t.Fatalf("%d rows, first %v second %v", len(got), got[0], got[1])
	}
}

func TestOff(t *testing.T) {
	s := load(t)
	var screen bytes.Buffer
	logger := New("demo", "", &screen, clock(now))
	logger.Record(row("a", "error"))
	logger.Record(row("b", "error"))
	if want := strings.ReplaceAll(s.Screen.Off, "{plugin}", "demo") + "\n"; screen.String() != want {
		t.Fatalf("screen %q, want %q", screen.String(), want)
	}
}

func TestInvalidLevelIsSaidNotHeld(t *testing.T) {
	dir := t.TempDir()
	var screen bytes.Buffer
	logger := New("demo", dir, &screen, clock(now))
	logger.Record(row("x", "fatal"))
	if !strings.Contains(screen.String(), "invalid level") || logger.Held() != 0 {
		t.Fatalf("screen %q held %d", screen.String(), logger.Held())
	}
}

func TestForSharesOneLogger(t *testing.T) {
	a, b := For("demo", "/x/one", nil), For("demo", "/x/one", nil)
	if a != b || For("demo", "/x/two", nil) == a {
		t.Fatal("For must return one Logger per plugin and directory")
	}
}
