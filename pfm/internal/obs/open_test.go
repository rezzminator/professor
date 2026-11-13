package obs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// openIn opens the activity log of the jailed home at dir and returns its file path.
func openIn(t *testing.T, dir string, settings Settings) (func(int), string) {
	t.Helper()
	t.Setenv(paths.EnvHome, dir)
	if settings.Stderr == nil {
		settings.Stderr = &bytes.Buffer{}
	}
	_, finish := OpenLog(context.Background(), settings)
	return finish, filepath.Join(dir, ".local", "state", "pfm", "log", "pfm.jsonl")
}

// TestOpenLogWritesOneFilePerHome pins the environment separation: the home
// jail IS the separation, so two homes never mix one record.
func TestOpenLogWritesOneFilePerHome(t *testing.T) {
	first, second := t.TempDir(), t.TempDir()
	// A jailed home whose install recorded a writable clone still never writes into it.
	clone := t.TempDir()
	if err := paths.WriteSourceRepoMarker(first, clone); err != nil {
		t.Fatal(err)
	}
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
	if entries, err := os.ReadDir(clone); err != nil || len(entries) != 0 {
		t.Fatalf("the jail wrote into its recorded clone %s: %v (err %v)", clone, entries, err)
	}
}

// TestOpenLogWritesUnderTheRecordedClone: an unjailed home logs in its
// recorded clone's tmp/logs/pfm, silently; a recorded clone that is gone
// logs under the home's state folder and says so once on stderr.
func TestOpenLogWritesUnderTheRecordedClone(t *testing.T) {
	for _, gone := range []bool{false, true} {
		home, clone := t.TempDir(), filepath.Join(t.TempDir(), "professor")
		if err := os.Mkdir(clone, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
			t.Fatal(err)
		}
		want, wantStderr := filepath.Join(clone, "tmp", "logs", "pfm", "pfm.jsonl"), ""
		if gone {
			if err := os.Remove(clone); err != nil {
				t.Fatal(err)
			}
			want = filepath.Join(home, ".local", "state", "pfm", "log", "pfm.jsonl")
			wantStderr = "pfm: activity log: log root: inspect recorded source repository " + clone +
				": recorded source repository is unusable: stat " + clone + ": no such file or directory; " +
				"logging under " + filepath.Dir(want) + "\n"
		}
		stderr := &bytes.Buffer{}
		env := &paths.MapEnv{HomeDir: home, Values: map[string]string{paths.EnvRealHome: "1"}}
		_, finish := OpenLog(context.Background(), Settings{Cmd: "doctor", Version: "1.2.3", Env: env, Stderr: stderr})
		finish(0)
		if got := recordsText(readLog(t, want)); !strings.Contains(got, "cmd.exit") {
			t.Fatalf("gone=%v: %s holds %q, want the run's records", gone, want, got)
		}
		if stderr.String() != wantStderr {
			t.Fatalf("gone=%v: stderr = %q, want %q", gone, stderr.String(), wantStderr)
		}
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
	end := Span(ctx, "chat.call")
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

func TestOpenLogLevelPerEnvironment(t *testing.T) {
	for _, row := range []struct{ name, version, configured, envLevel, want string }{
		{"alpha build defaults to debug", "1.2.3-alpha", "", "", "cmd.start probe.debug cmd.exit"},
		{"release build defaults to info", "1.2.3", "", "", "cmd.start cmd.exit"},
		{"config lifts a release build", "1.2.3", "debug", "", "cmd.start probe.debug cmd.exit"},
		{"config quiets an alpha build", "1.2.3-alpha", "warn", "", ""},
		{"env overrides the build", "1.2.3", "", "debug", "cmd.start probe.debug cmd.exit"},
		{"env overrides the config", "1.2.3-alpha", "error", "debug", "cmd.start probe.debug cmd.exit"},
	} {
		t.Run(row.name, func(t *testing.T) {
			if row.envLevel != "" {
				t.Setenv(paths.EnvLogLevel, row.envLevel)
			}
			finish, path := openIn(t, t.TempDir(), Settings{Cmd: "doctor", Version: row.version, Level: row.configured})
			Logger(context.Background()).Debug("probe.debug")
			finish(0)
			if got := messages(readLog(t, path)); got != row.want {
				t.Fatalf("records = %q, want %q", got, row.want)
			}
		})
	}
}

func TestOpenLogReportsAnUnparsableLevelInsteadOfIgnoringIt(t *testing.T) {
	stderr := &bytes.Buffer{}
	t.Setenv(paths.EnvLogLevel, "chatty")
	finish, path := openIn(t, t.TempDir(), Settings{Cmd: "doctor", Version: "1.2.3", Stderr: stderr})
	finish(0)
	if stderr.String() != "pfm: activity log: PFM_LOG_LEVEL: level \"chatty\" is not one of debug, info, warn, warning, error, off; keeping info from build\n" {
		t.Fatalf("stderr = %q, want the rejected level named", stderr.String())
	}
	if text := recordsText(readLog(t, path)); !strings.Contains(text, "cmd.start") {
		t.Fatalf("a bad level silenced the log entirely: %s", text)
	}
}

func TestOpenLogMirrorsToStderr(t *testing.T) {
	stderr := &bytes.Buffer{}
	t.Setenv(paths.EnvLogMirror, StderrMirror)
	finish, path := openIn(t, t.TempDir(), Settings{Cmd: "ls", Version: "1.2.3", Stderr: stderr})
	finish(0)
	mirror := &Recorder{}
	if _, err := mirror.Write(stderr.Bytes()); err != nil {
		t.Fatal(err)
	}
	if got, want := messages(
		mirror.Records(),
	)+" | "+messages(
		readLog(t, path),
	), "cmd.start cmd.exit | cmd.start cmd.exit"; got != want {
		t.Fatalf("stderr records | file records = %q, want %q", got, want)
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
	if got, want := strings.ReplaceAll(
		stderr.String(),
		blocked,
		"{blocked}",
	), "pfm: activity log unavailable: create activity log directory {blocked}/log: mkdir {blocked}: not a directory\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
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
	if got, want := stderr.String()+" | "+messages(
		readLog(t, path),
	), "pfm: activity log: PFM_LOG_LEVEL: level \"chatty\" is not one of debug, info, warn, warning, error, off; keeping warn from config\nPFM_LOG_COMPONENTS: component \"database\" is not one of cli, mcp, http.in, http.out, runner, tmux, hooks, db, installer, state; keeping warn from config\n | still.writing"; got != want {
		t.Fatalf("stderr | records = %q, want %q", got, want)
	}
}

func TestAlphaBuild(t *testing.T) {
	for version, want := range map[string]bool{
		"0.80.0-alpha": true, " 0.80.0-alpha\n": true, "0.80.0": false, "dev": false, "dev (abc, modified)": false, "": false,
	} {
		if got := AlphaBuild(version); got != want {
			t.Errorf("AlphaBuild(%q) = %v, want %v", version, got, want)
		}
	}
}

// messages is each record's message, with its comp field when it has one, in log order.
func messages(records []Record) string {
	parts := make([]string, 0, len(records))
	for _, record := range records {
		if comp, ok := record.Fields[FieldComp]; ok {
			parts = append(parts, fmt.Sprintf("%s@%v", record.Message, comp))
			continue
		}
		parts = append(parts, record.Message)
	}
	return strings.Join(parts, " ")
}

// runs writes each run of 50 or more of one byte as {N×c}, so a capped value compares whole.
func runs(text string) string {
	var out strings.Builder
	for i := 0; i < len(text); {
		j := i
		for j < len(text) && text[j] == text[i] {
			j++
		}
		if j-i >= 50 {
			fmt.Fprintf(&out, "{%d×%c}", j-i, text[i])
		} else {
			out.WriteString(text[i:j])
		}
		i = j
	}
	return out.String()
}
