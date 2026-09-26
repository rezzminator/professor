package reap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/agentrole"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type liveDuringCorpseReprobe struct{}

func (liveDuringCorpseReprobe) ListPanes(context.Context, string) ([]gather.ProbePane, error) {
	return []gather.ProbePane{{PaneID: "%1"}}, nil
}

func (liveDuringCorpseReprobe) Sessions(context.Context, string) ([]VSCTSession, error) {
	return nil, nil
}

func (liveDuringCorpseReprobe) KillSession(context.Context, string, string) error {
	return errors.New("unexpected kill-session")
}

func (liveDuringCorpseReprobe) ClientIdle(context.Context, string) (time.Duration, bool, error) {
	return 0, false, nil
}

type unreadableDuringCorpseReprobe struct{}

func (unreadableDuringCorpseReprobe) ListPanes(context.Context, string) ([]gather.ProbePane, error) {
	return nil, errors.New("fixture permission denied")
}

func (unreadableDuringCorpseReprobe) Sessions(context.Context, string) ([]VSCTSession, error) {
	return nil, nil
}

func (unreadableDuringCorpseReprobe) KillSession(context.Context, string, string) error {
	return errors.New("unexpected kill-session")
}

func (unreadableDuringCorpseReprobe) ClientIdle(context.Context, string) (time.Duration, bool, error) {
	return 0, false, nil
}

func TestApplyRechecksAPlannedCorpseBeforeRemovingItsSocket(t *testing.T) {
	tmuxDir := t.TempDir()
	socket := "probe-900-1-1"
	path := filepath.Join(tmuxDir, socket)
	if err := os.WriteFile(path, []byte("socket fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{
		paths: paths.Values{TmuxDir: tmuxDir},
		tmux:  liveDuringCorpseReprobe{},
	}
	decisions, _ := runner.apply(context.Background(), []Decision{{
		Socket: socket,
		State:  StateDead,
		Action: ActionRemoveSocketFile,
	}})
	if len(decisions) != 1 || decisions[0].State != StateSkip || decisions[0].Action != ActionNone {
		t.Fatalf("corpse apply decisions = %#v, want one skipped decision", decisions)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("apply removed a socket that became live: %v", err)
	}
}

func TestApplyPreservesAPlannedCorpseWhenReprobeIsUnreadable(t *testing.T) {
	tmuxDir := t.TempDir()
	socket := "probe-901-1-1"
	path := filepath.Join(tmuxDir, socket)
	if err := os.WriteFile(path, []byte("socket fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{
		paths: paths.Values{TmuxDir: tmuxDir},
		tmux:  unreadableDuringCorpseReprobe{},
	}
	decisions, _ := runner.apply(context.Background(), []Decision{{
		Socket: socket,
		State:  StateDead,
		Action: ActionRemoveSocketFile,
	}})
	if len(decisions) != 1 || decisions[0].State != StateSkip || decisions[0].Action != ActionNone {
		t.Fatalf("unreadable corpse apply decisions = %#v, want one skipped decision", decisions)
	}
	// A re-probe that could not run is a genuine failed attempt, not a safety
	// exemption working as designed — the exit code must be able to see it.
	if !decisions[0].Failed {
		t.Fatalf("an unreadable re-probe did not mark the decision Failed: %#v", decisions[0])
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("apply removed a socket it could not re-probe: %v", err)
	}
}

// goneDuringCorpseReprobe answers every re-probe with ErrServerGone — the
// ordinary shape of a real corpse, letting apply proceed to the actual
// filesystem removal rather than stopping at the re-probe gate.
type goneDuringCorpseReprobe struct{}

func (goneDuringCorpseReprobe) ListPanes(context.Context, string) ([]gather.ProbePane, error) {
	return nil, gather.ErrServerGone
}

func (goneDuringCorpseReprobe) Sessions(context.Context, string) ([]VSCTSession, error) {
	return nil, nil
}

func (goneDuringCorpseReprobe) KillSession(context.Context, string, string) error {
	return errors.New("unexpected kill-session")
}

func (goneDuringCorpseReprobe) ClientIdle(context.Context, string) (time.Duration, bool, error) {
	return 0, false, nil
}

// Role-prompt cleanup is wired into both paths that end a socket.
func TestApplyRemovesTheRolePromptOnADeadSocketRemoval(t *testing.T) {
	const socket = "cc-905-1-1"
	const seat = "builder"
	tmuxDir := t.TempDir()
	path := filepath.Join(tmuxDir, socket)
	if err := os.WriteFile(path, []byte("corpse fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	sidDir := t.TempDir()
	prompt := mustReapSeatPromptPath(t, sidDir, socket)
	if err := agentrole.WriteSeatPrompt(sidDir, socket, "", "<!-- pfm agent-role: worker -->\nROLE"); err != nil {
		t.Fatal(err)
	}
	living := mustReapSeatPromptPath(t, sidDir, "cc-906-1-1")
	if err := agentrole.WriteSeatPrompt(sidDir, "cc-906-1-1", "", "<!-- pfm agent-role: worker -->\nROLE"); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{
		paths: paths.Values{TmuxDir: tmuxDir, SIDDir: sidDir},
		tmux:  goneDuringCorpseReprobe{},
	}
	decisions, warnings := runner.apply(context.Background(), []Decision{{
		Socket: socket,
		Label:  seat,
		State:  StateDead,
		Action: ActionRemoveSocketFile,
	}})
	if len(warnings) != 0 {
		t.Fatalf("clean role-prompt removal produced warnings: %v", warnings)
	}
	if len(decisions) != 1 || decisions[0].Failed {
		t.Fatalf("dead-socket removal decision = %#v, want a clean, non-failed removal", decisions)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corpse socket file survived its own removal: %v", err)
	}
	if _, err := os.Stat(prompt); !os.IsNotExist(err) {
		t.Fatalf("role prompt survived a dead-socket removal: %v", err)
	}
	if _, err := os.Stat(living); err != nil {
		t.Fatalf("living seat's role prompt was removed: %v", err)
	}
}

// A sweep that could not do what it said must not report success: a removal
// that genuinely fails has to mark the decision Failed, or the caller's exit
// code reads clean over a corpse it never actually cleared. The failure here
// is a non-empty directory sitting where the corpse socket file should be —
// os.Remove on a non-empty directory returns ENOTEMPTY for every uid, root
// included, unlike a permission-bit trick (chmod 0500), which root ignores
// outright: that shape passed on the host and silently no-op'd inside this
// repo's own fenced gate, which runs the suite as root.
func TestApplyMarksADecisionFailedWhenTheCorpseSocketCannotBeRemoved(t *testing.T) {
	const socket = "cc-906-1-1"
	tmuxDir := t.TempDir()
	path := filepath.Join(tmuxDir, socket)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "occupant"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{
		paths: paths.Values{TmuxDir: tmuxDir},
		tmux:  goneDuringCorpseReprobe{},
	}
	decisions, warnings := runner.apply(context.Background(), []Decision{{
		Socket: socket,
		State:  StateDead,
		Action: ActionRemoveSocketFile,
	}})
	if len(decisions) != 1 {
		t.Fatalf("apply returned %d decisions, want one", len(decisions))
	}
	if !decisions[0].Failed {
		t.Fatalf("a genuine removal failure did not mark the decision Failed: %#v", decisions[0])
	}
	if decisions[0].State == StateDead {
		t.Fatalf("a failed removal was still reported as the original dead state: %#v", decisions[0])
	}
	if len(warnings) != 0 {
		t.Fatalf("a Failed decision must not ALSO be reported as a mere warning: %v", warnings)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the corpse that could not be removed vanished anyway: %v", err)
	}
}

// A reap that reports success having failed to kill is the exact failure
// mode this command exists to prevent — apply must mark the decision Failed
// so the caller's exit code can never read clean.
func TestApplyMarksADecisionFailedWhenKillServerErrors(t *testing.T) {
	const socket = "cc-902-1-1"
	runner := &Runner{
		paths: paths.Values{TmuxDir: t.TempDir()},
		tmux:  liveDuringCorpseReprobe{},
		killServer: func(context.Context, string) error {
			return errors.New("tmux kill-server: connection refused")
		},
	}
	decisions, _ := runner.apply(context.Background(), []Decision{{
		Socket: socket,
		State:  StateOrphan,
		Action: ActionKillServer,
	}})
	if len(decisions) != 1 {
		t.Fatalf("apply returned %d decisions, want one", len(decisions))
	}
	if !decisions[0].Failed {
		t.Fatalf("a kill-server error did not mark the decision Failed: %#v", decisions[0])
	}
	if decisions[0].State == StateKilled {
		t.Fatalf("a failed kill was still reported as killed: %#v", decisions[0])
	}
}

func TestRemoveRoleSeatPromptIsBestEffortWhenMissing(t *testing.T) {
	if err := removeRoleSeatPrompt(t.TempDir(), "missing"); err != nil {
		t.Fatalf("removeRoleSeatPrompt on a missing prompt returned an error: %v", err)
	}
}

func TestRemoveRoleSeatPromptRemovesAnExistingPrompt(t *testing.T) {
	sidDir := t.TempDir()
	const socket = "cc-builder"
	path := mustReapSeatPromptPath(t, sidDir, socket)
	if err := agentrole.WriteSeatPrompt(sidDir, socket, "", "<!-- pfm agent-role: worker -->\nROLE"); err != nil {
		t.Fatal(err)
	}
	if err := removeRoleSeatPrompt(sidDir, socket); err != nil {
		t.Fatalf("removeRoleSeatPrompt: %v", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("role prompt survived removal: %v", statErr)
	}
}

func TestApplyRemovesTheRolePromptOnAResolvedKill(t *testing.T) {
	const socket = "cc-903-1-1"
	const seat = "builder"
	sidDir := t.TempDir()
	prompt := mustReapSeatPromptPath(t, sidDir, socket)
	if err := agentrole.WriteSeatPrompt(sidDir, socket, "", "<!-- pfm agent-role: worker -->\nROLE"); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{
		paths: paths.Values{TmuxDir: t.TempDir(), SIDDir: sidDir},
		tmux:  liveDuringCorpseReprobe{},
		killServer: func(context.Context, string) error {
			return nil
		},
	}
	decisions, warnings := runner.apply(context.Background(), []Decision{{
		Socket: socket,
		Label:  seat,
		State:  StateIdle,
		Action: ActionKillServer,
	}})
	if len(warnings) != 0 {
		t.Fatalf("clean role-prompt removal produced warnings: %v", warnings)
	}
	if len(decisions) != 1 || decisions[0].State != StateKilled || decisions[0].Failed {
		t.Fatalf("kill decision = %#v, want a clean StateKilled", decisions)
	}
	if _, err := os.Stat(prompt); !os.IsNotExist(err) {
		t.Fatalf("role prompt survived a resolved kill: %v", err)
	}
}

// A role-prompt removal failure is a warning, never a reap failure: it holds
// up nothing this sweep's own exit code answers for, and it must never be
// swallowed silently either.
func TestApplyWarnsWithoutFailingWhenTheRolePromptCannotBeRemoved(t *testing.T) {
	const socket = "cc-904-1-1"
	const seat = "builder"
	sidDir := t.TempDir()
	prompt := mustReapSeatPromptPath(t, sidDir, socket)
	// A non-empty directory in the prompt's place makes os.Remove fail with
	// ENOTEMPTY for every uid, root included. A read-only parent directory
	// is not that: root ignores permission bits entirely, so that trick
	// proved "unremovable" only on a machine that happened not to be root —
	// exactly the coincidence this repo's own fenced gate exists to catch
	// (it runs the suite as root in a container).
	if err := os.Mkdir(prompt, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prompt, "occupant"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := &Runner{
		paths: paths.Values{TmuxDir: t.TempDir(), SIDDir: sidDir},
		tmux:  liveDuringCorpseReprobe{},
		killServer: func(context.Context, string) error {
			return nil
		},
	}
	decisions, warnings := runner.apply(context.Background(), []Decision{{
		Socket: socket,
		Label:  seat,
		State:  StateIdle,
		Action: ActionKillServer,
	}})
	if len(decisions) != 1 || decisions[0].State != StateKilled || decisions[0].Failed {
		t.Fatalf(
			"an unremovable role prompt must not fail the reap itself: %#v",
			decisions,
		)
	}
	if len(warnings) != 1 {
		t.Fatalf("an unremovable role prompt produced %d warnings, want 1: %v", len(warnings), warnings)
	}
	if _, err := os.Stat(prompt); err != nil {
		t.Fatalf("the prompt directory that could not be removed vanished anyway: %v", err)
	}
}

func mustReapSeatPromptPath(t *testing.T, sidDir, socket string) string {
	t.Helper()
	path, err := agentrole.SeatPromptPath(sidDir, socket, "")
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestReapSocketSelectionDelegatesCanonicalClassifier(t *testing.T) {
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "")
	for _, testCase := range []struct {
		name string
		want bool
	}{
		{name: "cx-fixture", want: true},
		{name: "ox-fixture", want: true},
		{name: "vsct-fixture", want: false},
		{name: "mystery-fixture", want: false},
		{name: "probe-fixture", want: false},
	} {
		if got := isReapSocketName(testCase.name); got != testCase.want {
			t.Fatalf("isReapSocketName(%q) = %t, want %t", testCase.name, got, testCase.want)
		}
	}
	t.Setenv("PFM_TEST_PROBE_SOCKETS", "1")
	if !isReapSocketName("probe-fixture") {
		t.Fatal("probe-* socket was not admitted by the explicit jail opt-in")
	}
}

// The reaper's whole contract is a table: one socket, one verdict, and an
// action ONLY where killing is both asked for and safe. Every rule that keeps
// a chat alive is here, because each of them was paid for by a chat somebody
// lost.
func TestPlanClassifiesEverySocket(t *testing.T) {
	const busyUUID = "11111111-1111-4111-8111-111111111111"
	live := Socket{Name: "cc-100-1-1", HasServer: true, HasCrumb: true}

	cases := []struct {
		name   string
		input  Input
		want   State
		action Action
	}{
		{
			name: "an attached chat active within the operator window is never a candidate",
			input: Input{
				AgentsOK:     true,
				Apply:        true,
				ClientActive: time.Hour,
				Sockets: []Socket{{
					Name: "cc-100-1-1", HasServer: true, Attached: true, HasCrumb: true,
					ClientIdleOK: true,
				}},
			},
			want:   StateKeep,
			action: ActionNone,
		},
		{
			name: "the caller's own socket is never a candidate",
			input: Input{
				AgentsOK: true,
				Apply:    true,
				Self:     "cc-100-1-1",
				Sockets:  []Socket{live},
			},
			want:   StateSelf,
			action: ActionNone,
		},
		{
			name: "a detached teammate is headless by design",
			input: Input{
				AgentsOK: true,
				Apply:    true,
				Sockets:  []Socket{{Name: "cc-new-worker", HasServer: true}},
			},
			want:   StateMate,
			action: ActionNone,
		},
		{
			name: "a marked detached fork the engine calls busy survives a sweep",
			input: Input{
				AgentsOK: true,
				Apply:    true,
				BusyIDs:  map[string]struct{}{busyUUID: {}},
				Sockets: []Socket{{
					Name:         "cc-100-1-1",
					HasServer:    true,
					HasCrumb:     true,
					DetachedFork: true,
					CrumbUUIDs:   []string{busyUUID},
				}},
			},
			want:   StateBusy,
			action: ActionNone,
		},
		{
			name: "a transcript written moments ago outranks a stale busy snapshot",
			input: Input{
				AgentsOK:   true,
				Apply:      true,
				BusyRecent: time.Minute,
				RecentIDs:  map[string]struct{}{busyUUID: {}},
				Sockets: []Socket{{
					Name:       "cc-100-1-1",
					HasServer:  true,
					HasCrumb:   true,
					CrumbUUIDs: []string{busyUUID},
				}},
			},
			want:   StateActive,
			action: ActionNone,
		},
		{
			name: "a socket hosting non-chat work is load-bearing",
			input: Input{
				AgentsOK: true,
				Apply:    true,
				Sockets: []Socket{{
					Name:      "cx-100-1-1",
					HasServer: true,
					Foreign:   []string{"node", "uv"},
				}},
			},
			want:   StateHosts,
			action: ActionNone,
		},
		{
			name: "an unattached idle chat is the reapable case",
			input: Input{
				AgentsOK: true,
				Apply:    true,
				Sockets:  []Socket{live},
			},
			want:   StateOrphan,
			action: ActionKillServer,
		},
		{
			name: "without --apply nothing is ever actioned",
			input: Input{
				AgentsOK: true,
				Sockets:  []Socket{live},
			},
			want:   StateOrphan,
			action: ActionNone,
		},
		{
			name: "an untouched marker-backed fork is explicitly reapable",
			input: Input{
				AgentsOK: true,
				Apply:    true,
				Sockets: []Socket{{
					Name: "cc-100-1-1", HasServer: true, DetachedFork: true,
				}},
			},
			want:   StateFork,
			action: ActionKillServer,
		},
		{
			name: "a failed busy query skips every chat carrying a breadcrumb",
			input: Input{
				Apply:   true,
				Sockets: []Socket{live},
			},
			want:   StateSkip,
			action: ActionNone,
		},
		{
			name: "a claude socket with no breadcrumb is busy-unknown",
			input: Input{
				AgentsOK: true,
				Apply:    true,
				Sockets:  []Socket{{Name: "cc-100-1-1", HasServer: true}},
			},
			want:   StateSkip,
			action: ActionNone,
		},
		{
			name: "a codex socket writes no breadcrumb by design and stays reapable",
			input: Input{
				AgentsOK: true,
				Apply:    true,
				Sockets:  []Socket{{Name: "cx-100-1-1", HasServer: true}},
			},
			want:   StateOrphan,
			action: ActionKillServer,
		},
		{
			name: "an OpenCode socket without a breadcrumb is still busy-unknown",
			input: Input{
				Apply: true,
				// OpenCode exports no session environment, so its socket has no
				// crumb to carry an engine busy result. A failed busy probe must
				// not turn that absence into permission to kill the server.
				Sockets: []Socket{{Name: "ox-100-1-1", HasServer: true}},
			},
			want:   StateSkip,
			action: ActionNone,
		},
		{
			name: "an old socket file with no server is a corpse",
			input: Input{
				AgentsOK:  true,
				Apply:     true,
				DeadAfter: time.Hour,
				Sockets:   []Socket{{Name: "cc-100-1-1", Age: 3 * time.Hour}},
			},
			want:   StateDead,
			action: ActionRemoveSocketFile,
		},
		{
			name: "a young empty socket may be a server still starting",
			input: Input{
				AgentsOK:  true,
				Apply:     true,
				DeadAfter: time.Hour,
				Sockets:   []Socket{{Name: "cc-100-1-1", Age: time.Minute}},
			},
			want:   StateSkip,
			action: ActionNone,
		},
		{
			name: "a probe that could not run is not an empty answer",
			input: Input{
				AgentsOK: true,
				Apply:    true,
				Sockets: []Socket{{
					Name:        "cc-100-1-1",
					ProbeFailed: true,
					ProbeError:  "permission denied",
					Age:         72 * time.Hour,
				}},
			},
			want:   StateSkip,
			action: ActionNone,
		},
		{
			// The order verbatim: "if a chat is idle for 24h, it has to get
			// killed, idle means no activity, 0" — measured against the live
			// fleet using the transcript's own last record, never tmux
			// window_activity and never the transcript file's mtime.
			name: "an attached chat idle past the horizon is reaped",
			input: Input{
				AgentsOK:     true,
				Apply:        true,
				Horizon:      48 * time.Hour,
				ClientActive: time.Hour,
				Sockets: []Socket{{
					Name:         "cc-100-1-1",
					HasServer:    true,
					HasCrumb:     true,
					Attached:     true,
					ClientIdleOK: true,
					ClientIdle:   2 * time.Hour,
					ActivityOK:   true,
					IdleFor:      49 * time.Hour,
				}},
			},
			want:   StateIdle,
			action: ActionKillServer,
		},
		{
			name: "an attached chat inside the horizon is kept",
			input: Input{
				AgentsOK:     true,
				Apply:        true,
				Horizon:      48 * time.Hour,
				ClientActive: time.Hour,
				Sockets: []Socket{{
					Name:         "cc-100-1-1",
					HasServer:    true,
					HasCrumb:     true,
					Attached:     true,
					ClientIdleOK: true,
					ClientIdle:   2 * time.Hour,
					ActivityOK:   true,
					IdleFor:      3 * time.Hour,
				}},
			},
			want:   StateKeep,
			action: ActionNone,
		},
		{
			// Exemption 1, mandatory: "unless it's open for me." A chat
			// sitting at exactly the horizon was on the operator's screen at
			// the moment of a real sweep — the client window wins outright,
			// whatever the transcript idle time looks like.
			name: "exemption 1 — open for the operator outranks the horizon",
			input: Input{
				AgentsOK:     true,
				Apply:        true,
				Horizon:      48 * time.Hour,
				ClientActive: time.Hour,
				Sockets: []Socket{{
					Name:         "cc-100-1-1",
					HasServer:    true,
					HasCrumb:     true,
					Attached:     true,
					ClientIdleOK: true,
					ClientIdle:   5 * time.Minute,
					ActivityOK:   true,
					IdleFor:      200 * time.Hour,
				}},
			},
			want:   StateKeep,
			action: ActionNone,
		},
		{
			// Exemption 2, mandatory, the single most important line in the
			// spec: UNKNOWN is never killed. Absence of a measurement is not
			// a measurement of idleness.
			name: "exemption 2 — an unreadable transcript is UNKNOWN, never reaped",
			input: Input{
				AgentsOK:     true,
				Apply:        true,
				Horizon:      48 * time.Hour,
				ClientActive: time.Hour,
				Sockets: []Socket{{
					Name:         "cc-100-1-1",
					HasServer:    true,
					HasCrumb:     true,
					Attached:     true,
					ClientIdleOK: true,
					ClientIdle:   5 * time.Hour,
					ActivityOK:   false,
				}},
			},
			want:   StateUnknown,
			action: ActionNone,
		},
		{
			// Exemption 3, mandatory: a socket hosting non-chat work is out
			// of the reaper's remit entirely, even attached and idle past
			// the horizon.
			name: "exemption 3 — hosted non-chat work outranks the horizon",
			input: Input{
				AgentsOK:     true,
				Apply:        true,
				Horizon:      48 * time.Hour,
				ClientActive: time.Hour,
				Sockets: []Socket{{
					Name:       "cx-100-1-1",
					HasServer:  true,
					Attached:   true,
					ClientIdle: 5 * time.Hour,
					ActivityOK: true,
					IdleFor:    200 * time.Hour,
					Foreign:    []string{"vite"},
				}},
			},
			want:   StateHosts,
			action: ActionNone,
		},
		{
			name: "dry run: an idle-past-horizon chat is previewed but nothing is actioned",
			input: Input{
				AgentsOK:     true,
				Horizon:      48 * time.Hour,
				ClientActive: time.Hour,
				Sockets: []Socket{{
					Name:         "cc-100-1-1",
					HasServer:    true,
					HasCrumb:     true,
					Attached:     true,
					ClientIdleOK: true,
					ClientIdle:   2 * time.Hour,
					ActivityOK:   true,
					IdleFor:      49 * time.Hour,
				}},
			},
			want:   StateIdle,
			action: ActionNone,
		},
		{
			// The two failure modes probeIdleSignals can hand back, and the
			// exact defect this fix closes: neither may render as "active".
			name: "a client-activity probe that could not run is UNKNOWN, not keep",
			input: Input{
				AgentsOK:     true,
				Apply:        true,
				Horizon:      48 * time.Hour,
				ClientActive: time.Hour,
				Sockets: []Socket{{
					Name:             "cc-100-1-1",
					HasServer:        true,
					HasCrumb:         true,
					Attached:         true,
					ClientIdleOK:     false,
					ClientProbeError: "tmux list-clients: socket did not answer within 5s",
					ActivityOK:       true,
					IdleFor:          49 * time.Hour,
				}},
			},
			want:   StateUnknown,
			action: ActionNone,
		},
		{
			name: "an attached socket where list-clients found no client is UNKNOWN, not keep",
			input: Input{
				AgentsOK:     true,
				Apply:        true,
				Horizon:      48 * time.Hour,
				ClientActive: time.Hour,
				Sockets: []Socket{{
					// The 17-day zombie-client shape: a foreground tmux client
					// whose terminal died. The session still reads Attached,
					// but nobody answers list-clients.
					Name:         "cc-100-1-1",
					HasServer:    true,
					HasCrumb:     true,
					Attached:     true,
					ClientIdleOK: false,
					ActivityOK:   true,
					IdleFor:      49 * time.Hour,
				}},
			},
			want:   StateUnknown,
			action: ActionNone,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			decisions := Plan(testCase.input)
			if len(decisions) != 1 {
				t.Fatalf("Plan() returned %d decisions, want one", len(decisions))
			}
			if decisions[0].State != testCase.want {
				t.Fatalf(
					"state = %q (%s), want %q",
					decisions[0].State,
					decisions[0].Reason,
					testCase.want,
				)
			}
			if decisions[0].Action != testCase.action {
				t.Fatalf(
					"action = %d, want %d (%s)",
					decisions[0].Action,
					testCase.action,
					decisions[0].Reason,
				)
			}
		})
	}
}

// Age is a corpse test, never a staleness test: a chat quiet for a month is
// still a chat, and the only thing its socket's age may decide is whether an
// EMPTY socket is dead or still starting.
func TestPlanNeverReapsFromAgeAlone(t *testing.T) {
	decisions := Plan(Input{
		AgentsOK:     true,
		Apply:        true,
		DeadAfter:    time.Hour,
		ClientActive: time.Hour,
		Sockets: []Socket{{
			Name:         "cc-100-1-1",
			Age:          30 * 24 * time.Hour,
			HasServer:    true,
			Attached:     true,
			ClientIdleOK: true,
			HasCrumb:     true,
		}},
	})
	if decisions[0].State != StateKeep || decisions[0].Action != ActionNone {
		t.Fatalf(
			"a month-old ATTACHED chat was classified %q/%d",
			decisions[0].State,
			decisions[0].Action,
		)
	}
}

// The dry run must be a true preview: the shell original reported fail-closed
// skips as plain orphans, so its preview promised kills the reap never made.
func TestDryRunPreviewsExactlyWhatApplyWouldDo(t *testing.T) {
	sockets := []Socket{
		{Name: "cc-100-1-1", HasServer: true},
		{Name: "cc-200-1-1", HasServer: true, HasCrumb: true},
		{Name: "cx-300-1-1", HasServer: true, Foreign: []string{"vite"}},
	}
	preview := Plan(Input{AgentsOK: true, Sockets: sockets})
	applied := Plan(Input{AgentsOK: true, Apply: true, Sockets: sockets})
	if len(preview) != len(applied) {
		t.Fatalf("preview has %d rows, apply has %d", len(preview), len(applied))
	}
	for index := range preview {
		if preview[index].State != applied[index].State {
			t.Fatalf(
				"%s previewed as %q but applies as %q",
				preview[index].Socket,
				preview[index].State,
				applied[index].State,
			)
		}
		if preview[index].Action != ActionNone {
			t.Fatalf("dry run planned action %d on %s",
				preview[index].Action, preview[index].Socket)
		}
	}
}

// The bunker socket is shared: one idle session is killed by NAME, never by
// killing the server every other terminal is living on.
func TestPlanSweepsBunkerSessionsIndividually(t *testing.T) {
	decisions := Plan(Input{
		AgentsOK:    true,
		Apply:       true,
		VSCTMaxIdle: 7 * 24 * time.Hour,
		VSCT: []VSCTSession{
			{Name: "projc-1", Attached: true, Idle: 30 * 24 * time.Hour},
			{Name: "proja-2", Idle: time.Hour},
			{Name: "old-3", Idle: 30 * 24 * time.Hour},
		},
	})
	want := map[string]struct {
		state  State
		action Action
	}{
		"vsct:projc-1": {StateKeep, ActionNone},
		"vsct:proja-2": {StateKeep, ActionNone},
		"vsct:old-3":   {StateOrphan, ActionKillSession},
	}
	for _, decision := range decisions {
		expected, found := want[decision.Socket]
		if !found {
			t.Fatalf("unexpected row %q", decision.Socket)
		}
		if decision.State != expected.state || decision.Action != expected.action {
			t.Fatalf(
				"%s = %q/%d, want %q/%d",
				decision.Socket,
				decision.State,
				decision.Action,
				expected.state,
				expected.action,
			)
		}
	}
}

// fakeClientIdleTmux answers ClientIdle from canned values and nothing
// else — the exact three-way branch probeIdleSignals turns into
// Socket.ClientIdleOK / ClientIdle / ClientProbeError.
type fakeClientIdleTmux struct {
	idle  time.Duration
	found bool
	err   error
}

func (fakeClientIdleTmux) ListPanes(context.Context, string) ([]gather.ProbePane, error) {
	return nil, nil
}

func (fakeClientIdleTmux) Sessions(context.Context, string) ([]VSCTSession, error) {
	return nil, nil
}

func (fakeClientIdleTmux) KillSession(context.Context, string, string) error {
	return errors.New("unexpected kill-session")
}

func (fake fakeClientIdleTmux) ClientIdle(context.Context, string) (time.Duration, bool, error) {
	return fake.idle, fake.found, fake.err
}

// TestPlanClassifiesEverySocket proves planAttached's decision given
// hand-built Socket fields. It never proves the WIRING that fills those
// fields from a real tmux answer — probeIdleSignals — is honest. This test
// calls that translation directly with a fake probe standing in for tmux, so
// the zero-value-reads-as-active defect cannot be reintroduced in the
// plumbing without a Plan-level test ever noticing.
func TestProbeIdleSignalsNeverTranslatesAnUnansweredProbeIntoFreshness(t *testing.T) {
	cases := []struct {
		name         string
		tmux         fakeClientIdleTmux
		wantErrorSet bool
	}{
		{
			name:         "the probe itself errored",
			tmux:         fakeClientIdleTmux{err: errors.New("tmux list-clients: socket did not answer within 5s")},
			wantErrorSet: true,
		},
		{
			// The 17-day zombie-client shape: Attached is set, but
			// list-clients came back with nobody there.
			name: "the probe ran and found no client",
			tmux: fakeClientIdleTmux{found: false},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			runner := &Runner{tmux: testCase.tmux, now: time.Now}
			sockets := []Socket{{Name: "cc-100-1-1", Attached: true}}
			runner.probeIdleSignals(context.Background(), sockets)
			if sockets[0].ClientIdleOK {
				t.Fatalf("ClientIdleOK = true, want false for %s", testCase.name)
			}
			if sockets[0].ClientIdle != 0 {
				t.Fatalf(
					"ClientIdle = %v, want the zero value left UNREAD (ClientIdleOK false)",
					sockets[0].ClientIdle,
				)
			}
			if testCase.wantErrorSet && sockets[0].ClientProbeError == "" {
				t.Fatal("a probe error must be recorded — its reason must differ from a clean no-client answer")
			}
			if !testCase.wantErrorSet && sockets[0].ClientProbeError != "" {
				t.Fatalf("a clean no-client answer must not carry a probe error: %q", sockets[0].ClientProbeError)
			}
			// Chain straight into the decision the wiring feeds: this is the
			// full path from tmux's answer to the verdict, not just one end
			// of it.
			decision := planAttached(Input{ClientActive: time.Hour}, sockets[0], Decision{})
			if decision.State != StateUnknown {
				t.Fatalf("planAttached after an unanswered probe = %q, want UNKN", decision.State)
			}
		})
	}
}

// The positive case: a probe that genuinely answers must still be believed.
func TestProbeIdleSignalsCarriesAGenuineClientIdleThrough(t *testing.T) {
	runner := &Runner{
		tmux: fakeClientIdleTmux{idle: 2 * time.Hour, found: true},
		now:  time.Now,
	}
	sockets := []Socket{{Name: "cc-100-1-1", Attached: true}}
	runner.probeIdleSignals(context.Background(), sockets)
	if !sockets[0].ClientIdleOK {
		t.Fatal("ClientIdleOK = false for a probe that genuinely answered")
	}
	if sockets[0].ClientIdle != 2*time.Hour {
		t.Fatalf("ClientIdle = %v, want 2h", sockets[0].ClientIdle)
	}
	if sockets[0].ClientProbeError != "" {
		t.Fatalf("a clean answer must not carry a probe error: %q", sockets[0].ClientProbeError)
	}
}
