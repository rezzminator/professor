package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// readLog decodes every record a home's activity file holds.
func readLog(t *testing.T, path string) []Record {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	recorder := &Recorder{}
	if _, err := recorder.Write(content); err != nil {
		t.Fatal(err)
	}
	return recorder.Records()
}

// openIn opens the activity log of the home at dir and returns its file path.
func openIn(t *testing.T, dir string, settings Settings) (func(int), string) {
	t.Helper()
	t.Setenv(paths.EnvHome, dir)
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Stderr == nil {
		settings.Stderr = &bytes.Buffer{}
	}
	_, finish := OpenLog(context.Background(), settings)
	return finish, resolved.LogFile
}

// TestOpenLogWritesOneFilePerHome pins the environment separation: the home
// jail IS the separation, so two homes never mix one record.
func TestOpenLogWritesOneFilePerHome(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	finishFirst, firstPath := openIn(t, first, Settings{Cmd: "reload", Version: "9.9.9"})
	Logger(context.Background()).Info("first.home")
	finishFirst(0)

	finishSecond, secondPath := openIn(t, second, Settings{Cmd: "reap", Version: "9.9.9"})
	Logger(context.Background()).Info("second.home")
	finishSecond(0)

	if firstPath == secondPath {
		t.Fatalf("both homes resolved to %s", firstPath)
	}
	if want := filepath.Join(first, ".local", "state", "pfm", "log", "pfm.jsonl"); firstPath != want {
		t.Fatalf("log path = %s, want %s", firstPath, want)
	}
	firstRaw, secondRaw := readLog(t, firstPath), readLog(t, secondPath)
	if strings.Contains(recordsText(firstRaw), "second.home") ||
		strings.Contains(recordsText(secondRaw), "first.home") {
		t.Fatalf("homes mixed records:\n%s\n%s", recordsText(firstRaw), recordsText(secondRaw))
	}
	if got, _ := firstRaw[0].Field(FieldCmd); got != "reload" {
		t.Fatalf("first home cmd = %v, want reload", got)
	}
}

func recordsText(records []Record) string {
	parts := make([]string, 0, len(records))
	for _, record := range records {
		encoded, _ := json.Marshal(record.Fields)
		parts = append(parts, string(encoded))
	}
	return strings.Join(parts, "\n")
}

// TestOpenLogRecordsCarryEveryDeclaredField pins the record shape: ts, level,
// msg, cmd, pid, version on every record, and the scoped fields where known.
func TestOpenLogRecordsCarryEveryDeclaredField(t *testing.T) {
	home := t.TempDir()
	finish, path := openIn(t, home, Settings{Cmd: "chat", Version: "1.2.3-alpha"})
	ctx := With(context.Background(), FieldChat, "cc-9", FieldSeat, 1, FieldEngine, "codex", FieldSock, "cc-9")
	end := Span(ctx, "chat.inject")
	end(nil)
	finish(7)

	records := readLog(t, path)
	if len(records) < 4 {
		t.Fatalf("records = %d, want cmd.start, span start/end and cmd.exit", len(records))
	}
	for _, key := range []string{FieldTime, slog.LevelKey, slog.MessageKey, FieldCmd, FieldPID, FieldVersion} {
		if _, found := records[0].Field(key); !found {
			t.Fatalf("cmd.start carries no %s: %+v", key, records[0].Fields)
		}
	}
	span := records[len(records)-2]
	for _, key := range []string{FieldChat, FieldSeat, FieldEngine, FieldSock, FieldDur} {
		if _, found := span.Field(key); !found {
			t.Fatalf("the span's end record carries no %s: %+v", key, span.Fields)
		}
	}
	exit := records[len(records)-1]
	if exit.Message != "cmd.exit" {
		t.Fatalf("last record = %q, want cmd.exit", exit.Message)
	}
	if code, _ := exit.Field(FieldExit); code != float64(7) {
		t.Fatalf("cmd.exit exit = %v, want 7", code)
	}
	if _, found := exit.Field(FieldDur); !found {
		t.Fatal("cmd.exit carries no duration")
	}
	if records[0].Message != "cmd.start" {
		t.Fatalf("first record = %q, want cmd.start", records[0].Message)
	}
}

// TestOpenLogLevelPerEnvironment covers the four level doors: the build's own
// default, pfm.config.json, PFM_LOG_LEVEL, and which of them wins.
func TestOpenLogLevelPerEnvironment(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		version    string
		configured string
		envLevel   string
		wantDebug  bool
		wantInfo   bool
	}{
		{name: "alpha build defaults to debug", version: "1.2.3-alpha", wantDebug: true, wantInfo: true},
		{name: "release build defaults to info", version: "1.2.3", wantDebug: false, wantInfo: true},
		{name: "config lifts a release build", version: "1.2.3", configured: "debug", wantDebug: true, wantInfo: true},
		{
			name: "config quiets an alpha build", version: "1.2.3-alpha", configured: "warn",
			wantDebug: false, wantInfo: false,
		},
		{name: "env overrides the build", version: "1.2.3", envLevel: "debug", wantDebug: true, wantInfo: true},
		{
			name: "env overrides the config", version: "1.2.3-alpha", configured: "error", envLevel: "debug",
			wantDebug: true, wantInfo: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if testCase.envLevel != "" {
				t.Setenv(paths.EnvLogLevel, testCase.envLevel)
			}
			finish, path := openIn(t, t.TempDir(), Settings{
				Cmd: "doctor", Version: testCase.version, Level: testCase.configured,
			})
			Logger(context.Background()).Debug("probe.debug")
			finish(0)
			text := recordsText(readLog(t, path))
			if got := strings.Contains(text, "probe.debug"); got != testCase.wantDebug {
				t.Fatalf("debug record present = %t, want %t: %s", got, testCase.wantDebug, text)
			}
			if got := strings.Contains(text, "cmd.start"); got != testCase.wantInfo {
				t.Fatalf("info record present = %t, want %t: %s", got, testCase.wantInfo, text)
			}
		})
	}
}

func TestOpenLogReportsAnUnparsableLevelInsteadOfIgnoringIt(t *testing.T) {
	stderr := &bytes.Buffer{}
	t.Setenv(paths.EnvLogLevel, "chatty")
	finish, path := openIn(t, t.TempDir(), Settings{Cmd: "doctor", Version: "1.2.3", Stderr: stderr})
	finish(0)
	if !strings.Contains(stderr.String(), paths.EnvLogLevel) {
		t.Fatalf("stderr = %q, want the rejected level named", stderr.String())
	}
	if text := recordsText(readLog(t, path)); !strings.Contains(text, "cmd.start") {
		t.Fatalf("a bad level silenced the log entirely: %s", text)
	}
}

// TestOpenLogMirrorsToStderr pins PFM_LOG=stderr for a foreground run.
func TestOpenLogMirrorsToStderr(t *testing.T) {
	stderr := &bytes.Buffer{}
	t.Setenv(paths.EnvLogMirror, StderrMirror)
	finish, path := openIn(t, t.TempDir(), Settings{Cmd: "ls", Version: "1.2.3", Stderr: stderr})
	finish(0)
	if !strings.Contains(stderr.String(), "cmd.start") {
		t.Fatalf("stderr mirror = %q, want the records", stderr.String())
	}
	if text := recordsText(readLog(t, path)); !strings.Contains(text, "cmd.start") {
		t.Fatalf("the mirror replaced the file instead of doubling it: %s", text)
	}
}

// TestOpenLogNamesAnUnwritableDestination: a log that cannot be opened is
// reported on stderr — never a silently quiet log.
func TestOpenLogNamesAnUnwritableDestination(t *testing.T) {
	stderr := &bytes.Buffer{}
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, finish := OpenLog(context.Background(), Settings{
		Cmd: "doctor", Version: "1.2.3", Path: filepath.Join(blocked, "log", "pfm.jsonl"), Stderr: stderr,
	})
	finish(1)
	if !strings.Contains(stderr.String(), "activity log unavailable") {
		t.Fatalf("stderr = %q, want the failure named", stderr.String())
	}
}

func TestVerbNamesTheCommandOrThePicker(t *testing.T) {
	for args, want := range map[string]string{"": "picker", "ls": "ls", "chat": "chat"} {
		var argv []string
		if args != "" {
			argv = []string{args, "extra"}
		}
		if got := Verb(argv); got != want {
			t.Fatalf("Verb(%q) = %q, want %q", args, got, want)
		}
	}
}

// TestOpenLogOffWritesNoFile: `off` records nothing AND creates nothing — an
// operator who turned the log off finds no pfm.jsonl, not an empty one.
func TestOpenLogOffWritesNoFile(t *testing.T) {
	for name, settings := range map[string]Settings{
		"config": {Cmd: "ls", Version: "1.2.3-alpha", Level: "off"},
		"env":    {Cmd: "ls", Version: "1.2.3-alpha", Level: "debug"},
	} {
		t.Run(name, func(t *testing.T) {
			if name == "env" {
				t.Setenv(paths.EnvLogLevel, "off")
			}
			finish, path := openIn(t, t.TempDir(), settings)
			Logger(context.Background()).Error("must.not.land")
			finish(0)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("stat %s: err = %v, want the file to not exist", path, err)
			}
			if Enabled(context.Background(), "mcp", slog.LevelError) {
				t.Fatal("Enabled = true while the log is off")
			}
		})
	}
}

// TestOpenLogNamesTheSettingThatStaysInForce: a bad PFM_LOG_LEVEL or
// PFM_LOG_COMPONENTS is reported with the accepted values and what stands.
func TestOpenLogNamesTheSettingThatStaysInForce(t *testing.T) {
	stderr := &bytes.Buffer{}
	t.Setenv(paths.EnvLogLevel, "chatty")
	t.Setenv(paths.EnvLogComponents, "database=debug")
	finish, path := openIn(t, t.TempDir(), Settings{Cmd: "doctor", Version: "1.2.3", Level: "warn", Stderr: stderr})
	Logger(context.Background()).Warn("still.writing")
	finish(0)
	for _, want := range []string{
		paths.EnvLogLevel, "chatty", "debug, info, warn, warning, error, off", "keeping warn from config",
		paths.EnvLogComponents, "database", "cli, mcp, http.in",
	} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, want %q in it", stderr.String(), want)
		}
	}
	if text := recordsText(readLog(t, path)); !strings.Contains(text, "still.writing") {
		t.Fatalf("a bad override silenced the log: %s", text)
	}
}
