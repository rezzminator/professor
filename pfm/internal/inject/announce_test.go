package inject

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/resolve"
)

// announcingTmux is the shared fakeTmux plus a Display recorder — a wrapper
// here, not a field on the shared fake, so every other fixture keeps
// modelling a tmux that cannot announce and the "no Display → no notice, no
// error" path stays measured by them.
type announcingTmux struct {
	*fakeTmux
	displays []string
	err      error
}

func (fake *announcingTmux) Display(_ context.Context, _, _, text string) error {
	if fake.err != nil {
		return fake.err
	}
	fake.displays = append(fake.displays, text)
	return nil
}

const (
	waitingForSelf  = "one idle sample, then this turn's compaction receipt or a stable idle"
	waitingForOther = "the current turn to end"
)

// TestScheduleAnnouncesTheArmedCompactOnThePane is Wave 8 item 5's pane
// notice (the reload-hold pattern, reload.go announcePane): the operator
// looking at the pane sees WHAT the waiter is waiting for, in the waiter's
// own words, the moment it is armed.
func TestScheduleAnnouncesTheArmedCompactOnThePane(t *testing.T) {
	fake := &fakeTmux{capture: captureIdle}
	engine := newTestEngineWith(t, "cc-announce", fake, &fakeSpawner{})
	announcer := &announcingTmux{fakeTmux: fake}
	engine.tmux = announcer

	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), Request{
		Target:  "chat",
		Message: "/compact hold the wave state",
		Then:    []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 {
		t.Fatalf("schedule = %+v, want scheduled", result)
	}
	want := "compact armed — waiting for: " + waitingForOther
	if len(announcer.displays) != 1 || announcer.displays[0] != want {
		t.Fatalf("pane notices = %q, want exactly [%q]", announcer.displays, want)
	}
}

// TestScheduleSelfCompactNoticeNamesTheSelfContract: the self waiter's
// contract differs (it must see the caller's own turn end first), and the
// notice says so with the SAME words the waiter's own start log line uses.
func TestScheduleSelfCompactNoticeNamesTheSelfContract(t *testing.T) {
	fake := &fakeTmux{capture: captureIdle}
	engine := newTestEngineWith(t, "cc-announce-self", fake, &fakeSpawner{})
	engine.whoami = fakeSelf{identity: resolve.Identity{
		Session:    "cc-announce-self",
		SocketPath: filepath.Join(string(filepath.Separator), "tmp", "tmux-jail", "cc-announce-self"),
		Pane:       "%1",
		Engine:     "claude",
		Source:     "test",
	}}
	announcer := &announcingTmux{fakeTmux: fake}
	engine.tmux = announcer

	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), Request{
		Target:  "self",
		Message: "/compact hold the wave state",
		Then:    []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 {
		t.Fatalf("schedule = %+v, want scheduled", result)
	}
	want := "compact armed — waiting for: " + waitingForSelf
	if len(announcer.displays) != 1 || announcer.displays[0] != want {
		t.Fatalf("pane notices = %q, want exactly [%q]", announcer.displays, want)
	}
}

// TestScheduleNoticeNamesACodexTarget: on a Codex pane the waiter's contract
// is different again (no steady-idle fallback until the Codex footer is
// pinned), and the notice must say which engine it is waiting on.
func TestScheduleNoticeNamesACodexTarget(t *testing.T) {
	fake := &fakeTmux{capture: "conversation\n› "}
	engine := newTestEngineWith(t, "cx-announce", fake, &fakeSpawner{})
	announcer := &announcingTmux{fakeTmux: fake}
	engine.tmux = announcer

	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), Request{
		Target:  "chat",
		Message: "/compact",
		Then:    []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 {
		t.Fatalf("schedule = %+v, want scheduled", result)
	}
	if len(announcer.displays) != 1 || !strings.Contains(announcer.displays[0], "engine codex") {
		t.Fatalf("pane notices = %q, want one notice naming engine codex", announcer.displays)
	}
}

// TestScheduleWithoutAPaneAnnouncerSchedulesSilently: a Tmux that cannot
// Display is not an error and types nothing — the notice is a courtesy, the
// arming is the contract.
func TestScheduleWithoutAPaneAnnouncerSchedulesSilently(t *testing.T) {
	fake := &fakeTmux{capture: captureIdle}
	engine := newTestEngineWith(t, "cc-announce-none", fake, &fakeSpawner{})
	var warnings bytes.Buffer
	engine.warningWriter = &warnings

	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), Request{
		Target:  "chat",
		Message: "/compact hold the wave state",
		Then:    []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 {
		t.Fatalf("schedule = %+v, want scheduled", result)
	}
	if len(fake.literals) != 0 || len(fake.keys) != 0 {
		t.Fatalf("a notice was TYPED into the pane: literals=%q keys=%q", fake.literals, fake.keys)
	}
	if warnings.Len() != 0 {
		t.Fatalf("a tmux without Display produced a warning: %q", warnings.String())
	}
}

// TestScheduleDisplayFailureWarnsAndStillSchedules: a failed notice is a
// warning on the log, never a refusal — the waiter is already armed.
func TestScheduleDisplayFailureWarnsAndStillSchedules(t *testing.T) {
	fake := &fakeTmux{capture: captureIdle}
	engine := newTestEngineWith(t, "cc-announce-err", fake, &fakeSpawner{})
	announcer := &announcingTmux{fakeTmux: fake, err: errors.New("no client attached")}
	engine.tmux = announcer
	var warnings bytes.Buffer
	engine.warningWriter = &warnings

	result, err := engine.ScheduleAfterCurrentTurn(context.Background(), Request{
		Target:  "chat",
		Message: "/compact hold the wave state",
		Then:    []string{"resume the wave"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Code != 0 || result.Status != "scheduled" {
		t.Fatalf("schedule = %+v, want scheduled despite the failed notice", result)
	}
	if !strings.Contains(warnings.String(), "no client attached") {
		t.Fatalf("Display failure was not reported on the warning stream: %q", warnings.String())
	}
}
