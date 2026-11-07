package hookentry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
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
	// watchInterruptAt is watchInterruptEntry's timestamp in nanoseconds.
	watchInterruptAt = 1792141051254000000
	// The entry Claude Code 2.1.317 writes when the user presses Esc while the
	// model streams, after its tool calls, from a live transcript (ids, cwd and
	// time replaced): no hook fires for it.
	watchModelInterruptEntry = `{"parentUuid":"f2e1d0c9-1a2b-4c3d-8e4f-5a6b7c8d9e0f","isSidechain":false,` +
		`"promptId":"0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d","type":"user","message":{"role":"user","content":` +
		`[{"type":"text","text":"[Request interrupted by user]"}]},"uuid":"5d4c3b2a-1f0e-4d9c-8b7a-6f5e4d3c2b1a",` +
		`"timestamp":"2026-10-16T08:57:41.146Z","interruptedMessageId":"msg_01AbCdEfGhIjKlMnOpQrStUv",` +
		`"session_id":"35a503c8-b5df-4d33-87fd-9b7f6e91e79c","userType":"external","entrypoint":"cli",` +
		`"cwd":"/tmp/proj","sessionId":"35a503c8-b5df-4d33-87fd-9b7f6e91e79c","version":"2.1.317","gitBranch":"HEAD"}`
	// watchModelInterruptAt is watchModelInterruptEntry's timestamp in nanoseconds.
	watchModelInterruptAt = 1792141061146000000
	watchPermission       = `{"session_id":"s1","notification_type":"permission_prompt",` +
		`"message":"Claude needs your permission to use Bash"}`
	// watchRigClock is when the rig's hooks run (2026-10-16T08:57:00Z), before
	// every entry; watchClock is the watch's own clock (08:58:00Z), after them.
	watchRigClock = 1792141020000000000
	watchClock    = 1792141080000000000
)

// fastPoll is the poll of the tests that wait for a watch to end: they see it
// end within a few ticks of its cause.
const fastPoll = 10 * time.Millisecond

// stampedInterrupt is the model turn's interrupt entry written at stamp.
func stampedInterrupt(stamp string) string {
	return strings.Replace(watchModelInterruptEntry, "2026-10-16T08:57:41.146Z", stamp, 1)
}

// watchRig is a chat rig whose Claude chat is working, attached from w1:p1,
// with a transcript holding the tool_use a permission dialog asks about, and a
// spawn that records each detached watch a hook starts. Its engine is pid 50,
// claude, until engineGone.
type watchRig struct {
	*chatRig
	transcript string
	spawned    [][]string
	engineGone atomic.Bool
}

func newWatchRig(t *testing.T) *watchRig {
	t.Helper()
	rig := &watchRig{chatRig: newChatRig(t), transcript: filepath.Join(t.TempDir(), "s1.jsonl")}
	rig.clock = watchRigClock
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

// payload is a hook payload of session s1 naming the rig's transcript, plus
// extra fields.
func (rig *watchRig) payload(extra string) string {
	return fmt.Sprintf(`{"session_id":"s1","transcript_path":%q%s}`, rig.transcript, extra)
}

func (rig *watchRig) blockedPayload(tool, transcript string) string {
	return fmt.Sprintf(`{"session_id":"s1","hook_event_name":"PermissionRequest","tool_name":%q,"transcript_path":%q}`,
		tool, transcript)
}

// starts runs one hook and returns the one watch it started.
func (rig *watchRig) starts(event, stdin string) []string {
	rig.t.Helper()
	before := len(rig.spawned)
	rig.wantQuiet(rig.engine(event, stdin))
	if len(rig.spawned) != before+1 {
		rig.t.Fatalf("%s started %d watches, want 1: %q", event, len(rig.spawned)-before, rig.spawned[before:])
	}
	return rig.spawned[len(rig.spawned)-1]
}

// block runs the PermissionRequest hook and returns the watch it started.
func (rig *watchRig) block(tool string) []string {
	rig.t.Helper()
	return rig.starts("blocked", rig.blockedPayload(tool, rig.transcript))
}

// prompt runs the UserPromptSubmit hook and returns the watch it started.
func (rig *watchRig) prompt() []string {
	rig.t.Helper()
	return rig.starts("working", rig.payload(`,"hook_event_name":"UserPromptSubmit"`))
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

func (rig *watchRig) record() string {
	return (&herdrReporter{deps: herdrDeps{lockDir: rig.jail.lockDir}}).watchRecord("s1")
}

// watchDeps are the seams a watch process of the rig runs on: its own clock,
// the rig's tmux, and the engine gone once engineGone is set.
func (rig *watchRig) watchDeps() herdrDeps {
	clock := int64(watchClock)
	deps := rig.jail.deps(rig.env, &clock)
	deps.tmux = rig.tmux.run
	deps.listPanes = rig.tmux.listPanes
	procInfo := deps.procInfo
	deps.procInfo = func(pid int) (int, string, error) {
		if pid == 50 && rig.engineGone.Load() {
			return 0, "", fmt.Errorf("fake process table has no pid %d: %w", pid, fs.ErrNotExist)
		}
		return procInfo(pid)
	}
	return deps
}

// runningWatch is one watch process played in a goroutine with its own clock.
type runningWatch struct {
	done   chan struct{}
	rc     int
	stderr bytes.Buffer
	ended  time.Time
}

// start plays the spawned argv the way the detached child runs it.
func (rig *watchRig) start(argv []string, poll time.Duration) *runningWatch {
	rig.t.Helper()
	if len(argv) < 3 || !slices.Equal(argv[:3], []string{"internal", "herdr-state", herdrWatchVerb}) {
		rig.t.Fatalf("spawned argv = %q, want internal herdr-state watch …", argv)
	}
	deps := rig.watchDeps()
	watch := &runningWatch{done: make(chan struct{})}
	go func() {
		defer close(watch.done)
		watch.rc = watchForHerdr(argv[3:], &watch.stderr, deps, poll)
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

// waitClaimed waits for the started watch to claim its record with its own
// pid and command (the rig's pid 100, pfm).
func (rig *watchRig) waitClaimed(argv []string) {
	rig.t.Helper()
	claimed := flagValue(rig.t, argv, "--seq") + " 100 pfm"
	deadline := time.Now().Add(3 * time.Second)
	for {
		data, err := os.ReadFile(rig.record())
		if err == nil && strings.TrimSpace(string(data)) == claimed {
			return
		}
		if time.Now().After(deadline) {
			rig.t.Fatalf("watch record = %q (%v), want claimed as %q", data, err, claimed)
		}
		<-time.After(fastPoll)
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

// A Claude chat whose user interrupts its turn (Esc or No at a permission
// dialog, Esc while the model streams or a tool runs) is reported idle within
// 2 s of the interrupt entry reaching its transcript, stored at the entry's
// own time, its theme state done while the theme sync is on, and the watch's
// record is gone.
func TestHerdrWatchReportsIdleOnTheInterruptEntry(t *testing.T) {
	cases := []struct {
		name    string
		start   func(rig *watchRig) []string
		after   func(rig *watchRig)
		entries []string
		seq     uint64
	}{
		{
			"Esc or No at a permission dialog", func(rig *watchRig) []string { return rig.block("Bash") },
			func(*watchRig) {},
			[]string{watchRejectionEntry, watchInterruptEntry},
			watchInterruptAt,
		},
		{
			"the permission notification keeps the watch", func(rig *watchRig) []string { return rig.block("Bash") },
			func(rig *watchRig) { rig.wantQuiet(rig.engine("notification", watchPermission)) },
			[]string{watchRejectionEntry, watchInterruptEntry},
			watchInterruptAt,
		},
		{
			"the theme sync on", func(rig *watchRig) []string { return rig.block("Bash") },
			func(rig *watchRig) { rig.themeOn() },
			[]string{watchRejectionEntry, watchInterruptEntry},
			watchInterruptAt,
		},
		{
			"Esc while the model streams", func(rig *watchRig) []string { return rig.prompt() },
			func(*watchRig) {},
			[]string{watchModelInterruptEntry},
			watchModelInterruptAt,
		},
		{
			// The interrupted tool's failure leaves the turn working for the watch.
			"Esc while a tool runs", func(rig *watchRig) []string { return rig.prompt() },
			func(rig *watchRig) {
				rig.wantQuiet(rig.engine("working",
					rig.payload(`,"hook_event_name":"PostToolUseFailure","tool_name":"Bash","is_interrupt":true`)))
				if state := rig.tmux.storedState(rig.t); state.State != "working" {
					rig.t.Fatalf("stored after the interrupted tool = %q, want working", rig.tmux.stored)
				}
			},
			[]string{watchInterruptEntry},
			watchInterruptAt,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newWatchRig(t)
			info, err := os.Stat(rig.transcript)
			if err != nil {
				t.Fatal(err)
			}
			argv := tc.start(rig)
			started := rig.tmux.storedState(t)
			for flag, want := range map[string]string{
				"--socket": rigSocket, "--session": "cc-1", "--pid": "50", "--command": "claude",
				"--seq": strconv.FormatUint(started.Seq, 10), "--since": strconv.FormatUint(started.Seq, 10),
				"--transcript": rig.transcript,
				"--offset":     strconv.FormatInt(info.Size(), 10),
			} {
				if got := flagValue(t, argv, flag); got != want {
					t.Fatalf("watch %s = %q, want %q (argv %q)", flag, got, want, argv)
				}
			}
			if session := argv[len(argv)-1]; session != "s1" {
				t.Fatalf("watch session id = %q, want s1 last (argv %q)", session, argv)
			}
			tc.after(rig)
			sent := len(rig.herdr.calls())
			watch := rig.start(argv, herdrWatchPoll)
			time.Sleep(2 * herdrWatchPoll) // the watch is polling when the entries land
			appended := time.Now()
			rig.appendEntries(tc.entries...)
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
			if state := rig.tmux.storedState(t); state.State != "idle" || state.Seq != tc.seq {
				t.Fatalf("stored = %q, want idle at the interrupt's seq %d", rig.tmux.stored, tc.seq)
			}
			if _, err := os.Stat(rig.record()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("watch record after the idle: %v, want it gone", err)
			}
			if len(rig.spawned) != 1 {
				t.Fatalf("spawned %q, want the one watch", rig.spawned)
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

// A watch ends when the main turn closes (a Stop, a release, a /clear), when
// the next turn's watch replaces it, and when the chat's engine exits without
// its SessionEnd; an interrupt entry after it ended reports nothing, so the
// newer state stays.
func TestHerdrWatchStops(t *testing.T) {
	cases := []struct {
		name  string
		stop  func(rig *watchRig)
		state string
	}{
		{"the Stop", func(rig *watchRig) { rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`)) }, "idle"},
		{"a release", func(rig *watchRig) {
			rig.wantQuiet(rig.engine("end", `{"session_id":"s1","reason":"prompt_input_exit"}`))
		}, ""},
		{"a /clear", func(rig *watchRig) {
			rig.wantQuiet(rig.engine("end", `{"session_id":"s1","reason":"clear"}`))
		}, "blocked"},
		{"the next turn's watch", func(rig *watchRig) {
			rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
			rig.prompt()
		}, "working"},
		{"the chat's engine exits", func(rig *watchRig) { rig.engineGone.Store(true) }, "blocked"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rig := newWatchRig(t)
			argv := rig.block("Bash")
			watch := rig.start(argv, fastPoll)
			time.Sleep(5 * fastPoll)
			tc.stop(rig)
			watch.wait(t, 3*time.Second)
			if watch.rc != 0 || watch.stderr.String() != "" {
				t.Fatalf("watch rc=%d stderr=%q, want a quiet exit 0", watch.rc, watch.stderr.String())
			}
			before, sent := rig.tmux.stored, len(rig.herdr.calls())
			rig.appendEntries(watchRejectionEntry, watchInterruptEntry)
			time.Sleep(5 * fastPoll)
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
	// A turn's every hook keeps its one watch: no tool, failure, dialog or
	// answer starts another, and that watch settles the turn.
	t.Run("one watch per turn", func(t *testing.T) {
		rig := newWatchRig(t)
		argv := rig.prompt()
		for _, hook := range []struct{ event, stdin string }{
			{"working", rig.payload(`,"hook_event_name":"PostToolUse","tool_name":"Read"`)},
			{"working", rig.payload(`,"hook_event_name":"PostToolUseFailure","tool_name":"Grep"`)},
			{"blocked", rig.blockedPayload("Bash", rig.transcript)},
			{"notification", watchPermission},
			{"working", rig.payload(`,"hook_event_name":"PostToolUse","tool_name":"Bash"`)},
		} {
			rig.wantQuiet(rig.engine(hook.event, hook.stdin))
		}
		if len(rig.spawned) != 1 {
			t.Fatalf("spawned %d watches over the turn, want 1: %q", len(rig.spawned), rig.spawned)
		}
		watch := rig.start(argv, fastPoll)
		rig.appendEntries(watchModelInterruptEntry)
		watch.wait(t, 3*time.Second)
		if state := rig.tmux.storedState(t); state.State != "idle" || state.Seq != watchModelInterruptAt {
			t.Fatalf("stored = %q, want idle at seq %d", rig.tmux.stored, uint64(watchModelInterruptAt))
		}
	})
	// A watch whose engine exited removed its record: the session's next
	// working event starts a new one.
	t.Run("a retired watch is renewed", func(t *testing.T) {
		rig := newWatchRig(t)
		watch := rig.start(rig.block("Bash"), fastPoll)
		rig.engineGone.Store(true)
		watch.wait(t, 3*time.Second)
		if _, err := os.Stat(rig.record()); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("watch record after its engine exited: %v, want it gone", err)
		}
		renewed := rig.starts("working", rig.payload(`,"hook_event_name":"PostToolUse","tool_name":"Bash"`))
		want := strconv.FormatUint(rig.tmux.storedState(t).Seq, 10)
		if got := flagValue(t, renewed, "--seq"); got != want {
			t.Fatalf("renewed watch --seq = %s, want the working event's %s", got, want)
		}
	})
	// A running watch claims its record with its own pid: a hook leaves it be
	// while that process runs, and replaces a watch killed mid-turn.
	t.Run("a killed watch is replaced", func(t *testing.T) {
		rig := newWatchRig(t)
		argv := rig.prompt()
		watch := rig.start(argv, fastPoll)
		rig.waitClaimed(argv)
		tool := rig.payload(`,"hook_event_name":"PostToolUse","tool_name":"Bash"`)
		rig.wantQuiet(rig.engine("working", tool))
		if len(rig.spawned) != 1 {
			t.Fatalf("spawned %q beside the running watch, want none", rig.spawned[1:])
		}
		if err := os.WriteFile(rig.record(), []byte(flagValue(t, argv, "--seq")+" 4242 pfm\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		renewed := rig.starts("working", tool)
		if got, want := flagValue(
			t,
			renewed,
			"--seq",
		), strconv.FormatUint(
			rig.tmux.storedState(t).Seq,
			10,
		); got != want {
			t.Fatalf("replacing watch --seq = %s, want the working event's %s", got, want)
		}
		watch.wait(t, 3*time.Second) // the record names the replacement now
	})
}

// The watch's idle is ordered by the interrupt's own time: a hook that
// started after the interrupt (the next prompt, a new dialog) wins, and the
// watch, its record kept, goes on to settle that later turn's interrupt.
func TestHerdrWatchNeverReportsOverANewerState(t *testing.T) {
	for _, newer := range []struct{ name, event, state string }{
		{"a prompt", "working", "working"},
		{"a dialog", "blocked", "blocked"},
	} {
		t.Run(newer.name, func(t *testing.T) {
			rig := newWatchRig(t)
			argv := rig.prompt()
			rig.appendEntries(watchModelInterruptEntry)
			rig.clock = watchModelInterruptAt + int64(time.Second)
			stdin := rig.payload(`,"hook_event_name":"UserPromptSubmit"`)
			if newer.event == "blocked" {
				stdin = rig.blockedPayload("Edit", rig.transcript)
			}
			rig.wantQuiet(rig.engine(newer.event, stdin))
			if len(rig.spawned) != 1 {
				t.Fatalf("spawned %q, want the turn's one watch kept", rig.spawned)
			}
			before, sent := rig.tmux.stored, len(rig.herdr.calls())
			watch := rig.start(argv, fastPoll)
			time.Sleep(5 * fastPoll)
			if got := rig.herdr.calls()[sent:]; len(got) != 0 || rig.tmux.stored != before {
				t.Fatalf("requests %q, stored %q, want none and the newer %q", got, rig.tmux.stored, before)
			}
			if state := rig.tmux.storedState(t); state.State != newer.state {
				t.Fatalf("stored = %q, want the newer %s", rig.tmux.stored, newer.state)
			}
			rig.appendEntries(stampedInterrupt("2026-10-16T08:57:43.146Z"))
			watch.wait(t, 3*time.Second)
			if state := rig.tmux.storedState(t); state.State != "idle" || state.Seq != 1792141063146000000 {
				t.Fatalf("stored = %q, want the later interrupt's idle at seq 1792141063146000000", rig.tmux.stored)
			}
		})
	}
}

// A hook Claude spawned before the interrupt is ordered by its spawn, however
// late it runs: its working never outranks the interrupt's idle, before or
// after the watch reports, and a late prompt hook's own watch still finds the
// interrupt written before it ran. A sub-agent stopping after the interrupt
// leaves the main turn's idle to the watch.
func TestHerdrWatchIdleOutranksAHookSpawnedBeforeTheInterrupt(t *testing.T) {
	const spawned = watchModelInterruptAt - int64(time.Second)
	tool := `,"hook_event_name":"PostToolUse","tool_name":"Bash"`
	late := func(rig *watchRig, event, stdin string) {
		rig.jail.started = map[int]int64{100: spawned}
		rig.clock = watchModelInterruptAt + 2*int64(time.Second)
		rig.wantQuiet(rig.engine(event, stdin))
		rig.jail.started = nil
	}
	for _, tc := range []struct {
		name string
		turn func(rig *watchRig) []string // the turn's watch, its interrupt written
		then func(rig *watchRig)          // after the watch ended
	}{
		{"a tool hook run before the watch reports", func(rig *watchRig) []string {
			argv := rig.prompt()
			rig.appendEntries(watchModelInterruptEntry)
			late(rig, "working", rig.payload(tool))
			return argv
		}, func(*watchRig) {}},
		{"a tool hook run after the watch reported", func(rig *watchRig) []string {
			argv := rig.prompt()
			rig.appendEntries(watchModelInterruptEntry)
			return argv
		}, func(rig *watchRig) { late(rig, "working", rig.payload(tool)) }},
		{"a prompt hook run after the interrupt", func(rig *watchRig) []string {
			rig.appendEntries(watchModelInterruptEntry)
			rig.jail.started = map[int]int64{100: spawned}
			rig.clock = watchModelInterruptAt + 2*int64(time.Second)
			argv := rig.prompt()
			rig.jail.started = nil
			if since := flagValue(rig.t, argv, "--since"); since != strconv.FormatInt(spawned, 10) {
				rig.t.Fatalf("watch --since = %s, want the prompt's spawn %d", since, spawned)
			}
			return argv
		}, func(*watchRig) {}},
		{"a sub-agent stopping after the interrupt", func(rig *watchRig) []string {
			argv := rig.prompt()
			rig.wantQuiet(rig.engine("agent-start", `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStart"}`))
			rig.appendEntries(watchModelInterruptEntry)
			rig.clock = watchModelInterruptAt + int64(time.Second)
			rig.wantQuiet(rig.engine("agent-stop", `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStop"}`))
			return argv
		}, func(*watchRig) {}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rig := newWatchRig(t)
			watch := rig.start(tc.turn(rig), fastPoll)
			watch.wait(t, 3*time.Second)
			tc.then(rig)
			state := rig.tmux.storedState(t)
			if state.State != "idle" || state.MainSeq != watchModelInterruptAt {
				t.Fatalf("stored = %q, want idle with the interrupt's main seq %d", rig.tmux.stored,
					uint64(watchModelInterruptAt))
			}
			if len(rig.spawned) != 1 {
				t.Fatalf("spawned %q, want the turn's one watch", rig.spawned)
			}
		})
	}
}

// A hook is ordered by its spawn, the process right under the engine, while
// that lies within herdrFireWindow before the run; otherwise, or with no
// spawn to read, by its run.
func TestHerdrHookFiredSeq(t *testing.T) {
	const ran = 1792141080000001000 // when the hook began
	for _, tc := range []struct {
		name    string
		started map[int]int64
		engine  string
		between []string // the command line of a process between the hook and the engine; nil: none, empty: unreadable
		want    uint64
		logged  string
	}{
		{"spawned a second before its run", map[int]int64{100: ran - int64(time.Second)}, "claude", nil, ran - uint64(time.Second), ""},
		{
			"spawned at the window's edge",
			map[int]int64{100: ran - int64(herdrFireWindow)},
			"claude", nil, ran - uint64(herdrFireWindow), "",
		},
		{"spawned past the window", map[int]int64{100: ran - int64(herdrFireWindow) - 1}, "claude", nil, ran, ""},
		{"spawned after its run", map[int]int64{100: ran + 1}, "claude", nil, ran, ""},
		{"a spawn the table cannot read", map[int]int64{}, "claude", nil, ran, "ordered by its run, not its spawn"},
		{"no spawn clock", nil, "claude", nil, ran, ""},
		{"a native install named by its version", map[int]int64{100: ran - int64(time.Second)}, "2.1.3", nil, ran - uint64(time.Second), ""},
		{"under an engine that is no Claude process", map[int]int64{100: ran - int64(time.Second)}, "node", nil, ran, ""},
		{"run in a shell's -c", map[int]int64{60: ran - int64(time.Second)}, "claude", []string{"sh", "-c", "pfm"}, ran - uint64(time.Second), ""},
		{"under a long-lived child of the engine", map[int]int64{60: ran - int64(time.Second)}, "claude", []string{"bash", "fake"}, ran, ""},
		{"a child whose command line is unreadable", map[int]int64{60: ran - int64(time.Second)}, "claude", []string{}, ran, "ordered by its run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jail := newHerdrJail(t)
			jail.started = tc.started
			jail.procs[50] = fakeProc{1, tc.engine}
			if tc.between != nil {
				jail.procs[100], jail.procs[60] = fakeProc{60, "pfm"}, fakeProc{50, "sh"}
				if len(tc.between) > 0 {
					jail.argv = map[int][]string{60: tc.between}
				}
			}
			clock := int64(ran)
			reporter := &herdrReporter{
				deps: jail.deps(nil, &clock), seq: ran, session: "cc-1", stderr: io.Discard,
				options: herdrOptions{engine: pfmengine.Claude, event: "working"},
			}
			if got := reporter.firedSeq(); got != tc.want {
				t.Fatalf("firedSeq = %d, want %d", got, tc.want)
			}
			if logged := jail.logged(
				"cc-1",
			); (tc.logged == "") != (logged == "") ||
				!strings.Contains(logged, tc.logged) {
				t.Fatalf("error log = %q, want a line naming %q (or none)", logged, tc.logged)
			}
		})
	}
}

// An idle the watch could not deliver (the activity lock held past the
// deadline) is tried again at the next poll, never dropped.
func TestHerdrWatchRetriesAnIdleItCouldNotDeliver(t *testing.T) {
	rig := newWatchRig(t)
	argv := rig.prompt()
	watch := rig.start(argv, fastPoll)
	rig.waitClaimed(argv)
	holder := &herdrReporter{deps: rig.watchDeps(), stderr: io.Discard}
	unlock, err := holder.lock(context.Background(), holder.activityKey("s1"))
	if err != nil {
		t.Fatal(err)
	}
	rig.appendEntries(watchModelInterruptEntry)
	deadline := time.Now().Add(2 * herdrDeadline)
	for !strings.Contains(rig.jail.logged("cc-1"), "still held at the deadline") {
		if time.Now().After(deadline) {
			unlock()
			t.Fatalf("error log = %q, want the held lock named", rig.jail.logged("cc-1"))
		}
		<-time.After(fastPoll)
	}
	unlock()
	watch.wait(t, 3*time.Second)
	if state := rig.tmux.storedState(t); state.State != "idle" || state.Seq != watchModelInterruptAt {
		t.Fatalf("stored = %q, want idle at the interrupt's seq %d", rig.tmux.stored, uint64(watchModelInterruptAt))
	}
}

// A watcher is ordered by the interrupt it reports, not the later
// reannouncement's wall clock. Rebinding and reannouncing must not strand its
// pane as blocked.
func TestHerdrStateWatcherSurvivesBridgeReconciliation(t *testing.T) {
	for _, operation := range []string{"reannounce", "move", "adopt"} {
		t.Run(operation, func(t *testing.T) {
			rig := newChatRig(t)
			clients := map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}
			if operation == "adopt" {
				clients = nil
			}
			state := rig.registered("blocked", clients)
			state.Seq = 700
			rig.seed(state)
			rig.tmux.clients = []string{"4242"}
			rig.clientEnv(4242, "w1:p1")
			deps := rig.jail.deps(rig.env, &rig.clock)
			deps.tmux, deps.listPanes = rig.tmux.run, rig.tmux.listPanes
			pane := "w1:p1"
			switch operation {
			case "reannounce":
				if _, err := reannounceOn(context.Background(), rigSocket, deps); err != nil {
					t.Fatal(err)
				}
			case "move":
				rig.herdr.move(pane, "w2:p3")
				pane = "w2:p3"
				rig.wantQuiet(rig.hook("sync", "--client", "4242"))
			}
			reporter := &herdrReporter{
				deps: deps, session: "cc-1", stderr: io.Discard,
				options: herdrOptions{engine: pfmengine.Claude, event: "idle", socket: rigSocket},
			}
			watch := &herdrWatch{reporter: reporter, sessionID: "s1", record: reporter.watchRecord("s1"), seq: 700}
			if err := errors.Join(
				os.MkdirAll(rig.jail.lockDir, 0o700),
				os.WriteFile(watch.record, []byte("700"), 0o600),
			); err != nil {
				t.Fatal(err)
			}
			before := len(rig.herdr.calls())
			if err := watch.report(time.Unix(0, 701)); err != nil {
				t.Fatal(err)
			}
			if calls := rig.herdr.calls()[before:]; !slices.Contains(calls, reportCall(pane, "idle", "")) {
				t.Fatalf("watch after %s: wire=%q, want idle in %s", operation, calls, pane)
			}
			if got := rig.tmux.storedState(t); got.State != "idle" || got.Seq != 701 {
				t.Fatalf("watch stored=%+v, want idle at 701", got)
			}
		})
	}
}

// A watch that cannot start or cannot read leaves the hook's own report as it
// is: the blocked state still reaches Herdr, the hook exits 0, and the error
// lands in the session's reporter error log, the one pfm doctor reads. A
// watch that can no longer read removes its record, so the next working event
// starts a new one.
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
		// A FIFO with no writer would block an open forever.
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
			watch := rig.start(argv, fastPoll)
			time.Sleep(5 * fastPoll)
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
			if _, err := os.Stat(rig.record()); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("watch record after the unreadable transcript: %v, want it gone", err)
			}
		})
	}
	// An engine the process table cannot read may well run: the watch goes on
	// and still settles the turn.
	t.Run("watch: the engine's process unreadable", func(t *testing.T) {
		rig := newWatchRig(t)
		argv := rig.block("Bash")
		rig.jail.procFails = map[int]error{50: syscall.EACCES}
		watch := rig.start(argv, fastPoll)
		rig.appendEntries(watchRejectionEntry, watchInterruptEntry)
		watch.wait(t, 3*time.Second)
		if state := rig.tmux.storedState(t); state.State != "idle" || state.Seq != watchInterruptAt {
			t.Fatalf("stored = %q, want idle at the interrupt's seq %d", rig.tmux.stored, uint64(watchInterruptAt))
		}
		if logged := rig.jail.logged("cc-1"); strings.Count(logged, "could not be read (watching on)") != 1 {
			t.Fatalf("error log = %q, want the unreadable engine named once", logged)
		}
	})
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
	// The user's own tmux keeps no watch: it may rename the session a watch
	// reports through.
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

// Only a main-thread user entry whose content (a string or a text block) is
// Claude's interrupt marker ends the turn, at the time the entry names; every
// other form Claude writes, or a re-carried copy, does not.
func TestClaudeInterrupt(t *testing.T) {
	cases := []struct {
		name  string
		entry string
		want  bool
		at    int64
	}{
		{"the dialog's interrupt text block", watchInterruptEntry, true, watchInterruptAt},
		{"the model turn's interrupt text block", watchModelInterruptEntry, true, watchModelInterruptAt},
		{
			"the interrupt as string content",
			`{"type":"user","isSidechain":false,"message":{"role":"user","content":"[Request interrupted by user for tool use]"}}`,
			true,
			0,
		},
		{
			"an unreadable timestamp",
			strings.Replace(watchModelInterruptEntry, "2026-10-16T08:57:41.146Z", "yesterday", 1),
			true,
			0,
		},
		{
			"a sub-agent's interrupt",
			strings.Replace(watchInterruptEntry, `"isSidechain":false`, `"isSidechain":true`, 1), false, 0,
		},
		{
			"a sub-agent's model turn interrupt",
			strings.Replace(watchModelInterruptEntry, `"isSidechain":false`, `"isSidechain":true`, 1), false, 0,
		},
		{"the rejection alone", watchRejectionEntry, false, 0},
		{"the next request's blob re-carrying it", `{"type":"api-request-blob","content":[{"type":"text",` +
			`"text":"[Request interrupted by user]"}]}`, false, 0},
		{"an assistant quoting it", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text",` +
			`"text":"[Request interrupted by user for tool use]"}]}}`, false, 0},
		{"a prompt that mentions it", `{"type":"user","message":{"role":"user","content":` +
			`"why [Request interrupted by user]?"}}`, false, 0},
		{"a torn line", watchInterruptEntry[20:], false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			at, got := claudeInterrupt([]byte(tc.entry + "\n"))
			stamp := int64(0)
			if !at.IsZero() {
				stamp = at.UnixNano()
			}
			if got != tc.want || stamp != tc.at {
				t.Fatalf("claudeInterrupt(%s) = %d, %v; want %d, %v", tc.entry, stamp, got, tc.at, tc.want)
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
		_, got, err := watch.scan()
		if err != nil || got != want {
			t.Fatalf("scan at offset %d = %v, %v; want %v, nil", watch.offset, got, err, want)
		}
	}
	// An interrupt entry is read once: the scan after it goes on past it.
	t.Run("an offset inside a line", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "s.jsonl")
		write(t, path, watchInterruptEntry+"\n")
		watch := &herdrWatch{transcript: path, offset: int64(len(watchInterruptEntry) / 2)}
		scan(t, watch, false)
		write(t, path, watchRejectionEntry+"\n")
		scan(t, watch, false)
		write(t, path, watchInterruptEntry+"\n"+watchToolUseEntry+"\n")
		scan(t, watch, true)
		scan(t, watch, false)
		if size := int64(
			len(watchInterruptEntry)*2 + len(watchRejectionEntry) + len(watchToolUseEntry) + 4,
		); watch.offset != size {
			t.Fatalf("offset after the interrupt = %d, want the file's %d bytes read", watch.offset, size)
		}
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
