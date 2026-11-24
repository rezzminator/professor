package headless

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/codexmeta"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// conversation is a chat whose transcript a test writes by hand, plus the
// resolver Await calls to find it. The chat's state is written from the test's
// own goroutine while Await reads it from another, so it is mutex-guarded:
// this fixture models a live seat, and a live seat changes under its watcher.
type conversation struct {
	path    string
	guard   sync.Mutex
	chat    Chat
	found   bool
	resolve func(context.Context) (Chat, bool, error)
}

func (talk *conversation) vanish() {
	talk.guard.Lock()
	defer talk.guard.Unlock()
	talk.found = false
}

func (talk *conversation) appear() {
	talk.guard.Lock()
	defer talk.guard.Unlock()
	talk.found = true
}

func (talk *conversation) die() {
	talk.guard.Lock()
	defer talk.guard.Unlock()
	talk.chat.Live = false
}

func newConversation(t *testing.T) *conversation {
	t.Helper()
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	talk := &conversation{
		path:  path,
		found: true,
		chat: Chat{
			Name:   "worker",
			Engine: "cc",
			Path:   path,
			Socket: "cc-1-2-3",
			Live:   true,
		},
	}
	talk.resolve = func(context.Context) (Chat, bool, error) {
		talk.guard.Lock()
		defer talk.guard.Unlock()
		return talk.chat, talk.found, nil
	}
	return talk
}

// say appends to the transcript. It panics rather than calling t.Fatal because
// tests call it from their own goroutines, where a Fatal would abandon the
// test instead of failing it.
func (talk *conversation) say(lines ...string) {
	file, err := os.OpenFile(talk.path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		panic(err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			panic(fmt.Errorf("close conversation transcript: %w", err))
		}
	}()
	for _, line := range lines {
		if _, err := file.WriteString(line + "\n"); err != nil {
			panic(err)
		}
	}
}

func user(text string) string {
	return `{"type":"user","message":{"content":"` + text + `"}}`
}

func assistant(text string) string {
	return `{"type":"assistant","message":{"content":[{"type":"text","text":"` +
		text + `"}]}}`
}

func tool(name string) string {
	return `{"type":"assistant","message":{"content":[{"type":"tool_use",` +
		`"name":"` + name + `","input":{}}]}}`
}

func fastOptions() AwaitOptions {
	return AwaitOptions{
		Poll:         5 * time.Millisecond,
		Settle:       60 * time.Millisecond,
		ResolveEvery: 5 * time.Millisecond,
		Timeout:      10 * time.Second,
	}
}

// fakeAwait is one Await on a fake clock, on a goroutine of its own. The test
// writes the transcript only while Await is parked on its next poll, then moves
// the clock one Poll, so every look Await takes is one the test chose: no
// writer races Settle, on a loaded host as on an idle one.
type fakeAwait struct {
	clock *clock.Fake
	poll  time.Duration
	done  chan awaitResult
}

type awaitResult struct {
	turn Turn
	err  error
}

func startAwait(resolve func(context.Context) (Chat, bool, error), options AwaitOptions) *fakeAwait {
	fake := clock.NewFake(time.Unix(1_760_000_000, 0))
	options.Clock = fake
	run := &fakeAwait{clock: fake, poll: options.orDefaults().Poll, done: make(chan awaitResult, 1)}
	go func() {
		turn, err := Await(context.Background(), resolve, options)
		run.done <- awaitResult{turn, err}
	}()
	return run
}

// parked waits until Await is parked on its next poll, or has returned.
func (run *fakeAwait) parked(t *testing.T) (awaitResult, bool) {
	t.Helper()
	var result awaitResult
	returned := false
	testwait.Until(t, "Await to park on its next poll or return", func() (bool, string) {
		select {
		case result = <-run.done:
			returned = true
			return true, ""
		default:
		}
		return run.clock.Pending() > 0, run.clock.String()
	})
	return result, returned
}

// step runs between two of Await's looks: write (nil for a quiet poll) runs
// while Await is parked, then the clock moves one Poll. Await returning first
// fails the test.
func (run *fakeAwait) step(t *testing.T, write func()) {
	t.Helper()
	if result, returned := run.parked(t); returned {
		t.Fatalf("Await returned %+v, %v before the test's next step", result.turn, result.err)
	}
	if write != nil {
		write()
	}
	run.clock.Advance(run.poll)
}

// quiet takes polls steps with no write.
func (run *fakeAwait) quiet(t *testing.T, polls int) {
	t.Helper()
	for range polls {
		run.step(t, nil)
	}
}

// result moves the clock poll by poll until Await returns, and gives back
// what it returned.
func (run *fakeAwait) result(t *testing.T) (Turn, error) {
	t.Helper()
	for {
		if result, returned := run.parked(t); returned {
			return result.turn, result.err
		}
		run.clock.Advance(run.poll)
	}
}

// TestAwaitReturnsTheAnswerToTheQuestionJustAsked is the two-way contract: the
// frontier is taken before the message, so nothing said earlier can be
// mistaken for the reply.
func TestAwaitReturnsTheAnswerToTheQuestionJustAsked(t *testing.T) {
	talk := newConversation(t)
	talk.say(user("an older question"), assistant("an older answer"))
	frontier, err := Frontier(talk.chat)
	if err != nil {
		t.Fatal(err)
	}

	options := fastOptions()
	options.Offset = frontier
	run := startAwait(talk.resolve, options)
	run.quiet(t, 4)
	run.step(t, func() { talk.say(user("the new question")) })
	run.quiet(t, 4)
	run.step(t, func() { talk.say(assistant("the new answer")) })
	turn, err := run.result(t)
	if err != nil {
		t.Fatalf("Await() error = %v", err)
	}
	if !turn.Delivered {
		t.Fatal("Delivered = false, want the human turn to count as proof")
	}
	if turn.Answer != "the new answer" {
		t.Fatalf("Answer = %q, want only the answer written after the question", turn.Answer)
	}
	if turn.State != StateIdle {
		t.Fatalf("State = %q, want %q", turn.State, StateIdle)
	}
}

// TestAwaitWaitsThroughToolWork is the difference between a model pausing to
// think and a model that has finished: an assistant line followed by tool work
// is a preamble, not an answer.
func TestAwaitWaitsThroughToolWork(t *testing.T) {
	talk := newConversation(t)
	talk.say(user("do the thing"), assistant("let me look"), tool("Bash"))
	options := fastOptions()
	run := startAwait(talk.resolve, options)
	// Twice the Settle of quiet polls with tool work newest: Await looked at it
	// every time and still holds.
	run.quiet(t, int(2*options.Settle/options.Poll))
	run.step(t, func() { talk.say(assistant("done, it was the firewall")) })
	turn, err := run.result(t)
	if err != nil {
		t.Fatalf("Await() error = %v", err)
	}
	if !strings.Contains(turn.Answer, "done, it was the firewall") {
		t.Fatalf("Answer = %q, want the finished answer", turn.Answer)
	}
	if turn.Tools != 1 {
		t.Fatalf("Tools = %d, want 1", turn.Tools)
	}
}

// TestAwaitTimesOutWithDeliveryIntact separates "you were not heard" from "you
// were heard and the answer is not in yet" — different facts with different
// recoveries.
func TestAwaitTimesOutWithDeliveryIntact(t *testing.T) {
	talk := newConversation(t)
	talk.say(user("a question nobody answers"))

	options := fastOptions()
	options.Timeout = 80 * time.Millisecond
	turn, err := startAwait(talk.resolve, options).result(t)
	if !errors.Is(err, ErrAwaitTimeout) {
		t.Fatalf("Await() error = %v, want ErrAwaitTimeout", err)
	}
	if !turn.Delivered {
		t.Fatal("Delivered = false, want the question to still count as delivered")
	}
	if turn.Answer != "" {
		t.Fatalf("Answer = %q, want none", turn.Answer)
	}
}

func TestAwaitReportsAChatThatDies(t *testing.T) {
	talk := newConversation(t)
	run := startAwait(talk.resolve, fastOptions())
	run.quiet(t, 4)
	run.step(t, talk.vanish)
	_, err := run.result(t)
	if !errors.Is(err, ErrChatGone) {
		t.Fatalf("Await() error = %v, want ErrChatGone", err)
	}
}

// TestAwaitKeepsWhatADyingChatManagedToSay: an answer written before the seat
// went away is a real answer and must not be thrown out with the corpse.
func TestAwaitKeepsWhatADyingChatManagedToSay(t *testing.T) {
	talk := newConversation(t)
	options := fastOptions()
	options.Settle = 5 * time.Second
	run := startAwait(talk.resolve, options)
	run.quiet(t, 2)
	run.step(t, func() { talk.say(user("last words?"), assistant("goodbye")) })
	run.quiet(t, 2)
	run.step(t, talk.die)
	turn, err := run.result(t)
	if !errors.Is(err, ErrChatGone) {
		t.Fatalf("Await() error = %v, want ErrChatGone", err)
	}
	if turn.Answer != "goodbye" {
		t.Fatalf("Answer = %q, want what it said before it died", turn.Answer)
	}
	if turn.State != StateDead {
		t.Fatalf("State = %q, want %q", turn.State, StateDead)
	}
}

// TestAwaitWaitsOutTheGraceForAChatBeingBorn covers `run --await`: the seat is
// created, then indexed, and a lookup in between must not call it gone.
func TestAwaitWaitsOutTheGraceForAChatBeingBorn(t *testing.T) {
	talk := newConversation(t)
	talk.vanish()
	options := fastOptions()
	options.Grace = 5 * time.Second
	run := startAwait(talk.resolve, options)
	run.quiet(t, 6)
	run.step(t, func() {
		talk.say(user("hello"), assistant("hello back"))
		talk.appear()
	})
	turn, err := run.result(t)
	if err != nil {
		t.Fatalf("Await() error = %v", err)
	}
	if turn.Answer != "hello back" {
		t.Fatalf("Answer = %q", turn.Answer)
	}
}

// TestAwaitStopsAtDeliveryWhenAskedTo is the launch proof: `run` needs to know
// the model was ASKED, and must not sit through the whole answer to learn it.
func TestAwaitStopsAtDeliveryWhenAskedTo(t *testing.T) {
	talk := newConversation(t)
	options := fastOptions()
	options.StopOnDelivery = true
	options.Settle = time.Minute
	run := startAwait(talk.resolve, options)
	run.quiet(t, 2)
	run.step(t, func() { talk.say(user("the launch prompt")) })
	turn, err := run.result(t)
	if err != nil {
		t.Fatalf("Await() error = %v", err)
	}
	if !turn.Delivered || turn.Answer != "" {
		t.Fatalf("turn = %+v, want delivery alone", turn)
	}
}

// TestAwaitAnswersTheLatestQuestion: a second human turn arriving mid-wait
// makes everything said before it the answer to something else.
func TestAwaitAnswersTheLatestQuestion(t *testing.T) {
	talk := newConversation(t)
	// "answer to first" is read a poll before the second question lands, one
	// poll of the Settle that could end the wait on it.
	run := startAwait(talk.resolve, fastOptions())
	run.step(t, func() { talk.say(user("first"), assistant("answer to first")) })
	run.step(t, func() { talk.say(user("second")) })
	run.step(t, func() { talk.say(assistant("answer to second")) })
	turn, err := run.result(t)
	if err != nil {
		t.Fatalf("Await() error = %v", err)
	}
	if turn.Answer != "answer to second" {
		t.Fatalf("Answer = %q, want only the answer to the last question", turn.Answer)
	}
}

func TestAwaitStreamsProgress(t *testing.T) {
	talk := newConversation(t)
	var progress bytes.Buffer
	options := fastOptions()
	options.Progress = &progress
	run := startAwait(talk.resolve, options)
	run.step(t, func() { talk.say(user("go"), tool("Read"), assistant("read it")) })
	if _, err := run.result(t); err != nil {
		t.Fatalf("Await() error = %v", err)
	}
	if got, want := progress.String(), "U go\nT Read|{}\nA read it\n"; got != want {
		t.Fatalf("progress = %q, want %q", got, want)
	}
}

// TestAwaitFlagsAnAnswerSomebodyElseMayOwn: two callers can hold the same chat
// at once, and the second question makes the first caller's claim on the
// answer unprovable. Returning the newest answer is the useful behaviour; not
// SAYING so would be the dishonest one.
func TestAwaitFlagsAnAnswerSomebodyElseMayOwn(t *testing.T) {
	talk := newConversation(t)
	run := startAwait(talk.resolve, fastOptions())
	run.step(t, func() { talk.say(user("my question")) })
	run.quiet(t, 4)
	run.step(t, func() { talk.say(user("somebody else's question")) })
	run.quiet(t, 4)
	run.step(t, func() { talk.say(assistant("one answer for both")) })
	turn, err := run.result(t)
	if err != nil {
		t.Fatalf("Await() error = %v", err)
	}
	if !turn.Superseded {
		t.Fatal("Superseded = false after a second human turn landed mid-wait")
	}
	if turn.Answer != "one answer for both" {
		t.Fatalf("Answer = %q", turn.Answer)
	}
}

// TestAwaitReadsOneTranscriptOnceThroughTwoSpellings: the fleet names a fresh
// Claude chat's transcript by its hook crumb's spelling until the index has
// it, then by the index's — and with an account's projects/ symlinked to a
// shared one, those are two strings for one file. Re-reading the file from
// the top on the switch would count the one question twice and call the
// answer somebody else's.
func TestAwaitReadsOneTranscriptOnceThroughTwoSpellings(t *testing.T) {
	talk := newConversation(t)
	linked := filepath.Join(t.TempDir(), "projects")
	if err := os.Symlink(filepath.Dir(talk.path), linked); err != nil {
		t.Fatal(err)
	}
	crumbSpelling := filepath.Join(linked, filepath.Base(talk.path))
	calls := 0
	resolve := func(ctx context.Context) (Chat, bool, error) {
		chat, found, err := talk.resolve(ctx)
		if calls == 0 {
			chat.Path = crumbSpelling
		}
		calls++
		return chat, found, err
	}
	talk.say(user("my question"))
	run := startAwait(resolve, fastOptions())
	run.quiet(t, 8)
	run.step(t, func() { talk.say(assistant("the answer")) })
	turn, err := run.result(t)
	if err != nil {
		t.Fatalf("Await() error = %v", err)
	}
	if calls < 2 {
		t.Fatalf("resolver ran %d times, want the switch to the second spelling seen", calls)
	}
	if turn.Superseded {
		t.Fatal("Superseded = true for one question read through two spellings of one transcript")
	}
	if turn.Answer != "the answer" {
		t.Fatalf("Answer = %q, want %q", turn.Answer, "the answer")
	}
}

// TestAwaitKeepsTheFrontierAcrossARolloutDecompressedToResume: a finished
// Codex chat's rollout is compressed; the frontier is taken there, and Codex
// decompresses it to its plain twin before it writes the turn. The twin holds
// the history at the same offsets, so reading it from the top would count
// every old question as somebody else's.
func TestAwaitKeepsTheFrontierAcrossARolloutDecompressedToResume(t *testing.T) {
	talk := newConversation(t)
	talk.say(user("an old question"), assistant("an old answer"), user("another old question"), assistant("done"))
	compressed, err := codexmeta.CompressRollout(talk.path)
	if err != nil {
		t.Fatal(err)
	}
	asked := talk.chat
	asked.Path = compressed
	frontier, err := Frontier(asked)
	if err != nil {
		t.Fatalf("Frontier(%s) error = %v", compressed, err)
	}
	calls := 0
	resolve := func(ctx context.Context) (Chat, bool, error) {
		chat, found, err := talk.resolve(ctx)
		if calls == 0 {
			chat.Path = compressed
		}
		calls++
		return chat, found, err
	}
	if _, err := codexmeta.DecompressRollout(compressed); err != nil {
		t.Fatal(err)
	}
	talk.say(user("my question"))
	options := fastOptions()
	options.Offset = frontier
	run := startAwait(resolve, options)
	run.quiet(t, 8)
	run.step(t, func() { talk.say(assistant("the answer")) })
	turn, err := run.result(t)
	if err != nil {
		t.Fatalf("Await() error = %v", err)
	}
	if calls < 2 || turn.Superseded || turn.Answer != "the answer" {
		t.Fatalf("Await() after %d resolves = superseded %t, answer %q; want one question answered %q",
			calls, turn.Superseded, turn.Answer, "the answer")
	}
}

// codexAgentsInstructionsLine is the AGENTS.md block a Codex rollout records as a user message.
const codexAgentsInstructionsLine = `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions\n\n<INSTRUCTIONS>\nbe brief\n</INSTRUCTIONS>"}]}}`

func codexSaid(text string) string {
	return `{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"` +
		text + `"}]}}`
}

// A Codex seat writes commentary between the tool calls of one turn, so an
// assistant entry newest and a quiet file past Settle is not an answer: only
// the rollout's task_complete or turn_aborted ends the turn. Claude, whose
// transcript holds no turn records, keeps the newest-entry rule.
func TestAwaitHoldsACodexTurnUntilItsEndRecord(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		engine pfmengine.ID
		turn   []string
		end    string
		answer string
	}{
		{
			name:   "codex task_complete",
			engine: pfmengine.Codex,
			turn: []string{
				codexTurnStartLine, codexUserLine, codexToolLine, codexSaid("checking the next file"),
				codexToolLine, codexSaid("still looking"),
			},
			end:    codexTurnCompleteLine,
			answer: "checking the next file\n\nstill looking",
		},
		{
			name:   "codex turn_aborted",
			engine: pfmengine.Codex,
			turn:   []string{codexTurnStartLine, codexUserLine, codexToolLine, codexSaid("checking the next file")},
			end:    codexAbortLine,
			answer: "checking the next file\n\n[turn aborted: interrupted]",
		},
		{
			name:   "codex without turn records keeps the newest-entry rule",
			engine: pfmengine.Codex,
			turn:   []string{codexUserLine, codexToolLine, codexSaid("answered")},
			answer: "answered",
		},
		{
			name:   "codex harness instructions written as a user message are no human turn",
			engine: pfmengine.Codex,
			turn: []string{
				codexAgentsInstructionsLine, codexTurnStartLine, codexUserLine, codexSaid("answered"),
			},
			end:    codexTurnCompleteLine,
			answer: "answered",
		},
		{
			name:   "claude is unchanged",
			engine: pfmengine.Claude,
			turn:   []string{user("hello"), tool("Read"), assistant("answered")},
			answer: "answered",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			talk := newConversation(t)
			talk.chat.Engine = testCase.engine
			options := fastOptions()
			run := startAwait(talk.resolve, options)
			run.step(t, func() { talk.say(testCase.turn...) })
			var turn Turn
			var err error
			returned := false
			// Eight Settles of quiet polls: an answer returns within them, a
			// turn waiting on its end record outlasts them.
			for range int(8 * options.Settle / options.Poll) {
				var result awaitResult
				if result, returned = run.parked(t); returned {
					turn, err = result.turn, result.err
					break
				}
				run.clock.Advance(run.poll)
			}
			switch {
			case returned && testCase.end != "":
				t.Fatalf("Await() returned %q mid-turn with answer %q, want it waiting for the turn's end record",
					turn.State, turn.Answer)
			case !returned && testCase.end == "":
				t.Fatalf("Await() still waiting after %v of quiet, want the answer", 8*options.Settle)
			case !returned:
				run.step(t, func() { talk.say(testCase.end) })
				turn, err = run.result(t)
			}
			if err != nil {
				t.Errorf("Await() error = %v", err)
			}
			if turn.State != StateIdle || turn.Answer != testCase.answer || !turn.Delivered || turn.Superseded {
				t.Fatalf("turn = %#v, want delivered idle with answer %q, not superseded", turn, testCase.answer)
			}
		})
	}
}

// TestAwaitStopsAtTheFirstReplyAndNamesAnAPIError: the reply watch a launch
// runs after delivery returns on the first thing the chat writes back, and a
// reply that is a refused request reads as an error, never as an answer.
func TestAwaitStopsAtTheFirstReplyAndNamesAnAPIError(t *testing.T) {
	for _, reply := range []struct {
		name, line, wantState, wantError string
	}{
		{name: "api error", line: claudeAPIErrorLine, wantState: StateError, wantError: "server_error"},
		{name: "tool call", line: tool("Bash"), wantState: StateWorking},
		{name: "answer", line: assistant("on it"), wantState: StateWorking},
	} {
		t.Run(reply.name, func(t *testing.T) {
			talk := newConversation(t)
			talk.say(user("the launch prompt"))
			options := fastOptions()
			options.StopOnReply = true
			options.Settle = time.Minute
			run := startAwait(talk.resolve, options)
			run.quiet(t, 2)
			run.step(t, func() { talk.say(reply.line) })
			turn, err := run.result(t)
			if err != nil {
				t.Fatalf("Await() error = %v", err)
			}
			if turn.State != reply.wantState || turn.Error != reply.wantError {
				t.Fatalf("turn = %+v, want state %q error %q", turn, reply.wantState, reply.wantError)
			}
		})
	}
}

// TestAwaitClearsAnErrorARealAnswerFollows: an API error the model recovered
// from is not the turn's verdict — only the newest answer's kind counts.
func TestAwaitClearsAnErrorARealAnswerFollows(t *testing.T) {
	talk := newConversation(t)
	talk.say(user("the question"), claudeAPIErrorLine, assistant("answered after a retry"))
	turn, err := startAwait(talk.resolve, fastOptions()).result(t)
	if err != nil {
		t.Fatalf("Await() error = %v", err)
	}
	if turn.Error != "" {
		t.Fatalf("Error = %q, want none once a real answer followed", turn.Error)
	}
}

// A failed fleet scan during a wait is no news of the chat: the last lookup
// holds and the next poll retries, and only UnreadablePolls failures in a row
// end the wait. The answer lands on the last failing lookup, so a wait that
// gave up early would never read it.
func TestAwaitRidesOutAFailedChatLookup(t *testing.T) {
	scanFailed := errors.New("fleet scan failed")
	for _, testCase := range []struct {
		name          string
		failing       []int // the lookups, counted from 0, that fail
		wantAnswer    string
		wantErrorText string
	}{
		{"before the chat is first found", []int{0, 1}, "the answer", ""},
		{"after the chat was found, in bursts", []int{1, 2, 4, 5}, "the answer", ""},
		{"UnreadablePolls in a row", []int{1, 2, 3}, "", "chat lookup failed 3 times in a row: fleet scan failed"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			talk := newConversation(t)
			talk.say(user("my question"))
			calls := 0
			resolve := func(ctx context.Context) (Chat, bool, error) {
				call := calls
				calls++
				if !slices.Contains(testCase.failing, call) {
					return talk.resolve(ctx)
				}
				if call == testCase.failing[len(testCase.failing)-1] && testCase.wantAnswer != "" {
					talk.say(assistant(testCase.wantAnswer))
				}
				return Chat{}, false, scanFailed
			}
			turn, err := startAwait(resolve, fastOptions()).result(t)
			gotErrorText := ""
			if err != nil {
				gotErrorText = err.Error()
			}
			if gotErrorText != testCase.wantErrorText || (err != nil && !errors.Is(err, scanFailed)) {
				t.Fatalf("Await() error = %v, want %q wrapping the scan failure", err, testCase.wantErrorText)
			}
			if turn.Answer != testCase.wantAnswer {
				t.Fatalf("Answer = %q, want %q", turn.Answer, testCase.wantAnswer)
			}
		})
	}
}

// A Codex rollout whose turn records cannot be read leaves the answer in hand
// to the newest-entry rule, and says why in the log.
func TestACodexTurnWhoseRecordsCannotBeReadIsDecidedByItsNewestEntry(t *testing.T) {
	ctx, recorder := obs.Test(t)
	path := filepath.Join(t.TempDir(), "rollout-gone.jsonl")
	read := codexTurnRead{at: -1}
	if open, readOK := read.open(ctx, path, 0); open || readOK {
		t.Fatalf(
			"open() = (%v, %v) for an unreadable rollout, want (false, false): the newest entry decides, unproven",
			open,
			readOK,
		)
	}
	var warned []string
	for _, record := range recorder.Records() {
		if record.Message == "await: Codex turn records could not be read; the newest entry decides" {
			logged, _ := record.Field("path")
			warned = append(warned, record.Level+" "+logged.(string))
		}
	}
	if want := []string{"WARN " + path}; !slices.Equal(warned, want) {
		t.Fatalf("warnings = %q, want %q", warned, want)
	}
}
