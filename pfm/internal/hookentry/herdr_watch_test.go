package hookentry

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The entries Claude Code 2.1.301 writes when the user answers a permission
// dialog with Esc or No, verbatim from a live transcript (cwd shortened): the
// tool_result rejection, then the interrupt 2 ms later. The assistant entry
// before them is the tool_use the dialog asked about.
const (
	watchToolUseEntry = `{"parentUuid":"c1","isSidechain":false,"type":"assistant","message":{"role":"assistant",` +
		`"content":[{"type":"tool_use","id":"toolu_01LGxd2rZLQuYQ1YLmLdLHgt","name":"Bash",` +
		`"input":{"command":"touch a.txt"}}]},"uuid":"86041a0e-7ff7-4d03-a744-92968e7d1c66","version":"2.1.301"}`
	watchRejectionEntry = `{"parentUuid":"86041a0e-7ff7-4d03-a744-92968e7d1c66","isSidechain":false,` +
		`"promptId":"2c0e71f8-7aee-4938-941f-ed54b7e17e55","type":"user","message":{"role":"user","content":` +
		`[{"type":"tool_result","content":"The user doesn't want to proceed with this tool use. The tool use was ` +
		`rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing ` +
		`and wait for the user to tell you how to proceed.","is_error":true,"tool_use_id":` +
		`"toolu_01LGxd2rZLQuYQ1YLmLdLHgt"}]},"uuid":"0f5dba99-9295-4428-bb80-fcd0fb4e60c0","timestamp":` +
		`"2026-10-16T08:57:31.252Z","toolUseResult":"User rejected tool use","toolDenialKind":"user-rejected",` +
		`"sourceToolAssistantUUID":"86041a0e-7ff7-4d03-a744-92968e7d1c66","session_id":` +
		`"35a503c8-b5df-4d33-87fd-9b7f6e91e79c","userType":"external","entrypoint":"cli","cwd":"/tmp/proj",` +
		`"sessionId":"35a503c8-b5df-4d33-87fd-9b7f6e91e79c","version":"2.1.301","gitBranch":"HEAD"}`
	watchInterruptEntry = `{"parentUuid":"c0255d4e-8535-4b2d-9740-c98812827f26","isSidechain":false,` +
		`"promptId":"2c0e71f8-7aee-4938-941f-ed54b7e17e55","type":"user","message":{"role":"user","content":` +
		`[{"type":"text","text":"[Request interrupted by user for tool use]"}]},"uuid":` +
		`"7af462d5-328a-4c83-b15c-e5b58959bc46","timestamp":"2026-10-16T08:57:31.254Z","session_id":` +
		`"35a503c8-b5df-4d33-87fd-9b7f6e91e79c","userType":"external","entrypoint":"cli","cwd":"/tmp/proj",` +
		`"sessionId":"35a503c8-b5df-4d33-87fd-9b7f6e91e79c","version":"2.1.301","gitBranch":"HEAD"}`
	watchPermission = `{"session_id":"s1","notification_type":"permission_prompt",` +
		`"message":"Claude needs your permission to use Bash"}`
)

// fastWatch polls every 10 ms for a minute: the stop tests see a watch end
// within a few ticks of its cause.
var fastWatch = herdrWatchTiming{poll: 10 * time.Millisecond, lifetime: time.Minute}

// watchRig is a chat rig whose Claude chat is working, attached from w1:p1,
// with a transcript holding the tool_use a permission dialog asks about, and a
// spawn that records each detached watch a blocked event starts.
type watchRig struct {
	*chatRig
	transcript string
	spawned    [][]string
}

func newWatchRig(t *testing.T) *watchRig {
	t.Helper()
	rig := &watchRig{chatRig: newChatRig(t), transcript: filepath.Join(t.TempDir(), "s1.jsonl")}
	rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
	if err := os.WriteFile(rig.transcript, []byte(watchToolUseEntry+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rig.jail.spawn = func(argv []string) error {
		rig.spawned = append(rig.spawned, argv)
		return nil
	}
	return rig
}

func (rig *watchRig) blockedPayload(tool, transcript string) string {
	return fmt.Sprintf(`{"session_id":"s1","hook_event_name":"PermissionRequest","tool_name":%q,"transcript_path":%q}`,
		tool, transcript)
}

// block runs the PermissionRequest hook and returns the watch it started.
func (rig *watchRig) block(tool string) []string {
	rig.t.Helper()
	before := len(rig.spawned)
	rig.wantQuiet(rig.engine("blocked", rig.blockedPayload(tool, rig.transcript)))
	if len(rig.spawned) != before+1 {
		rig.t.Fatalf("blocked(%s) started %d watches, want 1", tool, len(rig.spawned)-before)
	}
	return rig.spawned[len(rig.spawned)-1]
}

func (rig *watchRig) appendEntries(entries ...string) {
	rig.t.Helper()
	file, err := os.OpenFile(rig.transcript, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		rig.t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(strings.Join(entries, "\n") + "\n"); err != nil {
		rig.t.Fatal(err)
	}
}

func (rig *watchRig) record() string { return filepath.Join(rig.jail.lockDir, "cc-1.watch") }

// runningWatch is one watch process played in a goroutine with its own clock.
type runningWatch struct {
	done   chan struct{}
	rc     int
	stderr bytes.Buffer
	ended  time.Time
}

// start plays the spawned argv the way the detached child runs it.
func (rig *watchRig) start(argv []string, timing herdrWatchTiming) *runningWatch {
	rig.t.Helper()
	if len(argv) < 3 || !slices.Equal(argv[:3], []string{"internal", "herdr-state", herdrWatchVerb}) {
		rig.t.Fatalf("spawned argv = %q, want internal herdr-state watch …", argv)
	}
	clock := int64(9_000_000_000)
	deps := rig.jail.deps(rig.env, &clock)
	deps.tmux = rig.tmux.run
	watch := &runningWatch{done: make(chan struct{})}
	go func() {
		defer close(watch.done)
		watch.rc = watchForHerdr(argv[3:], &watch.stderr, deps, timing)
		watch.ended = time.Now()
	}()
	return watch
}

func (watch *runningWatch) wait(t *testing.T, within time.Duration) {
	t.Helper()
	select {
	case <-watch.done:
	case <-time.After(within):
		t.Fatalf("the watch still runs after %s", within)
	}
}

func flagValue(t *testing.T, argv []string, name string) string {
	t.Helper()
	for index := 0; index+1 < len(argv); index++ {
		if argv[index] == name {
			return argv[index+1]
		}
	}
	t.Fatalf("argv %q has no %s", argv, name)
	return ""
}

// A blocked Claude chat whose user presses Esc or No at the permission dialog
// is reported idle within 2 s of the interrupt entry reaching its transcript,
// at the stored seq right above the block, its theme state done while the
// theme sync is on, and the watch's record is gone.
func TestHerdrWatchReportsIdleOnTheInterruptEntry(t *testing.T) {
	cases := []struct {
		name  string
		after func(rig *watchRig)
	}{
		{"the interrupt entry", func(*watchRig) {}},
		{"the permission notification keeps the watch", func(rig *watchRig) {
			rig.wantQuiet(rig.engine("notification", watchPermission))
		}},
		{"the theme sync on", func(rig *watchRig) { rig.themeOn() }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newWatchRig(t)
			info, err := os.Stat(rig.transcript)
			if err != nil {
				t.Fatal(err)
			}
			argv := rig.block("Bash")
			blocked := rig.tmux.storedState(t)
			if blocked.State != "blocked" {
				t.Fatalf("stored = %q, want blocked", rig.tmux.stored)
			}
			for flag, want := range map[string]string{
				"--socket": rigSocket, "--session": "cc-1", "--seq": strconv.FormatUint(blocked.Seq, 10),
				"--transcript": rig.transcript, "--offset": strconv.FormatInt(info.Size(), 10),
			} {
				if got := flagValue(t, argv, flag); got != want {
					t.Fatalf("watch %s = %q, want %q (argv %q)", flag, got, want, argv)
				}
			}
			tc.after(rig)
			sent := len(rig.herdr.calls())
			watch := rig.start(argv, herdrWatchTiming{poll: herdrWatchPoll, lifetime: herdrWatchLifetime})
			time.Sleep(2 * herdrWatchPoll) // the watch is polling when the entries land
			appended := time.Now()
			rig.appendEntries(watchRejectionEntry, watchInterruptEntry)
			watch.wait(t, 5*time.Second)
			if took := watch.ended.Sub(appended); took > 2*time.Second {
				t.Fatalf("idle reported %s after the interrupt entry, want within 2s", took)
			}
			if watch.rc != 0 || watch.stderr.String() != "" {
				t.Fatalf("watch rc=%d stderr=%q, want a quiet exit 0", watch.rc, watch.stderr.String())
			}
			want := []string{listCall, reportCall("w1:p1", "idle", "")}
			if got := rig.herdr.calls()[sent:]; !slices.Equal(got, want) {
				t.Fatalf("requests after the interrupt = %q, want %q", got, want)
			}
			if state := rig.tmux.storedState(t); state.State != "idle" || state.Seq <= blocked.Seq {
				t.Fatalf("stored = %q, want idle above blocked seq %d", rig.tmux.stored, blocked.Seq)
			}
			if _, err := os.Stat(rig.record()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("watch record after the idle: %v, want it gone", err)
			}
			// The watch's idle marks the server's theme state as a hook's idle does.
			if rig.jail.stateDir != "" {
				want := "socket=" + rigSocket + " set-option -g @pfm-state done"
				if calls := rig.themeStateCalls(); !slices.Equal(calls, []string{want}) {
					t.Fatalf("@pfm-state calls = %q, want [%s]", calls, want)
				}
			}
		})
	}
}

// A watch ends on the next state-changing hook, on the chat's release, when a
// second block supersedes it, and at its lifetime; an interrupt entry after it
// ended reports nothing, so the newer state stays.
func TestHerdrWatchStops(t *testing.T) {
	cases := []struct {
		name   string
		timing herdrWatchTiming
		stop   func(rig *watchRig)
		state  string
	}{
		{"the next hook", fastWatch, func(rig *watchRig) {
			rig.wantQuiet(rig.engine("working", `{"session_id":"s1"}`))
		}, "working"},
		{"a release", fastWatch, func(rig *watchRig) {
			rig.wantQuiet(rig.engine("end", `{"session_id":"s1","reason":"prompt_input_exit"}`))
		}, ""},
		{"a second block", fastWatch, func(rig *watchRig) { rig.block("Edit") }, "blocked"},
		{
			"its lifetime",
			herdrWatchTiming{poll: 10 * time.Millisecond, lifetime: 300 * time.Millisecond},
			nil,
			"blocked",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newWatchRig(t)
			argv := rig.block("Bash")
			started := time.Now()
			watch := rig.start(argv, tc.timing)
			if tc.stop != nil {
				time.Sleep(5 * tc.timing.poll)
				tc.stop(rig)
			}
			watch.wait(t, 3*time.Second)
			if tc.stop == nil && watch.ended.Sub(started) < tc.timing.lifetime {
				t.Fatalf(
					"the watch ended after %s, before its %s lifetime",
					watch.ended.Sub(started),
					tc.timing.lifetime,
				)
			}
			if watch.rc != 0 || watch.stderr.String() != "" {
				t.Fatalf("watch rc=%d stderr=%q, want a quiet exit 0", watch.rc, watch.stderr.String())
			}
			before, sent := rig.tmux.stored, len(rig.herdr.calls())
			rig.appendEntries(watchRejectionEntry, watchInterruptEntry)
			time.Sleep(5 * tc.timing.poll)
			if got := rig.herdr.calls()[sent:]; len(got) != 0 || rig.tmux.stored != before {
				t.Fatalf(
					"after the stopped watch: requests %q, stored %q, want none and %q",
					got,
					rig.tmux.stored,
					before,
				)
			}
			if state := rig.tmux.storedState(t); state.State != tc.state {
				t.Fatalf("stored = %q, want state %q", rig.tmux.stored, tc.state)
			}
		})
	}
	t.Run("the second block's watch reports", func(t *testing.T) {
		rig := newWatchRig(t)
		first := rig.watchOf(rig.block("Bash"))
		second := rig.block("Edit")
		first.wait(t, 3*time.Second)
		blocked := rig.tmux.storedState(t)
		watch := rig.start(second, fastWatch)
		rig.appendEntries(watchRejectionEntry, watchInterruptEntry)
		watch.wait(t, 3*time.Second)
		if state := rig.tmux.storedState(t); state.State != "idle" || state.Seq != blocked.Seq+1 {
			t.Fatalf("stored = %q, want idle at seq %d", rig.tmux.stored, blocked.Seq+1)
		}
	})
}

func (rig *watchRig) watchOf(argv []string) *runningWatch { return rig.start(argv, fastWatch) }

// The watch's idle is written only over the very block it watches: a newer
// state that reached the stored option without removing the record (or a
// newer block) wins, and the watch reports nothing.
func TestHerdrWatchNeverReportsOverANewerState(t *testing.T) {
	for _, newer := range []string{"working", "blocked"} {
		t.Run(newer, func(t *testing.T) {
			rig := newWatchRig(t)
			argv := rig.block("Bash")
			state := rig.tmux.storedState(t)
			state.State, state.Seq = newer, state.Seq+5
			rig.seed(state)
			before, sent := rig.tmux.stored, len(rig.herdr.calls())
			watch := rig.start(argv, fastWatch)
			rig.appendEntries(watchRejectionEntry, watchInterruptEntry)
			watch.wait(t, 3*time.Second)
			if got := rig.herdr.calls()[sent:]; len(got) != 0 || rig.tmux.stored != before {
				t.Fatalf("requests %q, stored %q, want none and the newer %q", got, rig.tmux.stored, before)
			}
		})
	}
}

// A watch that cannot start or cannot read leaves the hook's own report as it
// is: the blocked state still reaches Herdr, the hook exits 0, and the error
// lands in the session's reporter error log, the one pfm doctor reads.
func TestHerdrWatchIsFailOpen(t *testing.T) {
	blockedCalls := []string{listCall, reportCall("w1:p1", "blocked", "waiting for approval: Bash")}
	hookCases := []struct {
		name, logged string
		transcript   func(rig *watchRig) string
		spawnErr     error
	}{
		{"no transcript_path", "transcript", func(*watchRig) string { return "" }, nil},
		{"a relative transcript_path", "transcript", func(*watchRig) string { return "s1.jsonl" }, nil},
		{
			"a missing transcript",
			"transcript",
			func(rig *watchRig) string { return rig.transcript + ".gone.jsonl" },
			nil,
		},
		{"a directory as transcript", "transcript", func(rig *watchRig) string {
			dir := filepath.Join(filepath.Dir(rig.transcript), "dir.jsonl")
			if err := os.Mkdir(dir, 0o700); err != nil {
				rig.t.Fatal(err)
			}
			return dir
		}, nil},
		{
			"a spawn that fails",
			"fork refused",
			func(rig *watchRig) string { return rig.transcript },
			errors.New("fork refused"),
		},
	}
	for _, tc := range hookCases {
		t.Run("hook: "+tc.name, func(t *testing.T) {
			rig := newWatchRig(t)
			if tc.spawnErr != nil {
				rig.jail.spawn = func([]string) error { return tc.spawnErr }
			}
			got := rig.engine("blocked", rig.blockedPayload("Bash", tc.transcript(rig)))
			if got.rc != 0 {
				t.Fatalf("rc=%d stderr=%q, want exit 0", got.rc, got.stderr)
			}
			rig.wantCalls(blockedCalls...)
			if state := rig.tmux.storedState(t); state.State != "blocked" {
				t.Fatalf("stored = %q, want blocked", rig.tmux.stored)
			}
			if len(rig.spawned) != 0 {
				t.Fatalf("spawned %q, want no watch", rig.spawned)
			}
			if logged := rig.jail.logged("cc-1"); !strings.Contains(logged, tc.logged) {
				t.Fatalf("error log = %q, want a line naming %q", logged, tc.logged)
			}
			if _, err := os.Stat(rig.record()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("watch record: %v, want none", err)
			}
		})
	}
	watchCases := []struct {
		name    string
		breakIt func(rig *watchRig)
	}{
		{"the transcript removed", func(rig *watchRig) { _ = os.Remove(rig.transcript) }},
		{"the transcript replaced by a directory", func(rig *watchRig) {
			_ = os.Remove(rig.transcript)
			if err := os.Mkdir(rig.transcript, 0o700); err != nil {
				rig.t.Fatal(err)
			}
		}},
		{"the transcript shrunk", func(rig *watchRig) { _ = os.Truncate(rig.transcript, 0) }},
		// A FIFO with no writer would block an open forever, past the lifetime.
		{"the transcript replaced by a FIFO", func(rig *watchRig) {
			_ = os.Remove(rig.transcript)
			if err := syscall.Mkfifo(rig.transcript, 0o600); err != nil {
				rig.t.Fatal(err)
			}
		}},
	}
	for _, tc := range watchCases {
		t.Run("watch: "+tc.name, func(t *testing.T) {
			rig := newWatchRig(t)
			argv := rig.block("Bash")
			before, sent := rig.tmux.stored, len(rig.herdr.calls())
			watch := rig.start(argv, fastWatch)
			time.Sleep(5 * fastWatch.poll)
			tc.breakIt(rig)
			watch.wait(t, 3*time.Second)
			if watch.rc != 0 {
				t.Fatalf("watch rc=%d, want 0", watch.rc)
			}
			if got := rig.herdr.calls()[sent:]; len(got) != 0 || rig.tmux.stored != before {
				t.Fatalf("requests %q, stored %q, want none and the block %q", got, rig.tmux.stored, before)
			}
			if logged := rig.jail.logged("cc-1"); !strings.Contains(logged, "transcript") {
				t.Fatalf("error log = %q, want a line naming the transcript", logged)
			}
		})
	}
	t.Run("a Codex block starts no watch", func(t *testing.T) {
		rig := newWatchRig(t)
		if err := os.WriteFile(rig.transcript,
			[]byte(`{"type":"session_meta","payload":{"id":"s1","source":"cli"}}`+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		// Codex TUI pid 50 is the chat pane's process, registered by its SessionStart.
		rig.jail.procs = map[int]fakeProc{100: {50, "pfm"}, 50: {1, "codex"}}
		rig.jail.argv = map[int][]string{50: {"codex"}}
		rig.jail.paneEnv = map[int]map[string]string{50: {"TMUX": rigSocket + ",1,0", "TMUX_PANE": "%0"}}
		rig.wantQuiet(rig.run([]string{"--engine", "codex", "start"}, `{"session_id":"s1","source":"startup"}`))
		calls := len(rig.herdr.calls())
		rig.wantQuiet(rig.run([]string{"--engine", "codex", "blocked"}, rig.blockedPayload("Bash", rig.transcript)))
		if len(rig.herdr.calls()) == calls {
			t.Fatalf("herdr requests = %q, want the block reported", rig.herdr.calls())
		}
		if len(rig.spawned) != 0 {
			t.Fatalf("spawned %q, want no watch for Codex", rig.spawned)
		}
	})
	// The user's own tmux keeps no watch: its session names are not unique
	// across servers, so a record keyed by one could end another's watch.
	t.Run("a block in the user's own tmux starts no watch", func(t *testing.T) {
		rig := newWatchRig(t)
		rig.env["TMUX"] = "/private/tmp/tmux-501/default,1,0"
		rig.wantQuiet(rig.engine("blocked", rig.blockedPayload("Bash", rig.transcript)))
		if len(rig.spawned) != 0 {
			t.Fatalf("spawned %q, want no watch outside a pfm chat server", rig.spawned)
		}
		if _, err := os.Stat(rig.record()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("watch record: %v, want none", err)
		}
	})
}

// The watch verb reaches the watch, never the reporter's event parser.
func TestHerdrStateRoutesTheWatchVerb(t *testing.T) {
	var stderr bytes.Buffer
	clock := int64(1)
	rc := routeHerdrState([]string{herdrWatchVerb}, strings.NewReader(""), &stderr, newHerdrJail(t).deps(nil, &clock))
	if rc != 1 || !strings.Contains(stderr.String(), "--transcript") {
		t.Fatalf("rc=%d stderr=%q, want the watch's usage error (exit 1)", rc, stderr.String())
	}
}

// Only a main-thread user entry whose content is exactly the interrupt marker
// ends the watch; every other form Claude writes, or a re-carried copy, does not.
func TestClaudeToolInterrupt(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		want  bool
	}{
		{"the interrupt text block", watchInterruptEntry, true},
		{
			"the interrupt as string content",
			`{"type":"user","isSidechain":false,"message":{"role":"user","content":"[Request interrupted by user for tool use]"}}`,
			true,
		},
		{
			"a sub-agent's interrupt",
			strings.Replace(watchInterruptEntry, `"isSidechain":false`, `"isSidechain":true`, 1), false,
		},
		{"the rejection alone", watchRejectionEntry, false},
		{"the next request's blob re-carrying it", `{"type":"api-request-blob","content":[{"type":"text",` +
			`"text":"[Request interrupted by user for tool use]"}]}`, false},
		{"an assistant quoting it", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text",` +
			`"text":"[Request interrupted by user for tool use]"}]}}`, false},
		{"a prompt that mentions it", `{"type":"user","message":{"role":"user","content":` +
			`"why [Request interrupted by user for tool use]?"}}`, false},
		{"a torn line", watchInterruptEntry[20:], false},
		{"the plain interrupt of a model turn", `{"type":"user","message":{"role":"user","content":[{"type":"text",` +
			`"text":"[Request interrupted by user]"}]}}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := claudeToolInterrupt([]byte(tc.entry + "\n")); got != tc.want {
				t.Fatalf("claudeToolInterrupt(%s) = %v, want %v", tc.entry, got, tc.want)
			}
		})
	}
}

// The scan reads only complete lines past its offset: a line torn by the
// offset is skipped, a line still being written is read once it ends, and a
// line longer than any interrupt entry is passed over without being held.
func TestHerdrWatchScan(t *testing.T) {
	write := func(t *testing.T, path, content string) {
		t.Helper()
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = file.Close() }()
		if _, err := file.WriteString(content); err != nil {
			t.Fatal(err)
		}
	}
	scan := func(t *testing.T, watch *herdrWatch, want bool) {
		t.Helper()
		got, err := watch.scan()
		if err != nil || got != want {
			t.Fatalf("scan at offset %d = %v, %v; want %v, nil", watch.offset, got, err, want)
		}
	}
	t.Run("an offset inside a line", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		write(t, path, watchInterruptEntry+"\n")
		watch := &herdrWatch{transcript: path, offset: int64(len(watchInterruptEntry) / 2)}
		scan(t, watch, false)
		write(t, path, watchRejectionEntry+"\n")
		scan(t, watch, false)
		write(t, path, watchInterruptEntry+"\n")
		scan(t, watch, true)
	})
	t.Run("a line still being written", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		write(t, path, watchToolUseEntry+"\n")
		watch := &herdrWatch{transcript: path, offset: int64(len(watchToolUseEntry) + 1)}
		write(t, path, watchInterruptEntry[:40])
		scan(t, watch, false)
		if watch.offset != int64(len(watchToolUseEntry)+1) {
			t.Fatalf("offset = %d, want it kept at the unfinished line's start", watch.offset)
		}
		write(t, path, watchInterruptEntry[40:]+"\n")
		scan(t, watch, true)
	})
	t.Run("a line longer than any interrupt entry", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		write(t, path, "")
		watch := &herdrWatch{transcript: path}
		huge := `{"type":"user","message":{"role":"user","content":"` + strings.Repeat("x", 300<<10)
		write(t, path, huge)
		scan(t, watch, false)
		write(t, path, `[Request interrupted by user for tool use]"}}`+"\n")
		scan(t, watch, false)
		write(t, path, watchInterruptEntry+"\n")
		scan(t, watch, true)
	})
}
