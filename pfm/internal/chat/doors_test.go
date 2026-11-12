package chat

import (
	"errors"
	"fmt"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
)

// TestTailDropsTrailingBlankRowsThenKeepsTheLastRows pins the one tail both capture doors cut: a screen's unpainted
// bottom is never counted as a row, and the kept rows end in exactly one newline.
func TestTailDropsTrailingBlankRowsThenKeepsTheLastRows(t *testing.T) {
	t.Parallel()
	screen := "one\n\ntwo\nthree\n   \n\n"
	cases := []struct {
		name, text string
		n          int
		want       string
		cut        bool
	}{
		{"every row", screen, 0, "one\n\ntwo\nthree\n", false},
		{"negative keeps every row", screen, -1, "one\n\ntwo\nthree\n", false},
		{"last two, blank bottom uncounted", screen, 2, "two\nthree\n", true},
		{"exactly every content row", screen, 4, "one\n\ntwo\nthree\n", false},
		{"one past the content rows", screen, 5, "one\n\ntwo\nthree\n", false},
		{"one short of the content rows", screen, 3, "\ntwo\nthree\n", true},
		{"no trailing newline", "a\nb", 1, "b\n", true},
		{"blank screen", "\n  \n\n", 3, "", false},
		{"empty", "", 3, "", false},
	}
	for _, testCase := range cases {
		kept, cut := TailRows(testCase.text, testCase.n)
		if kept != testCase.want || cut != testCase.cut {
			t.Errorf("%s: TailRows(%q, %d) = (%q, %v), want (%q, %v)",
				testCase.name, testCase.text, testCase.n, kept, cut, testCase.want, testCase.cut)
		}
	}
}

// TestDoorRefusalsWordEachFailureOnce pins every failure the doors share to its one refusal: status, code and
// message, or the error itself when the failure is a step that could not run.
func TestDoorRefusalsWordEachFailureOnce(t *testing.T) {
	t.Parallel()
	scanFailed := errors.New("fleet scan failed")
	claude := LastResult{Chat: headless.Chat{Name: "alpha", Engine: pfmengine.Claude}}
	opencode := LastResult{Chat: headless.Chat{Name: "omega", Engine: pfmengine.OpenCode}}
	midTurn := fmt.Errorf("%q: %w", "alpha", ErrTurnInProgress)
	keys := KeysRequest{Keys: []string{"Escape", "Enter"}}
	cases := []struct {
		name string
		got  error
		want error
	}{
		{
			"target unknown", TargetRefusal(&TargetError{Name: "n", Err: ErrUnknownChat}, "n"),
			pfmcall.Refusal{Status: "not_found", Code: 4, Message: `no chat named "n"`},
		},
		{
			"target ambiguous",
			TargetRefusal(&TargetError{Name: "a", Err: &headless.AmbiguousTargetError{Detail: "a1 a2"}}, "a"),
			pfmcall.Refusal{Status: "ambiguous", Code: 2, Message: "a1 a2"},
		},
		{"target scan failed", TargetRefusal(scanFailed, "n"), scanFailed},
		{
			"lookup failed", LookupFailed(scanFailed),
			pfmcall.Refusal{Status: "error", Code: 2, Message: "fleet scan failed"},
		},
		{"resolve found", ResolveRefusal(0, "", "n"), nil},
		{
			"resolve unknown", ResolveRefusal(4, `target "n" matched no live chat`, "n"),
			pfmcall.Refusal{Status: "not_found", Code: 4, Message: `no chat named "n"`},
		},
		{
			"resolve ambiguous", ResolveRefusal(2, "a1 a2", "a"),
			pfmcall.Refusal{Status: "ambiguous", Code: 2, Message: "a1 a2"},
		},
		{
			"last unknown", LastRefusal(&TargetError{Name: "n", Err: ErrUnknownChat}, LastResult{}, "n"),
			pfmcall.Refusal{Status: "not_found", Code: 4, Message: `no chat named "n"`},
		},
		{
			"last no transcript", LastRefusal(ErrNoTranscript, claude, "self"),
			pfmcall.Refusal{Status: "not_answered", Code: 3, Message: `"alpha" has not written a transcript yet`},
		},
		{"last opencode", LastRefusal(ErrNoTranscript, opencode, "omega"), pfmcall.Refusal{
			Status: "unsupported", Code: 1, Message: `reading OpenCode session content is not supported ("omega")`,
		}},
		{
			"last not answered", LastRefusal(ErrNoAnswer, claude, "self"),
			pfmcall.Refusal{Status: "not_answered", Code: 3, Message: `"alpha" has not answered yet`},
		},
		{
			"last mid-turn", LastRefusal(midTurn, claude, "alpha"),
			pfmcall.Refusal{Status: "in_progress", Code: 1, Message: `"alpha": mid-turn: no final answer yet`},
		},
		{"last read failed", LastRefusal(scanFailed, claude, "alpha"), scanFailed},
		{"keys capability", keysRefusal(keysFailure{ErrKeyCapability, errors.New("herdr cannot press F13")}, keys,
			KeysSent{}), pfmcall.Refusal{Status: "refused", Code: 2, Message: "herdr cannot press F13"}},
		{
			"keys send", keysRefusal(keysFailure{ErrKeySend, errors.New("pane gone")}, keys, KeysSent{Count: 1}),
			pfmcall.Refusal{Status: "dead", Code: 3, Message: "Enter: pane gone"},
		},
		{
			"keys capture", keysRefusal(keysFailure{ErrKeyCapture, errors.New("pane gone")}, keys, KeysSent{Count: 2}),
			pfmcall.Refusal{Status: "dead", Code: 3, Message: "capture: pane gone"},
		},
		{"keys landed", keysRefusal(nil, keys, KeysSent{Count: 2}), nil},
		{"keys paused", keysRefusal(scanFailed, keys, KeysSent{Count: 1}), scanFailed},
	}
	for _, testCase := range cases {
		if testCase.got != testCase.want {
			t.Errorf("%s: refusal = %#v, want %#v", testCase.name, testCase.got, testCase.want)
		}
	}
	// A resolve that could not run is no refusal: the error names its code and detail.
	const probeFailed = `resolve target "n" failed with code 6: probe failed`
	if err := ResolveRefusal(6, "probe failed", "n"); err == nil || err.Error() != probeFailed {
		t.Errorf("resolve undelivered = %#v, want the error %q", err, probeFailed)
	} else if _, refused := err.(pfmcall.Refusal); refused {
		t.Errorf("resolve undelivered = %#v, a refusal; want an error", err)
	}
}

// TestStatusCodeIsTheExitBothDoorsAnswer pins a status's code: 0 alive, 3 not alive.
func TestStatusCodeIsTheExitBothDoorsAnswer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		state string
		want  int
	}{
		{"idle", 0}, {"working", 0}, {"blocked", 0}, {headless.StateDead, 3}, {headless.StateMissing, 3},
	}
	for _, testCase := range cases {
		if code := StatusCode(headless.Status{State: testCase.state}); code != testCase.want {
			t.Errorf("StatusCode(%q) = %d, want %d", testCase.state, code, testCase.want)
		}
	}
}
