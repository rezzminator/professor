package statusline

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

const pacedTestInput = `{"session_id":"paced-chat","model":{"display_name":"Opus"}}`

// pacedRig is a Paced over a temp cache dir with a fake clock: Sleep advances
// it and runs onSleep, Render records each payload and the clock it ran at.
type pacedRig struct {
	t        *testing.T
	paced    Paced
	now      time.Time
	sleeps   []time.Duration
	onSleep  func()
	renders  []string
	renderAt []time.Time
	output   string
	fail     error
	spawned  []string
	spawnErr error
	warn     strings.Builder
}

func newPacedRig(t *testing.T) *pacedRig {
	t.Helper()
	rig := &pacedRig{t: t, now: time.Now(), output: "\x1b[1mrendered\x1b[0m line\n"}
	rig.paced = Paced{
		CacheDir: t.TempDir(),
		Key:      PacedKey([]byte(pacedTestInput), ""),
		Render: func(raw []byte) (string, error) {
			rig.renders = append(rig.renders, string(raw))
			rig.renderAt = append(rig.renderAt, rig.now)
			return rig.output, rig.fail
		},
		Spawn: func(key string) error {
			rig.spawned = append(rig.spawned, key)
			return rig.spawnErr
		},
		Now: func() time.Time { return rig.now },
		Sleep: func(wait time.Duration) error {
			rig.sleeps = append(rig.sleeps, wait)
			if len(rig.sleeps) > 20 {
				t.Fatalf("the worker slept %d times without exiting: %v", len(rig.sleeps), rig.sleeps)
			}
			rig.now = rig.now.Add(wait)
			if rig.onSleep != nil {
				rig.onSleep()
			}
			return nil
		},
		Warn: &rig.warn,
	}
	return rig
}

func (rig *pacedRig) base() string {
	return filepath.Join(rig.paced.CacheDir, pacedDir+"-"+strconv.Itoa(os.Getuid()), rig.paced.Key)
}

// writeInput saves raw as the chat's input, written at the rig's clock.
func (rig *pacedRig) writeInput(raw string, at time.Time) {
	rig.t.Helper()
	if err := os.MkdirAll(filepath.Dir(rig.base()), 0o700); err != nil {
		rig.t.Fatal(err)
	}
	if err := os.WriteFile(rig.base()+".input", []byte(raw), 0o600); err != nil {
		rig.t.Fatal(err)
	}
	if err := os.Chtimes(rig.base()+".input", at, at); err != nil {
		rig.t.Fatal(err)
	}
}

func (rig *pacedRig) writeLine(line pacedLine) {
	rig.t.Helper()
	if err := os.MkdirAll(filepath.Dir(rig.base()), 0o700); err != nil {
		rig.t.Fatal(err)
	}
	if err := writePacedLine(rig.base()+".out.json", line); err != nil {
		rig.t.Fatal(err)
	}
}

func (rig *pacedRig) readLine() pacedLine {
	rig.t.Helper()
	line, found, err := readPacedLine(rig.base() + ".out.json")
	if err != nil || !found {
		rig.t.Fatalf("cached line found=%v err=%v", found, err)
	}
	return line
}

// stopAfter makes the chat's input stale once n renders ran, so the worker's
// next turn exits.
func (rig *pacedRig) stopAfter(n int) {
	render := rig.paced.Render
	rig.paced.Render = func(raw []byte) (string, error) {
		output, err := render(raw)
		if len(rig.renders) >= n {
			old := rig.now.Add(-time.Hour)
			if chtimesErr := os.Chtimes(rig.base()+".input", old, old); chtimesErr != nil {
				rig.t.Fatal(chtimesErr)
			}
		}
		return output, err
	}
}

func TestPacedFrontPrintsTheCachedLineWithoutRendering(t *testing.T) {
	rig := newPacedRig(t)
	cached := "\x1b[32m◆ Opus\x1b[0m │ cached\n2nd row  \n"
	rig.writeLine(pacedLine{V: pacedVersion, Output: cached, FinishedAt: rig.now.UnixNano()})
	var stdout strings.Builder
	rig.paced.Front([]byte(pacedTestInput), &stdout)
	if stdout.String() != cached || len(rig.renders) != 0 {
		t.Fatalf("stdout %q after %d renders; want the cached line byte-identical, no render", stdout.String(),
			len(rig.renders))
	}
	if input, err := os.ReadFile(rig.base() + ".input"); err != nil || string(input) != pacedTestInput {
		t.Fatalf("saved input %q (%v); want the stdin", input, err)
	}
	if rig.warn.Len() != 0 {
		t.Fatalf("warnings %q", rig.warn.String())
	}
}

func TestPacedFrontShowsACachedRenderErrorOnStderr(t *testing.T) {
	rig := newPacedRig(t)
	rig.writeLine(pacedLine{V: pacedVersion, Output: "kept\n", FinishedAt: rig.now.UnixNano(), Error: "boom"})
	var stdout strings.Builder
	rig.paced.Front([]byte(pacedTestInput), &stdout)
	if stdout.String() != "kept\n" || !strings.Contains(rig.warn.String(), "render (fail-open): boom") {
		t.Fatalf("stdout %q warnings %q; want the kept line and the error told", stdout.String(), rig.warn.String())
	}
}

// A chat's first tick starts its worker before anything else and prints the
// pending line, never an inline render: Claude Code aborts a run slower than
// its tick, and a front killed mid-render would have started no worker, so the
// line would stay blank on every tick after.
func TestPacedFrontWithNoCacheStartsTheWorkerFirstAndPrintsPending(t *testing.T) {
	rig := newPacedRig(t)
	var events []string
	render, spawn := rig.paced.Render, rig.paced.Spawn
	rig.paced.Render = func(raw []byte) (string, error) {
		events = append(events, "render")
		return render(raw)
	}
	rig.paced.Spawn = func(key string) error {
		events = append(events, "spawn")
		return spawn(key)
	}
	var stdout strings.Builder
	rig.paced.Front([]byte(pacedTestInput), &stdout)
	if stdout.String() != PacedPending || !slices.Equal(events, []string{"spawn"}) {
		t.Fatalf("stdout %q events %v; want the pending line after the worker's spawn, no render",
			stdout.String(), events)
	}
	if _, err := os.Stat(rig.base() + ".out.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cached line stat %v; want none, so the worker renders at once", err)
	}
	if rig.warn.Len() != 0 {
		t.Fatalf("warnings %q", rig.warn.String())
	}
}

func TestPacedFrontSpawnsTheWorkerOnlyWhenTheLockIsFree(t *testing.T) {
	rig := newPacedRig(t)
	var stdout strings.Builder
	rig.paced.Front([]byte(pacedTestInput), &stdout)
	if !slices.Equal(rig.spawned, []string{rig.paced.Key}) {
		t.Fatalf("spawned %v; want the chat's worker once", rig.spawned)
	}
	lock, held, err := takePacedLock(rig.base() + ".lock")
	if err != nil || !held {
		t.Fatalf("take lock held=%v err=%v", held, err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	rig.paced.Front([]byte(pacedTestInput), &stdout)
	if len(rig.spawned) != 1 || len(rig.renders) != 0 {
		t.Fatalf("spawned %v, %d renders with the lock held; want no spawn, no render", rig.spawned,
			len(rig.renders))
	}
}

func TestPacedFrontFallsBackInlineWhenThePacerBreaks(t *testing.T) {
	t.Run("refused directory", func(t *testing.T) {
		rig := newPacedRig(t)
		elsewhere := filepath.Join(t.TempDir(), "elsewhere")
		if err := os.MkdirAll(elsewhere, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(elsewhere, filepath.Dir(rig.base())); err != nil {
			t.Fatal(err)
		}
		var stdout strings.Builder
		rig.paced.Front([]byte(pacedTestInput), &stdout)
		if stdout.String() != rig.output || !strings.Contains(rig.warn.String(), "paced directory refused") {
			t.Fatalf("stdout %q warnings %q; want the inline render and the refusal told", stdout.String(),
				rig.warn.String())
		}
		if entries, err := os.ReadDir(elsewhere); err != nil || len(entries) != 0 || len(rig.spawned) != 0 {
			t.Fatalf("planted dir holds %v (%v), spawned %v; want nothing written, no spawn", entries, err,
				rig.spawned)
		}
	})
	t.Run("spawn error over a cached line", func(t *testing.T) {
		rig := newPacedRig(t)
		rig.writeLine(pacedLine{V: pacedVersion, Output: "stale\n", FinishedAt: rig.now.UnixNano()})
		rig.spawnErr = errors.New("no setsid")
		var stdout strings.Builder
		rig.paced.Front([]byte(pacedTestInput), &stdout)
		if stdout.String() != rig.output || !strings.Contains(rig.warn.String(), "no setsid") {
			t.Fatalf("stdout %q warnings %q; want the inline render and the spawn error told", stdout.String(),
				rig.warn.String())
		}
	})
	t.Run("spawn error with no cached line", func(t *testing.T) {
		rig := newPacedRig(t)
		rig.spawnErr = errors.New("no setsid")
		var stdout strings.Builder
		rig.paced.Front([]byte(pacedTestInput), &stdout)
		if stdout.String() != rig.output || !strings.Contains(rig.warn.String(), "no setsid") {
			t.Fatalf("stdout %q warnings %q; want the inline render and the spawn error told", stdout.String(),
				rig.warn.String())
		}
	})
	t.Run("input write fails", func(t *testing.T) {
		rig := newPacedRig(t)
		if err := os.MkdirAll(rig.base()+".input", 0o700); err != nil { // a directory where the file goes
			t.Fatal(err)
		}
		var stdout strings.Builder
		rig.paced.Front([]byte(pacedTestInput), &stdout)
		if stdout.String() != rig.output || !strings.Contains(rig.warn.String(), "write paced input") {
			t.Fatalf("stdout %q warnings %q; want the inline render and the write error told", stdout.String(),
				rig.warn.String())
		}
	})
}

func TestPacedFrontWithNoChatKeyRendersInlineAndKeepsNoFile(t *testing.T) {
	rig := newPacedRig(t)
	raw := `{"model":{"display_name":"Opus"}}`
	rig.paced.Key = PacedKey([]byte(raw), "120")
	var stdout strings.Builder
	rig.paced.Front([]byte(raw), &stdout)
	if rig.paced.Key != "" || stdout.String() != rig.output || len(rig.spawned) != 0 {
		t.Fatalf("key %q stdout %q spawned %v; want no key, the inline render, no spawn", rig.paced.Key,
			stdout.String(), rig.spawned)
	}
	if entries, err := os.ReadDir(rig.paced.CacheDir); err != nil || len(entries) != 0 {
		t.Fatalf("cache dir holds %v (%v); want no file", entries, err)
	}
}

func TestPacedKeyNamesTheChatAndItsWidth(t *testing.T) {
	bySession := PacedKey([]byte(`{"session_id":"s","transcript_path":"/t"}`), "")
	byTranscript := PacedKey([]byte(`{"transcript_path":"/t"}`), "")
	for _, tc := range []struct {
		name string
		got  string
		want bool
	}{
		{"session id", bySession, true},
		{"transcript path", byTranscript, true},
		{"session wins over transcript", bySession, bySession != byTranscript},
		{"width is part of the key", PacedKey([]byte(`{"session_id":"s"}`), "80"), bySession != PacedKey(
			[]byte(`{"session_id":"s"}`), "80")},
		{"neither", PacedKey([]byte(`{"model":{}}`), ""), false},
		{"not json", PacedKey([]byte(`{`), ""), false},
	} {
		if (tc.got != "") != tc.want || (tc.got != "" && len(tc.got) != 32) {
			t.Fatalf("%s: key %q", tc.name, tc.got)
		}
	}
}

func TestPacedWorkerWaitsTheGapAfterTheLastRenderFinished(t *testing.T) {
	rig := newPacedRig(t)
	finished := rig.now.Add(-time.Second)
	rig.writeLine(pacedLine{V: pacedVersion, Output: "old\n", FinishedAt: finished.UnixNano()})
	rig.writeInput(pacedTestInput, rig.now)
	rig.stopAfter(2)
	if err := rig.paced.Work(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rig.sleeps, []time.Duration{4 * time.Second, pacedGap, pacedGap}) {
		t.Fatalf("sleeps %v; want 4s to finished_at+5s, then a gap after each render", rig.sleeps)
	}
	if len(rig.renderAt) != 2 || !rig.renderAt[0].Equal(finished.Add(pacedGap)) ||
		!rig.renderAt[1].Equal(rig.renderAt[0].Add(pacedGap)) {
		t.Fatalf("renders at %v; want finished_at+5s, then 5s after that render", rig.renderAt)
	}
	if line := rig.readLine(); line.Output != rig.output || line.FinishedAt != rig.renderAt[1].UnixNano() {
		t.Fatalf("cached %+v; want the last render, finished when it returned", line)
	}
}

func TestPacedWorkerRendersTheNewestInput(t *testing.T) {
	rig := newPacedRig(t)
	rig.writeInput(pacedTestInput, rig.now)
	rig.writeLine(pacedLine{V: pacedVersion, Output: "old\n", FinishedAt: rig.now.UnixNano()})
	newer := `{"session_id":"paced-chat","model":{"display_name":"Sonnet"}}`
	rig.onSleep = func() {
		rig.onSleep = nil
		rig.writeInput(newer, rig.now)
	}
	rig.stopAfter(1)
	if err := rig.paced.Work(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rig.renders, []string{newer}) {
		t.Fatalf("rendered %q; want the input written while the worker waited", rig.renders)
	}
}

func TestPacedWorkerExitsWhenItsChatGoesQuiet(t *testing.T) {
	t.Run("stale input", func(t *testing.T) {
		rig := newPacedRig(t)
		rig.writeInput(pacedTestInput, rig.now.Add(-pacedIdle-time.Second))
		if err := rig.paced.Work(); err != nil || len(rig.renders) != 0 {
			t.Fatalf("err %v after %d renders; want an exit with no render", err, len(rig.renders))
		}
	})
	t.Run("input gone", func(t *testing.T) {
		rig := newPacedRig(t)
		if err := rig.paced.Work(); err != nil || len(rig.renders) != 0 {
			t.Fatalf("err %v after %d renders; want an exit with no render", err, len(rig.renders))
		}
	})
}

func TestPacedWorkerExitsWhenAWatchedFileIsReplaced(t *testing.T) {
	rig := newPacedRig(t)
	rig.writeInput(pacedTestInput, rig.now)
	binary := filepath.Join(t.TempDir(), "pfm")
	if err := os.WriteFile(binary, []byte("v1"), 0o600); err != nil {
		t.Fatal(err)
	}
	rig.paced.Watch = []string{binary, filepath.Join(t.TempDir(), "absent-config.json")}
	rig.onSleep = func() {
		rig.writeInput(pacedTestInput, rig.now)
		if len(rig.renders) == 0 {
			return
		}
		staged := binary + ".new"
		if err := os.WriteFile(staged, []byte("v2"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(staged, binary); err != nil {
			t.Fatal(err)
		}
	}
	if err := rig.paced.Work(); err != nil || len(rig.renders) != 1 {
		t.Fatalf("err %v after %d renders; want one render, then an exit once the binary was replaced", err,
			len(rig.renders))
	}
}

func TestPacedWorkerRenderErrorKeepsThePreviousOutput(t *testing.T) {
	rig := newPacedRig(t)
	rig.writeInput(pacedTestInput, rig.now)
	rig.writeLine(pacedLine{V: pacedVersion, Output: "good\n", FinishedAt: rig.now.UnixNano()})
	rig.output, rig.fail = "", errors.New("decode statusline input: boom")
	rig.stopAfter(1)
	if err := rig.paced.Work(); err != nil {
		t.Fatal(err)
	}
	line := rig.readLine()
	if line.Output != "good\n" || line.Error != rig.fail.Error() || line.FinishedAt != rig.renderAt[0].UnixNano() {
		t.Fatalf("cached %+v; want the previous output, the error, the failed render's finish", line)
	}
}

func TestPacedSecondWorkerExitsWhenTheLockIsHeld(t *testing.T) {
	rig := newPacedRig(t)
	rig.writeInput(pacedTestInput, rig.now)
	lock, held, err := takePacedLock(rig.base() + ".lock")
	if err != nil || !held {
		t.Fatalf("take lock held=%v err=%v", held, err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	if err := rig.paced.Work(); err != nil || len(rig.sleeps) != 0 || len(rig.renders) != 0 {
		t.Fatalf("err %v, %d sleeps, %d renders; want an immediate exit", err, len(rig.sleeps), len(rig.renders))
	}
}

func TestPacedWorkerRefusesAKeyThatIsNoDigest(t *testing.T) {
	rig := newPacedRig(t)
	rig.paced.Key = "../escape"
	if err := rig.paced.Work(); err == nil || !strings.Contains(err.Error(), "32 hex digits") {
		t.Fatalf("err %v; want the key refused", err)
	}
}

func TestSpawnDetachedStartsThePacedWorkerWithItsKey(t *testing.T) {
	runner := &deps.FakeRunner{}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	runner.ScriptStart([]string{executable}, 7, nil, nil)
	if err := SpawnDetached(RefreshKindPaced, runner, "0123"); err != nil {
		t.Fatal(err)
	}
	starts := runner.Starts()
	if len(starts) != 1 || !slices.Equal(starts[0].Argv[1:], []string{"statusline", "--paced", "0123"}) ||
		!starts[0].Opts.Detach {
		raw, _ := json.Marshal(starts)
		t.Fatalf("starts %s; want one detached `statusline --paced 0123`", raw)
	}
}
