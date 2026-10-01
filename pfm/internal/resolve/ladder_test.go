package resolve

import (
	"context"
	"errors"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type ladderRaw struct {
	outcomes map[Kind]Outcome
	err      error
	calls    []Kind
}

func (fake *ladderRaw) Resolve(_ context.Context, kind Kind, _ string) (Outcome, error) {
	fake.calls = append(fake.calls, kind)
	return fake.outcomes[kind], fake.err
}

type ladderIdentity struct {
	identity Identity
	err      error
}

func (fake ladderIdentity) Identify(context.Context) (Identity, error) {
	return fake.identity, fake.err
}

type ladderSession string

func (session ladderSession) CurrentSession(context.Context, string) (string, error) {
	return string(session), nil
}

func TestLadderOrderAndFailureKinds(t *testing.T) {
	ctx := context.Background()
	for _, testCase := range []struct {
		name       string
		target     string
		ladder     Ladder
		wantCode   int
		wantDetail string
		wantPane   string
		wantErr    bool
	}{
		{name: "empty", target: " ", wantCode: CodeUnknown, wantDetail: "empty target"},
		{
			name: "self", target: "me", wantPane: "%8",
			ladder: Ladder{Self: ladderIdentity{identity: Identity{
				Session: "cc-seat", SocketPath: "/tmp/cc-seat", Pane: "%8", Engine: "claude",
			}}},
		},
		{
			name: "self no seat", target: "self", wantCode: CodeUnknown,
			wantDetail: "self target has no live tmux seat",
			ladder:     Ladder{Self: ladderIdentity{err: ErrNoTmux}},
		},
		{
			name: "self probe broke", target: "self", wantCode: CodeUndelivered, wantErr: true,
			ladder: Ladder{Self: ladderIdentity{err: errors.New("tmux probe failed")}},
		},
		{
			name: "raw pane", target: "%7", wantPane: "%7",
			ladder: Ladder{Env: &paths.MapEnv{Values: map[string]string{
				"CHAT_INJECT_SOCKET": "/tmp/cx-seat",
			}}},
		},
		{
			name: "raw pane no socket", target: "%7", wantCode: CodeUnknown,
			wantDetail: "raw pane target requires TMUX",
		},
		{
			name: "roster ambiguity", target: "same", wantCode: CodeAmbiguous,
			wantDetail: "thread id one, thread id two",
			ladder: Ladder{Roster: RosterFunc(func(context.Context, string, string) (Seat, int, string, error) {
				return Seat{}, CodeAmbiguous, "thread id one, thread id two", nil
			})},
		},
		{
			name: "roster broke", target: "same", wantCode: CodeUndelivered, wantErr: true,
			ladder: Ladder{Roster: RosterFunc(func(context.Context, string, string) (Seat, int, string, error) {
				return Seat{}, CodeUnknown, "", errors.New("scan broke")
			})},
		},
		{
			name: "unknown", target: "ghost", wantCode: CodeUnknown,
			wantDetail: `target "ghost" matched no live chat`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			seat, code, detail, err := testCase.ladder.Resolve(ctx, testCase.target, LadderOptions{})
			if (err != nil) != testCase.wantErr || code != testCase.wantCode || detail != testCase.wantDetail ||
				seat.Pane != testCase.wantPane {
				t.Fatalf("Resolve() = seat=%+v code=%d detail=%q err=%v", seat, code, detail, err)
			}
		})
	}
}

// TestNormalizeTargetStripsOneQuotedLayer pins both readings of the reply hint.
// The CLI form quotes a spaced label to make one argument; an MCP caller may
// pass those quotes inside its JSON string. Both must reach the same chat, while
// quotes genuinely inside a label must remain intact.
func TestNormalizeTargetStripsOneQuotedLayer(t *testing.T) {
	for _, test := range []struct{ in, want string }{
		{`"Delivery Trust"`, "Delivery Trust"},
		{`" Delivery Trust "`, "Delivery Trust"},
		{`Delivery Trust`, "Delivery Trust"},
		{`  "P:DO"  `, "P:DO"},
		{`P:DO`, "P:DO"},
		{`cc-1787705979-3980493-30867`, "cc-1787705979-3980493-30867"},
		{`say "hi" now`, `say "hi" now`},
		{`"`, `"`},
		{``, ``},
	} {
		if got := NormalizeTarget(test.in); got != test.want {
			t.Fatalf("NormalizeTarget(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestSeatFromPartsNamesTheSocketEngine(t *testing.T) {
	for _, test := range []struct {
		socket, pane, probe, want string
	}{
		{"/tmp/tmux-0/ox-1-2-3,99,0", "%5", "", string(pfmengine.OpenCode)},
		{"/tmp/tmux-0/cc-9,1,0", "%1", "", string(pfmengine.Claude)},
		{"/tmp/jail/probe-ox-7,3,0", "%2", "1", string(pfmengine.OpenCode)},
		{"/tmp/tmux-0/unmanaged,1,0", "%2", "", "unknown"},
	} {
		env := &paths.MapEnv{Values: map[string]string{"PFM_TEST_PROBE_SOCKETS": test.probe}}
		if got := SeatFromParts(test.socket, test.pane, env).Engine; got != test.want {
			t.Fatalf(
				"SeatFromParts(%q, %q, probe=%q).Engine = %q, want %q",
				test.socket,
				test.pane,
				test.probe,
				got,
				test.want,
			)
		}
	}
}

func TestLadderRosterMissFallsThroughAndEngineScopeNarrowsRaw(t *testing.T) {
	raw := &ladderRaw{outcomes: map[Kind]Outcome{
		CxWindow: {Code: 0, Stdout: "/tmp/cx-seat\t%9"},
	}}
	ladder := Ladder{
		Roster: RosterFunc(func(_ context.Context, _, requiredEngine string) (Seat, int, string, error) {
			if requiredEngine != string(pfmengine.Codex) {
				t.Fatalf("required engine = %q", requiredEngine)
			}
			return Seat{}, CodeUnknown, "", nil
		}),
		Raw: raw, Session: ladderSession("renamed-session"),
	}
	seat, code, _, err := ladder.Resolve(context.Background(), "fresh", LadderOptions{
		RequiredEngine: string(pfmengine.Codex), Kinds: []Kind{Label},
	})
	if err != nil || code != 0 || seat.Pane != "%9" || seat.Engine != string(pfmengine.Codex) ||
		seat.Session != "renamed-session" {
		t.Fatalf("Resolve() = %+v code=%d err=%v", seat, code, err)
	}
	if got := strings.Trim(strings.Join([]string{string(raw.calls[0])}, ","), ","); got != "cxwin" {
		t.Fatalf("raw calls = %v, want cxwin only", raw.calls)
	}
}

func TestLadderRawPaneSocketScopeAndAmbientFallback(t *testing.T) {
	requestSocket := "/tmp/cx-request"
	ambience := &paths.MapEnv{Values: map[string]string{
		"CHAT_INJECT_SOCKET": "/tmp/cx-inject",
		"TMUX":               "/tmp/cx-tmux,12,0",
	}}

	t.Run("request socket wins without copying caller identity", func(t *testing.T) {
		identity := Identity{
			SocketPath: requestSocket, Pane: "%1", Session: "caller", ID: "thread-a",
		}
		seat, code, detail, err := (Ladder{
			RequestIdentity: &identity,
			Env:             ambience,
		}).Resolve(context.Background(), "%2", LadderOptions{})
		if err != nil || code != 0 || detail != "" || seat.SocketPath != requestSocket || seat.Pane != "%2" {
			t.Fatalf("scoped raw pane = seat=%+v code=%d detail=%q err=%v", seat, code, detail, err)
		}
		if seat.ID != "" || seat.Session != "" {
			t.Fatalf("raw destination inherited request caller identity: %+v", seat)
		}
	})

	t.Run("missing request socket refuses ambient fallback", func(t *testing.T) {
		identity := Identity{Pane: "%1", Session: "caller", ID: "thread-a"}
		seat, code, detail, err := (Ladder{
			RequestIdentity: &identity,
			Env:             ambience,
		}).Resolve(context.Background(), "%2", LadderOptions{})
		if err != nil || code != CodeUnknown || detail != "raw pane target request identity has no socket" ||
			seat != (Seat{}) {
			t.Fatalf("missing scoped socket = seat=%+v code=%d detail=%q err=%v", seat, code, detail, err)
		}
	})

	t.Run("ambient socket precedence remains unchanged", func(t *testing.T) {
		seat, code, detail, err := (Ladder{Env: ambience}).Resolve(
			context.Background(), "%2", LadderOptions{},
		)
		if err != nil || code != 0 || detail != "" || seat.SocketPath != "/tmp/cx-inject" || seat.Pane != "%2" {
			t.Fatalf("ambient raw pane = seat=%+v code=%d detail=%q err=%v", seat, code, detail, err)
		}

		ambience.Values["CHAT_INJECT_SOCKET"] = ""
		seat, code, detail, err = (Ladder{Env: ambience}).Resolve(
			context.Background(), "%3", LadderOptions{},
		)
		if err != nil || code != 0 || detail != "" || seat.SocketPath != "/tmp/cx-tmux" || seat.Pane != "%3" {
			t.Fatalf("ambient TMUX raw pane = seat=%+v code=%d detail=%q err=%v", seat, code, detail, err)
		}
	})
}
