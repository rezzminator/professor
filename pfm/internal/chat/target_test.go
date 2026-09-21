package chat

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestMatchPrefersTheLiveSeat covers resolution: a name, an id, a socket, the
// live row winning over its own resume twin, and a genuine collision being
// refused rather than guessed.
func TestMatchPrefersTheLiveSeat(t *testing.T) {
	rows := []compose.Row{
		{Kind: compose.LiveCodex, ID: "019f-live", Name: "worker", Socket: "cx-1-2-3", Path: "/cx/live.jsonl"},
		{Kind: compose.ResumeCodex, ID: "019f-live", Name: "worker", Path: "/cx/live.jsonl"},
		{Kind: compose.ResumeClaude, ID: "b1111111-1111-4111-8111-111111111111", Name: "other"},
		{Kind: compose.ResumeClaude, ID: "c2222222-2222-4222-8222-222222222222", Name: "twin"},
		{Kind: compose.ResumeClaude, ID: "d3333333-3333-4333-8333-333333333333", Name: "twin"},
	}
	for _, testCase := range []struct {
		name    string
		query   string
		wantID  string
		live    bool
		wantErr bool
		missing bool
	}{
		{name: "by name prefers live", query: "worker", wantID: "019f-live", live: true},
		{name: "by socket", query: "cx-1-2-3", wantID: "019f-live", live: true},
		{name: "by id prefix", query: "b1111111", wantID: "b1111111-1111-4111-8111-111111111111"},
		{name: "case folded", query: "OTHER", wantID: "b1111111-1111-4111-8111-111111111111"},
		{name: "ambiguous", query: "twin", wantErr: true},
		{name: "unknown", query: "ghost", missing: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			chat, found, err := Match(rows, testCase.query)
			switch {
			case testCase.wantErr:
				if err == nil {
					t.Fatalf("ambiguous name resolved to %#v", chat)
				}
				if !strings.Contains(err.Error(), "matches 2 chats") {
					t.Fatalf("error = %v", err)
				}
			case testCase.missing:
				if found || err != nil {
					t.Fatalf("unknown name = %#v found=%t err=%v", chat, found, err)
				}
			default:
				if err != nil || !found {
					t.Fatalf("found=%t err=%v", found, err)
				}
				if chat.ID != testCase.wantID || chat.Live != testCase.live {
					t.Fatalf("chat = %#v", chat)
				}
			}
		})
	}
}

// TestMatchAcceptsAFullSocketPathAndTheBareName covers the second resolver's
// half of the same normalisation defect the cosmos graph had. A compose.Row
// carries the BARE socket name tmux is addressed by (-L), while everything the
// fleet RECORDS is a full -S path: inject writes one into the comms ledger,
// spawn writes one for every chat it starts, and `pfm chat resolve` prints
// one. An operator or a script pasting the path it was handed matched nothing,
// and the miss reported as "no chat named …" — an absence, for a chat that was
// right there.
func TestMatchAcceptsAFullSocketPathAndTheBareName(t *testing.T) {
	rows := []compose.Row{
		{Kind: compose.LiveClaude, ID: "p-do-id", Name: "P:DO", Socket: "cc-1787705979-3980493-30867", PaneID: "%0"},
		{Kind: compose.LiveCodex, ID: "other-id", Name: "Other", Socket: "cx-1787757492-3196324-4837", PaneID: "%1"},
	}
	for _, target := range []string{
		"cc-1787705979-3980493-30867",
		"/tmp/tmux-1000/cc-1787705979-3980493-30867",
		"P:DO",
		"p-do-id",
	} {
		chat, found, err := Match(rows, target)
		if err != nil {
			t.Fatalf("Match(%q) error: %v", target, err)
		}
		if !found {
			t.Fatalf("Match(%q) found nothing; the chat is in the roster", target)
		}
		if chat.Name != "P:DO" {
			t.Fatalf("Match(%q) = %q, want P:DO", target, chat.Name)
		}
	}
	if _, found, err := Match(rows, "/tmp/tmux-1000/cc-0-0-0"); found || err != nil {
		t.Fatalf("a path naming no live chat resolved: found=%v err=%v", found, err)
	}
}

// TestFromRowCarriesTheRowAndItsLiveness pins the one shape every verb
// operates on: a booting seat already has a server, so it is live; a
// resumable row has none.
func TestFromRowCarriesTheRowAndItsLiveness(t *testing.T) {
	row := compose.Row{
		Kind: compose.Booting, ID: "id-1", Name: "seat", Path: "/t/id-1.jsonl", CWD: "/work",
		Socket: "cc-1-2-3", SessionName: "cc-1-2-3", PaneID: "%4",
	}
	want := headless.Chat{
		Name: "seat", ID: "id-1", Engine: compose.EngineForKind(compose.Booting),
		Path: "/t/id-1.jsonl", CWD: "/work", Socket: "cc-1-2-3", Session: "cc-1-2-3",
		Pane: "%4", Live: true,
	}
	if got := FromRow(row); got != want {
		t.Fatalf("FromRow() = %#v, want %#v", got, want)
	}
	for _, kind := range []compose.Kind{compose.ResumeClaude, compose.ResumeCodex} {
		if kind.IsAddressable() {
			t.Fatalf("IsAddressable(%v) = true for a resumable row", kind)
		}
	}
}

// TestTargetErrorKeepsAScanFailureDistinctFromAbsence pins the root law at
// the verb layer: a scan that could not look is not "no such chat".
func TestTargetErrorKeepsAScanFailureDistinctFromAbsence(t *testing.T) {
	failure := &TargetError{Name: "x", Err: errors.New("open index: disk I/O error")}
	if errors.Is(failure, ErrUnknownChat) || failure.Error() != "open index: disk I/O error" {
		t.Fatalf("scan failure rendered as %q (unknown=%v)", failure.Error(), errors.Is(failure, ErrUnknownChat))
	}
}

// TestSeatIdentityIsOnlyTheLastRung pins the refusals that keep an INHERITED
// CODEX_THREAD_ID from naming anyone: no thread id, a Claude session of the
// process's own, or a thread the fleet binds to no live socket — each is "no
// seat identity", never a handle nobody can reply to.
func TestSeatIdentityIsOnlyTheLastRung(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	t.Setenv(resolve.ClaudeSessionEnv, "")
	t.Setenv(resolve.CodexThreadEnv, "")
	if identity, found := SeatIdentity(ctx, nil); found {
		t.Fatalf("SeatIdentity() without a thread = %+v", identity)
	}
	t.Setenv(resolve.CodexThreadEnv, "019f-inherited")
	t.Setenv(resolve.ClaudeSessionEnv, "a-claude-session")
	if identity, found := SeatIdentity(ctx, nil); found {
		t.Fatalf("SeatIdentity() under a Claude session = %+v", identity)
	}
	t.Setenv(resolve.ClaudeSessionEnv, "")
	// SeatIdentity folds a scan error into "no seat"; prove the scan ran, so
	// this case is "no live socket hosts the thread", not "could not look".
	if _, err := Rows(ctx, io.Discard, nil); err != nil {
		t.Fatalf("fleet scan failed, so the next case would pass vacuously: %v", err)
	}
	if identity, found := SeatIdentity(ctx, nil); found {
		t.Fatalf("SeatIdentity() for a thread no live socket hosts = %+v", identity)
	}
}

// TestTargetReportsAScanThatCouldNotLook drives the law through a real scan:
// with the fleet's index database unopenable, Target returns a *TargetError
// carrying the failure, not ErrUnknownChat — and a healthy scan that finds
// nothing is the ErrUnknownChat answer.
func TestTargetReportsAScanThatCouldNotLook(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	if _, err := Target(ctx, "ghost", nil); !errors.Is(err, ErrUnknownChat) {
		t.Fatalf("Target(ghost) on a healthy fleet = %v, want ErrUnknownChat", err)
	}
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvDB, filepath.Join(blocker, "index.db"))
	_, err := Target(ctx, "ghost", nil)
	var failure *TargetError
	if !errors.As(err, &failure) || errors.Is(err, ErrUnknownChat) || failure.Name != "ghost" {
		t.Fatalf("Target(ghost) with an unopenable index = %v, want a *TargetError that is not ErrUnknownChat", err)
	}
}
