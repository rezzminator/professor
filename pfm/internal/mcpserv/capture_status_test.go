package mcpserv

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/chat"
)

// captureCodeCaller answers the call engine's resolve and capture with one pinned code and detail, for a tool
// whose request is refused before any chat verb runs.
type captureCodeCaller struct {
	code   int
	detail string
}

func (fake captureCodeCaller) Resolve(context.Context, string) (pfmcall.Target, int, string, error) {
	return pfmcall.Target{SocketPath: "cc-fixture", Pane: "%0"}, fake.code, fake.detail, nil
}

func (fake captureCodeCaller) ResolveEngine(
	context.Context, string, string,
) (pfmcall.Target, int, string, error) {
	return pfmcall.Target{SocketPath: "cc-fixture", Pane: "%0"}, fake.code, fake.detail, nil
}

func (fake captureCodeCaller) Capture(
	context.Context, string, int,
) (pfmcall.Target, string, int, string, error) {
	return pfmcall.Target{SocketPath: "cc-fixture", Pane: "%0"}, "", fake.code, fake.detail, nil
}

func (captureCodeCaller) Call(context.Context, pfmcall.Request) (pfmcall.Result, error) {
	return pfmcall.Result{}, nil
}

// TestChatCaptureAndKeysAnswerTheChatVerbsRefusalsAsReceipts pins this door's half of capture and keys: every
// refusal the chat verb answers (the one `pfm chat capture` and `pfm chat keys` print) comes back as a receipt of
// its exact status, code and message, never a tool error; an ok capture keeps the verb's tail and its truncation,
// cut further only by max_bytes.
func TestChatCaptureAndKeysAnswerTheChatVerbsRefusalsAsReceipts(t *testing.T) {
	t.Parallel()
	seat := chat.CallTarget{Backend: "tmux", SocketPath: "/sock/cc-a", Pane: "%3"}
	maxBytes := 4
	cases := []struct {
		name     string
		verbs    *fakeChatVerbs
		capture  *CaptureInput
		keys     *KeysInput
		want     any
		wantTail int
	}{
		{
			name: "capture unknown", verbs: &fakeChatVerbs{err: pfmcall.UnknownChat("gone")},
			capture: &CaptureInput{Target: "gone"}, wantTail: 40,
			want: CaptureOutput{Status: "not_found", Code: 4, Message: `no chat named "gone"`},
		},
		{
			name: "capture not running", verbs: &fakeChatVerbs{err: pfmcall.NotRunning("child")},
			capture: &CaptureInput{Target: "child", TailLines: captureInt(3)}, wantTail: 3,
			want: CaptureOutput{Status: "dead", Code: 3, Message: `"child" is not running`},
		},
		{
			name:    "capture ambiguous",
			verbs:   &fakeChatVerbs{err: pfmcall.Refusal{Status: "ambiguous", Code: 2, Message: "a1 a2"}},
			capture: &CaptureInput{Target: "a"}, wantTail: 40,
			want: CaptureOutput{Status: "ambiguous", Code: 2, Message: "a1 a2"},
		},
		{
			name: "capture unreadable seat",
			verbs: &fakeChatVerbs{
				captured: chat.Captured{Seat: seat},
				err:      pfmcall.Refusal{Status: "dead", Code: 3, Message: "pane gone"},
			},
			capture: &CaptureInput{Target: "a"}, wantTail: 40,
			want: CaptureOutput{Status: "dead", Code: 3, SocketPath: "/sock/cc-a", Pane: "%3", Message: "pane gone"},
		},
		{
			name:    "capture tail kept",
			verbs:   &fakeChatVerbs{captured: chat.Captured{Seat: seat, Text: "b\nc\n", Truncated: true}},
			capture: &CaptureInput{Target: "a", TailLines: captureInt(2)}, wantTail: 2,
			want: CaptureOutput{
				Status: "ok", SocketPath: "/sock/cc-a", Pane: "%3", Text: "b\nc\n", Bytes: 4, Truncated: true,
			},
		},
		{
			name:    "capture at the byte bound",
			verbs:   &fakeChatVerbs{captured: chat.Captured{Seat: seat, Text: "b\nc\n"}},
			capture: &CaptureInput{Target: "a", MaxBytes: &maxBytes}, wantTail: 40,
			want: CaptureOutput{Status: "ok", SocketPath: "/sock/cc-a", Pane: "%3", Text: "b\nc\n", Bytes: 4},
		},
		{
			name:    "capture one byte past the bound",
			verbs:   &fakeChatVerbs{captured: chat.Captured{Seat: seat, Text: "ab\nc\n"}},
			capture: &CaptureInput{Target: "a", MaxBytes: &maxBytes}, wantTail: 40,
			want: CaptureOutput{
				Status: "ok", SocketPath: "/sock/cc-a", Pane: "%3", Text: "b\nc\n", Bytes: 4, Truncated: true,
			},
		},
		{
			name: "keys unknown", verbs: &fakeChatVerbs{err: pfmcall.UnknownChat("gone")},
			keys: &KeysInput{Target: "gone", Keys: []string{"Escape"}},
			want: KeysOutput{Status: "not_found", Code: 4, Message: `no chat named "gone"`, Keys: []string{"Escape"}},
		},
		{
			name: "keys pane vanished mid-sequence",
			verbs: &fakeChatVerbs{
				keyed: chat.Keyed{Seat: seat, Sent: chat.KeysSent{Count: 1}},
				err:   pfmcall.Refusal{Status: "dead", Code: 3, Message: "Enter: pane gone"},
			},
			keys: &KeysInput{Target: "a", Keys: []string{"Escape", "Enter"}},
			want: KeysOutput{
				Status: "dead", Code: 3, Message: "Enter: pane gone", SocketPath: "/sock/cc-a", Pane: "%3",
				Count: 1, Keys: []string{"Escape", "Enter"},
			},
		},
		{
			name: "keys refused by the host",
			verbs: &fakeChatVerbs{
				keyed: chat.Keyed{Seat: seat},
				err:   pfmcall.Refusal{Status: "refused", Code: 2, Message: "Herdr cannot press Home"},
			},
			keys: &KeysInput{Target: "a", Keys: []string{"Home"}},
			want: KeysOutput{
				Status: "refused", Code: 2, Message: "Herdr cannot press Home", SocketPath: "/sock/cc-a", Pane: "%3",
				Keys: []string{"Home"},
			},
		},
		{
			name:  "keys landed",
			verbs: &fakeChatVerbs{keyed: chat.Keyed{Seat: seat, Sent: chat.KeysSent{Count: 1, Screen: "screen\n"}}},
			keys:  &KeysInput{Target: "a", Keys: []string{"Enter"}, Capture: true},
			want: KeysOutput{
				Status: "ok", SocketPath: "/sock/cc-a", Pane: "%3", Count: 1, Keys: []string{"Enter"}, Text: "screen\n",
			},
		},
	}
	for _, testCase := range cases {
		service := newService("test", &backend{chat: testCase.verbs, allowAmbientIdentity: true})
		var got any
		var err error
		if testCase.capture != nil {
			_, got, err = service.chatCapture(context.Background(), nil, *testCase.capture)
		} else {
			_, got, err = service.chatKeys(context.Background(), nil, *testCase.keys)
		}
		if err != nil || !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("%s: %+v, %v; want %+v, nil", testCase.name, got, err, testCase.want)
		}
		if testCase.capture != nil && !reflect.DeepEqual(testCase.verbs.captureTails, []int{testCase.wantTail}) {
			t.Errorf("%s: capture tails = %v, want [%d]", testCase.name, testCase.verbs.captureTails, testCase.wantTail)
		}
	}
}

// TestCLIRefusalIsAReceiptInTheVerbsWords pins the argv actions (kill, unkill, name): an exit code that is a
// refusal answers a receipt carrying the CLI's own message without its prefix; any other failure stays a tool
// error.
func TestCLIRefusalIsAReceiptInTheVerbsWords(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, stderr string
		code         int
		want         ActionOutput
		wantErr      string
	}{
		{"unknown", "pfm chat: no chat named \"n\"\n", 4, ActionOutput{
			Status: "not_found", Code: 4,
			Message: `no chat named "n"`,
		}, ""},
		{"dead", "warning\npfm chat kill: \"c\" is not running: try again\n", 3, ActionOutput{
			Status: "dead", Code: 3,
			Message: `"c" is not running: try again`,
		}, ""},
		{"unprefixed", "boom\n", 3, ActionOutput{Status: "dead", Code: 3, Message: "boom"}, ""},
		{"undelivered", "pfm chat kill: stuck\n", 5, ActionOutput{
			Status: "error", Code: 5,
			Message: "pfm chat kill: stuck",
		}, "pfm chat kill n exited 5: pfm chat kill: stuck"},
	}
	for _, testCase := range cases {
		service := newService("test", &backend{dispatch: func(_ context.Context, _ []string, _, stderr io.Writer) int {
			_, _ = io.WriteString(stderr, testCase.stderr)
			return testCase.code
		}})
		_, got, err := service.cliAction(context.Background(), "chat", "kill", "n")
		gotErr := ""
		if err != nil {
			gotErr = err.Error()
		}
		if got != testCase.want || !strings.HasPrefix(gotErr, testCase.wantErr) ||
			(testCase.wantErr == "") != (err == nil) {
			t.Errorf("%s: %+v, %q; want %+v, %q", testCase.name, got, gotErr, testCase.want, testCase.wantErr)
		}
	}
}
