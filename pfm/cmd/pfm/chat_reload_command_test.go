package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/reload"
	"hostops/pfm/internal/resolve"
)

type reloadTargetTmux struct {
	panes []reload.Pane
}

func (tmux reloadTargetTmux) ListPanes(context.Context, string) ([]reload.Pane, error) {
	return tmux.panes, nil
}
func (reloadTargetTmux) SetRemain(context.Context, string, string, bool) error { return nil }
func (reloadTargetTmux) PaneInMode(context.Context, string, string) (bool, error) {
	return false, nil
}
func (reloadTargetTmux) CancelMode(context.Context, string, string) error { return nil }
func (reloadTargetTmux) Capture(context.Context, string, string) (string, error) {
	return "", nil
}
func (reloadTargetTmux) SendKey(context.Context, string, string, string) error { return nil }
func (reloadTargetTmux) SendLiteral(context.Context, string, string, string) error {
	return nil
}

func (reloadTargetTmux) Respawn(context.Context, string, string, string, string) error {
	return nil
}
func (reloadTargetTmux) Display(context.Context, string, string, string) error { return nil }

func TestReloadUsageIsCanonicalAndSwapIsNotMentioned(t *testing.T) {
	if !strings.Contains(reload.Usage, "usage: pfm chat reload") {
		t.Fatalf("reload usage=%q", reload.Usage)
	}
	if strings.Contains(reload.Usage, "chat swap") {
		t.Fatalf("legacy swap leaked into canonical usage: %q", reload.Usage)
	}
}

func TestReloadAcceptsBareSameAccountRequest(t *testing.T) {
	if err := validateReloadArgs(nil); err != nil {
		t.Fatalf("bare reload rejected: %v", err)
	}
}

// TestReloadPaneArgumentReportsWhatTheCallerTyped is reloadSocketArgument's
// sibling: the scheduler in runChatReloadWithRuntime reads this to decide
// whether it, or the caller, named the pane. A caller-typed --pane must be
// returned verbatim; its absence — or a trailing --pane with nothing after
// it — must report "", never panic on an out-of-range index.
func TestReloadPaneArgumentReportsWhatTheCallerTyped(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
		want string
	}{
		{name: "absent", args: []string{"--sock", "/jail/tmux/cc-1"}, want: ""},
		{name: "present", args: []string{"--sock", "/jail/tmux/cc-1", "--pane", "%3"}, want: "%3"},
		{name: "trailing with no value", args: []string{"--sock", "/jail/tmux/cc-1", "--pane"}, want: ""},
		{name: "no args at all", args: nil, want: ""},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := reloadPaneArgument(testCase.args); got != testCase.want {
				t.Fatalf("reloadPaneArgument(%#v) = %q, want %q", testCase.args, got, testCase.want)
			}
		})
	}
}

// TestReloadTargetWithPaneSelectsItAmongSeveralWithoutWhoami is the fix for
// the detached-worker bug: the scheduler in runChatReloadWithRuntime resolves
// the caller's pane once, while it still has ancestry or $TMUX to walk, and
// hands it to the worker via --pane. The worker must select THAT pane out of
// a multi-pane server directly — never fall through to resolve.NewWhoami,
// which a Setsid-detached, reparented worker cannot answer (no $TMUX, no
// tmux ancestor). TMUX/TMUX_PANE are cleared here so a wrongly-taken Whoami
// fallback would fail loudly instead of quietly picking up the test binary's
// own environment.
func TestReloadTargetWithPaneSelectsItAmongSeveralWithoutWhoami(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	panes := []reload.Pane{
		{ID: "%1", PID: 11, CurrentPath: "/one"},
		{ID: "%2", PID: 22, CurrentPath: "/two"},
		{ID: "%3", PID: 33, CurrentPath: "/three"},
	}
	var stderr bytes.Buffer
	socket, pane, state, code := reloadTarget(
		context.Background(), "/jail/tmux/cc-multi", "%2",
		paths.Values{}, commandRuntime{}, reloadTargetTmux{panes: panes}, &stderr,
	)
	if code != 0 || socket != "/jail/tmux/cc-multi" || pane != "%2" || state.PID != 22 {
		t.Fatalf(
			"reload target=(%q,%q,%+v,%d), want the requested pane among the multi-pane server; stderr=%q",
			socket, pane, state, code, stderr.String(),
		)
	}
}

// TestReloadTargetWithPaneRejectsAPaneThatIsNotLive covers the flip side: a
// --pane the resolved server no longer carries (the seat closed between the
// scheduler's own resolution and the worker running) is a named error, not a
// silent fall-back to "pick something".
func TestReloadTargetWithPaneRejectsAPaneThatIsNotLive(t *testing.T) {
	panes := []reload.Pane{{ID: "%1", PID: 11, CurrentPath: "/one"}}
	var stderr bytes.Buffer
	_, _, _, code := reloadTarget(
		context.Background(), "/jail/tmux/cc-gone", "%9",
		paths.Values{}, commandRuntime{}, reloadTargetTmux{panes: panes}, &stderr,
	)
	if code == 0 || !strings.Contains(stderr.String(), "pane %9 is not live on /jail/tmux/cc-gone") {
		t.Fatalf("reload target with a dead pane code=%d stderr=%q", code, stderr.String())
	}
}

// TestReloadTargetWithSockOnlyKeepsTheSinglePaneRule pins the existing
// contract for the CALLER-facing form of --sock (no --pane, e.g. `pfm chat
// reload --sock X` typed by a human, or the top-level scheduler call before
// it has resolved a pane of its own): a multi-pane server is still ambiguous
// and still refused, exactly as before --pane existed.
func TestReloadTargetWithSockOnlyKeepsTheSinglePaneRule(t *testing.T) {
	panes := []reload.Pane{
		{ID: "%1", PID: 11, CurrentPath: "/one"},
		{ID: "%2", PID: 22, CurrentPath: "/two"},
	}
	var stderr bytes.Buffer
	_, _, _, code := reloadTarget(
		context.Background(), "/jail/tmux/cc-multi", "",
		paths.Values{}, commandRuntime{}, reloadTargetTmux{panes: panes}, &stderr,
	)
	if code == 0 || !strings.Contains(stderr.String(), "multiple panes") {
		t.Fatalf(
			"reload target with --sock only and multiple panes code=%d stderr=%q, want the multi-pane refusal",
			code,
			stderr.String(),
		)
	}

	single := []reload.Pane{{ID: "%7", PID: 77, CurrentPath: "/solo"}}
	stderr.Reset()
	socket, pane, state, code := reloadTarget(
		context.Background(), "/jail/tmux/cc-solo", "",
		paths.Values{}, commandRuntime{}, reloadTargetTmux{panes: single}, &stderr,
	)
	if code != 0 || socket != "/jail/tmux/cc-solo" || pane != "%7" || state.PID != 77 {
		t.Fatalf(
			"reload target with --sock only and one pane=(%q,%q,%+v,%d), want it auto-selected; stderr=%q",
			socket, pane, state, code, stderr.String(),
		)
	}
}

func TestReloadTargetAcceptsRecoveredCodexSeatWithoutAmbientTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	identity := resolve.Identity{
		Session:    "cx-1800000000-1-1",
		SocketPath: "/jail/tmux/cx-1800000000-1-1",
		SocketName: "cx-1800000000-1-1",
		Engine:     "codex",
		ID:         "11111111-1111-4111-8111-111111111111",
		Source:     "codex-thread",
		Recovered:  true,
	}
	panes := []reload.Pane{{ID: "%0", PID: 42, CurrentPath: "/worktree"}}
	var stderr bytes.Buffer

	socket, pane, state, code := reloadTargetFromIdentity(
		context.Background(), identity, reloadTargetTmux{panes: panes}, &stderr,
	)
	if code != 0 || socket != identity.SocketPath || pane != "%0" || state.PID != 42 {
		t.Fatalf(
			"reload target=(%q,%q,%+v,%d), want recovered socket's only pane; stderr=%q",
			socket, pane, state, code, stderr.String(),
		)
	}
}
