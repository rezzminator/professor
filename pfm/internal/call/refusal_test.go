package call

import "testing"

// TestRefusalsAreWordedOnce pins every refusal both chat doors answer: its status, code and exact message.
func TestRefusalsAreWordedOnce(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		got  Refusal
		want Refusal
	}{
		{"unknown", UnknownChat("n"), Refusal{"not_found", 4, `no chat named "n"`}},
		{"not running", NotRunning("child"), Refusal{"dead", 3, `"child" is not running`}},
		{"dead", DeadChat("dead", "claude", "abc"), Refusal{
			"dead", 3,
			`"dead" is dead (engine claude, session abc); revive it with: pfm chat open dead`,
		}},
		{"resumable", DeadResumable("old"), Refusal{"dead", 3, "\"old\" is dead; `pfm chat call old` from a shell " +
			"appends the message to its transcript, or revive it with: pfm chat open old"}},
		{"not answered", NotAnswered("a"), Refusal{"not_answered", 3, `"a" has not answered yet`}},
		{"no transcript", NoTranscript("a"), Refusal{"not_answered", 3, `"a" has not written a transcript yet`}},
	}
	for _, testCase := range cases {
		if testCase.got != testCase.want || testCase.got.Error() != testCase.want.Message {
			t.Errorf("%s: %#v (Error %q), want %#v", testCase.name, testCase.got, testCase.got.Error(), testCase.want)
		}
	}
}

// TestExitRefusalReadsOnlyARefusalCode pins which exit codes of `pfm chat {verb}` are refusals: 4 and 3 are, the
// codes on either side and 0 are not.
func TestExitRefusalReadsOnlyARefusalCode(t *testing.T) {
	t.Parallel()
	cases := []struct {
		code int
		want Refusal
		ok   bool
	}{
		{0, Refusal{}, false},
		{2, Refusal{}, false},
		{3, Refusal{"dead", 3, "msg"}, true},
		{4, Refusal{"not_found", 4, "msg"}, true},
		{5, Refusal{}, false},
	}
	for _, testCase := range cases {
		got, ok := ExitRefusal(testCase.code, "msg")
		if got != testCase.want || ok != testCase.ok {
			t.Errorf("ExitRefusal(%d) = (%#v, %v), want (%#v, %v)", testCase.code, got, ok, testCase.want, testCase.ok)
		}
	}
}
