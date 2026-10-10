package reap

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type quietBusy struct{}

func (quietBusy) BusySessions(context.Context) (map[string]struct{}, error) { return nil, nil }

// TestRunRecordsTheSweepsStateTransitions: the reap coordinator walks
// probed → planned → applied → done through the state door, one comp=state
// record per phase with dur_ms; a sweep of an empty fleet has no decision
// records because it made no decision.
func TestRunRecordsTheSweepsStateTransitions(t *testing.T) {
	ctx, recorder := obs.Test(t)
	root := t.TempDir()
	values := paths.Values{
		TmuxDir: filepath.Join(root, "tmux"), SIDDir: filepath.Join(root, "sid"), ProcRoot: filepath.Join(root, "proc"),
		StateDB: filepath.Join(root, "pfm.db"), Home: filepath.Join(root, "home"),
	}
	for _, dir := range []string{values.TmuxDir, values.SIDDir, values.ProcRoot} {
		if err := mkdir(dir); err != nil {
			t.Fatal(err)
		}
	}
	runner, err := New(Dependencies{
		Paths:      values,
		Tmux:       fakeClientIdleTmux{},
		Proc:       gather.NewProcFS(values.ProcRoot),
		Busy:       quietBusy{},
		KillServer: func(context.Context, string) error { return nil },
		Now:        func() time.Time { return time.Unix(600, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.Run(ctx, Options{Apply: true}); err != nil {
		t.Fatal(err)
	}
	var path []string
	for _, record := range recorder.Records() {
		kind, _ := record.Field("kind")
		if record.Message != "state.transition" || kind != "reap" {
			continue
		}
		if comp, _ := record.Field(obs.FieldComp); comp != "state" {
			t.Fatalf("comp = %v", comp)
		}
		if _, found := record.Field(obs.FieldDur); !found {
			t.Fatalf("no dur_ms: %v", record.Fields)
		}
		next, _ := record.Field("next")
		path = append(path, next.(string))
	}
	if got := strings.Join(path, ","); got != "probed,planned,applied,done" {
		t.Fatalf("state path = %s: %s", got, recorder.Raw())
	}
}

func mkdir(dir string) error { return os.MkdirAll(dir, 0o700) }

type freshSafetyTmux struct {
	socket      string
	panes       []gather.ProbePane
	clientIdle  time.Duration
	clientFound bool
	clientErr   error
	sessions    []VSCTSession
	sessionsErr error
	onSessions  func()
}

func (probe *freshSafetyTmux) ListPanes(context.Context, string) ([]gather.ProbePane, error) {
	if probe.panes != nil {
		return probe.panes, nil
	}
	return []gather.ProbePane{{Socket: probe.socket, PaneID: "%1", Attached: true}}, nil
}

func (probe *freshSafetyTmux) ClientIdle(context.Context, string) (time.Duration, bool, error) {
	return probe.clientIdle, probe.clientFound, probe.clientErr
}

func (probe *freshSafetyTmux) Sessions(context.Context, string) ([]VSCTSession, error) {
	if probe.onSessions != nil {
		probe.onSessions()
	}
	return probe.sessions, probe.sessionsErr
}
func (*freshSafetyTmux) KillSession(context.Context, string, string) error { return nil }

type freshSafetyBusy struct {
	ids map[string]struct{}
	err error
}

func (probe *freshSafetyBusy) BusySessions(context.Context) (map[string]struct{}, error) {
	return probe.ids, probe.err
}

func freshSafetyRunner(t *testing.T, socket string) (*Runner, *freshSafetyTmux, *freshSafetyBusy, string) {
	t.Helper()
	root := testjail.ShortRoot(t)
	values := paths.Values{
		Home:    root,
		StateDB: filepath.Join(root, "state.db"),
		TmuxDir: filepath.Join(root, "tmux"),
		SIDDir:  filepath.Join(root, "sid"),
	}
	for _, dir := range []string{values.TmuxDir, values.SIDDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("unix", filepath.Join(values.TmuxDir, socket))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close socket fixture: %v", err)
		}
	})
	const id = "11111111-1111-4111-8111-111111111111"
	transcript := filepath.Join(root, id+".jsonl")
	old := time.Unix(1800000000, 0).Add(-72 * time.Hour)
	if err := os.WriteFile(transcript, []byte(`{"timestamp":"`+old.Format(time.RFC3339)+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(transcript, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(values.SIDDir, socket), []byte(transcript+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := &freshSafetyTmux{socket: socket, clientIdle: 72 * time.Hour, clientFound: true}
	busy := &freshSafetyBusy{}
	runner, err := New(
		Dependencies{
			Paths:      values,
			Tmux:       probe,
			Busy:       busy,
			Proc:       fakeProc{},
			Now:        func() time.Time { return time.Unix(1800000000, 0) },
			KillServer: func(context.Context, string) error { return nil },
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	return runner, probe, busy, transcript
}

func TestApplyReclassifiesImmediatelyBeforeEachKill(t *testing.T) {
	const socket = "cc-900-1-1"
	const id = "11111111-1111-4111-8111-111111111111"
	for _, race := range []struct {
		name  string
		state State
	}{
		{"busy turn starts", StateBusy},
		{"transcript written", StateActive},
		{"client active", StateKeep},
		{"busy probe fails", StateSkip},
		{"client probe fails", StateUnknown},
		{"client disappears", StateUnknown},
		{"transcript unreadable", StateUnknown},
		{"foreign process starts", StateHosts},
		{"still idle", StateKilled},
	} {
		t.Run(race.name, func(t *testing.T) {
			runner, probe, busy, transcript := freshSafetyRunner(t, socket)
			input := Input{
				Apply:        true,
				AgentsOK:     true,
				BusyRecent:   time.Minute,
				Horizon:      48 * time.Hour,
				ClientActive: time.Hour,
				Sockets: []Socket{
					{
						Name:         socket,
						HasServer:    true,
						Attached:     true,
						HasCrumb:     true,
						CrumbUUIDs:   []string{id},
						ActivityOK:   true,
						IdleFor:      72 * time.Hour,
						ClientIdleOK: true,
						ClientIdle:   72 * time.Hour,
					},
				},
			}
			plan := Plan(input)
			if len(plan) != 1 || plan[0].Action != ActionKillServer {
				t.Fatalf("fixture did not plan kill: %+v", plan)
			}
			switch race.name {
			case "busy turn starts":
				busy.ids = map[string]struct{}{id: {}}
			case "transcript written":
				if err := os.WriteFile(
					transcript,
					[]byte(`{"timestamp":"`+runner.now().Format(time.RFC3339)+`"}`+"\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(transcript, runner.now(), runner.now()); err != nil {
					t.Fatal(err)
				}
			case "client active":
				probe.clientIdle = 0
			case "busy probe fails":
				busy.err = errors.New("busy unavailable")
			case "client probe fails":
				probe.clientErr = errors.New("client unavailable")
			case "client disappears":
				probe.clientFound = false
			case "transcript unreadable":
				if err := os.Remove(transcript); err != nil {
					t.Fatal(err)
				}
			case "foreign process starts":
				runner.proc = newFleetProc()
				probe.panes = []gather.ProbePane{{Socket: socket, PaneID: "%1", Attached: true, PID: 200}}
			}
			applied, _ := runner.apply(context.Background(), plan, Options{})
			if len(applied) != 1 || applied[0].State != race.state {
				t.Fatalf("stale plan killed a freshly protected chat: got=%+v want=%s", applied, race.state)
			}
		})
	}
}

func TestApplyRechecksVSCTBeforeKillingSession(t *testing.T) {
	for _, race := range []string{"attached", "recent", "probe failure", "still idle"} {
		t.Run(race, func(t *testing.T) {
			runner, probe, _, _ := freshSafetyRunner(t, "cc-901-1-1")
			input := Input{
				Apply:       true,
				VSCTMaxIdle: vsctMaxIdle,
				VSCT:        []VSCTSession{{Name: "old", Idle: 30 * 24 * time.Hour}},
			}
			plan := Plan(input)
			if len(plan) != 1 || plan[0].Action != ActionKillSession {
				t.Fatalf("fixture did not plan session kill: %+v", plan)
			}
			probe.sessions = []VSCTSession{{Name: "old", Idle: 30 * 24 * time.Hour}}
			want := StateKeep
			switch race {
			case "attached":
				probe.sessions[0].Attached = true
			case "recent":
				probe.sessions[0].Idle = 0
			case "probe failure":
				probe.sessionsErr = errors.New("sessions unavailable")
				want = StateSkip
			case "still idle":
				want = StateKilled
			}
			applied, _ := runner.apply(context.Background(), plan, Options{})
			if len(applied) != 1 || applied[0].State != want {
				t.Fatalf("stale plan killed a freshly protected bunker: got=%+v want=%s", applied, want)
			}
		})
	}
}

func TestApplyDoesNotRunUnrelatedProbesAfterTargetSafety(t *testing.T) {
	const socket = "cc-902-1-1"
	runner, probe, _, _ := freshSafetyRunner(t, socket)
	// Bunker probing activates a real client while the old refresh is finishing.
	probe.onSessions = func() { probe.clientIdle = 0 }
	decision := runner.reclassifyKill(context.Background(), Decision{
		Socket: socket, State: StateIdle, Action: ActionKillServer,
	}, Options{})
	if decision.Action == ActionKillServer && probe.clientIdle == 0 {
		t.Fatalf("unrelated bunker probe invalidated the target safety reading: %+v", decision)
	}
}

func TestBusyUnknownPreviewMatchesApply(t *testing.T) {
	runner, _, busy, _ := freshSafetyRunner(t, "cc-903-1-1")
	busy.err = errors.New("busy unavailable")
	preview, err := runner.Run(context.Background(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := runner.Run(context.Background(), Options{Apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Decisions) != 1 || len(applied.Decisions) != 1 ||
		preview.Decisions[0].State != StateSkip || applied.Decisions[0].State != StateSkip {
		t.Fatalf("unknown busy probe preview/apply differ: preview=%+v apply=%+v", preview, applied)
	}
}
