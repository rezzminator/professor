package hookentry

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestReleaseNativeClaudeReports(t *testing.T) {
	const (
		release = "pane.release_agent pane=w1:p1 source=herdr:claude agent=claude seq=1000000000"
		session = "pane.report_agent_session pane=w1:p1 source=herdr:claude agent=claude session=s1 seq=1000000001"
	)
	for _, tc := range []struct {
		name     string
		source   string // the source the older build reported under
		terminal string
		// mode is the pane's fate since: "" as reported, "replaced" by another
		// owner, "moved", "closed", "refused" its release, "herdr gone".
		mode    string
		want    []string
		pending bool // the release is left for the next install
		kept    bool // the target stays: pfm still reports there
	}{
		{name: "reported under herdr:claude", source: "herdr:claude", want: []string{release, session}},
		{
			// Before sources were recorded every report went out under pfm's own.
			name: "reported under no recorded source",
			want: []string{"pane.release_agent pane=w1:p1 source=pfm agent=claude seq=12"},
		},
		{name: "reported under pfm's own source", source: "pfm", kept: true},
		{
			// No pfm build reports under herdr:claude now: the stale state goes, the
			// old session is not republished over the new owner's.
			name: "taken by another owner", source: "herdr:claude", mode: "replaced", want: []string{release},
		},
		{
			// A newer report of the same owner went out under pfm's source, which a
			// herdr:claude release leaves alone.
			name: "reported to since by the same owner", source: "herdr:claude", mode: "newer",
			want: []string{release, session},
		},
		{
			name: "moved with its terminal", source: "herdr:claude", terminal: "native-terminal", mode: "moved",
			// The pane.list that finds the terminal takes the first seq.
			want: []string{
				"pane.list",
				"pane.release_agent pane=w2:p2 source=herdr:claude agent=claude seq=1000000001",
				"pane.report_agent_session pane=w2:p2 source=herdr:claude agent=claude session=s1 seq=1000000002",
			},
		},
		{
			name: "closed since", source: "herdr:claude", terminal: "native-terminal", mode: "closed",
			want: []string{"pane.list"},
		},
		{name: "herdr no longer running", source: "herdr:claude", terminal: "native-terminal", mode: "herdr gone"},
		{name: "release refused", source: "herdr:claude", mode: "refused", want: []string{release}, pending: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := newFakeHerdr(t)
			switch tc.mode {
			case "moved":
				server.addPane("w2:p2", "native-terminal")
			case "closed":
			default:
				server.addPane("w1:p1", "native-terminal")
			}
			socket := server.path
			if tc.mode == "herdr gone" {
				socket = filepath.Join(testjail.ShortRoot(t), "gone.sock")
			}
			if tc.mode == "refused" {
				server.refuse["pane.release_agent"] = "refused"
			}
			jail := newHerdrJail(t)
			now := int64(time.Second)
			deps := jail.deps(map[string]string{}, &now)
			// A still clock: each request's seq is the next one after the install's time.
			deps.now = func() time.Time { return time.Unix(0, int64(time.Second)) }
			reporter := &herdrReporter{deps: deps}
			if err := os.MkdirAll(jail.lockDir, 0o700); err != nil {
				t.Fatal(err)
			}
			saved := herdrActivityTarget{
				Pane: "w1:p1", Socket: socket, Terminal: tc.terminal, Owner: nativeOwner, Seq: 10, Native: true,
				Source: tc.source,
			}
			owner := herdrPaneOwner{Client: nativeOwner, Seq: 10}
			switch tc.mode {
			case "replaced":
				owner.Client = "native\x00seat-2\x00native-terminal\x00900002\x0010\x0011\x00claude\x00w1:p1\x00w1"
			case "newer":
				owner.Seq = 20
			}
			ownerRecord, err := json.Marshal(owner)
			if err != nil {
				t.Fatal(err)
			}
			target := herdrTarget{socket: socket, pane: saved.Pane, terminal: saved.Terminal}
			if err := os.WriteFile(reporter.paneOwnerPath(target), ownerRecord, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := reporter.writeActivity("s1", herdrActivity{
				Stored:  herdrStored{Engine: "claude", SessionID: "s1", State: "working"},
				Targets: []herdrActivityTarget{saved},
			}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(reporter.watchRecord("s1"), []byte("7\n"), 0o600); err != nil {
				t.Fatal(err)
			}

			err = reporter.releaseNativeClaudeReports(context.Background())
			if got := herdrRequests(t, server); !slices.Equal(got, tc.want) {
				t.Fatalf("requests = %q, want %q", got, tc.want)
			}
			if tc.pending {
				refusal := "hand native Claude panes of " + reporter.activityPath("s1") +
					" back to Herdr: herdr refused " +
					"pane.release_agent pfm-1000000000 pane w1:p1: refused: refused by fake herdr"
				if err == nil || err.Error() != refusal {
					t.Fatalf("err = %v, want %s", err, refusal)
				}
				state, readErr := reporter.readActivity("s1")
				if readErr != nil || len(state.Targets) != 0 ||
					!slices.Equal(state.RetiredTargets, []herdrActivityTarget{saved}) {
					t.Fatalf("record %s = %+v (err %v), want the refused release pending alone",
						reporter.activityPath("s1"), state, readErr)
				}
				delete(server.refuse, "pane.release_agent")
				retry := &herdrReporter{deps: deps}
				if err := retry.releaseNativeClaudeReports(context.Background()); err != nil {
					t.Fatalf("retry: %v", err)
				}
				want := []string{release, release, session}
				if got := herdrRequests(t, server); !slices.Equal(got, want) {
					t.Fatalf("requests after the retry = %q, want %q", got, want)
				}
			} else if err != nil {
				t.Fatalf("err = %v, want the pane handed back", err)
			}
			if tc.kept {
				state, err := reporter.readActivity("s1")
				if err != nil || !slices.Equal(state.Targets, []herdrActivityTarget{saved}) ||
					len(state.RetiredTargets) != 0 {
					t.Fatalf("record %s = %+v (err %v), want its held target kept",
						reporter.activityPath("s1"), state, err)
				}
				if _, err := os.Stat(reporter.watchRecord("s1")); err != nil {
					t.Fatalf("stat %s: %v, want the held chat's watch record kept", reporter.watchRecord("s1"), err)
				}
			} else {
				for _, file := range []string{reporter.activityPath("s1"), reporter.watchRecord("s1")} {
					if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("stat %s: %v, want the orphan record removed", file, err)
					}
				}
			}
			sent := len(server.requests())
			again := &herdrReporter{deps: deps}
			err = again.releaseNativeClaudeReports(context.Background())
			if err != nil || len(server.requests()) != sent {
				t.Fatalf("second run: err %v, requests %q, want nothing more than %d", err, server.requests(), sent)
			}
		})
	}
}

func TestReleaseNativeClaudeReportsSparesTheTmuxBridge(t *testing.T) {
	rig := newChatRig(t)
	rig.herdr.addPane("w2:p2", "native-terminal")
	rig.seed(rig.registered("working", map[string]herdrClient{"4242": rig.client("w1:p1", "term_1")}))
	rig.wantQuiet(rig.engine("agent-start", `{"session_id":"s1","agent_id":"a","hook_event_name":"SubagentStart"}`))
	rig.wantQuiet(rig.engine("idle", `{"session_id":"s1"}`))
	deps := rig.jail.deps(rig.env, &rig.clock)
	deps.tmux, deps.listPanes = rig.tmux.run, rig.tmux.listPanes
	reporter := &herdrReporter{deps: deps, options: herdrOptions{engine: pfmengine.Claude, event: herdr.EventIdle}}
	state, err := reporter.readActivity("s1")
	if err != nil || len(state.Targets) != 1 {
		t.Fatalf("tmux record %s = %+v (err %v), want its one tmux target", reporter.activityPath("s1"), state, err)
	}
	tmuxTarget := state.Targets[0]
	// An older build's native report on another pane, recorded under the same session.
	state.Targets = append(state.Targets, herdrActivityTarget{
		Pane: "w2:p2", Socket: rig.herdr.path, Seq: 10, Native: true, Source: herdr.ClaudeSource,
	})
	if err := reporter.writeActivity("s1", state); err != nil {
		t.Fatal(err)
	}
	before := len(rig.herdr.requests())
	reporter.deps.now = func() time.Time { return time.Unix(0, 1_000_000) }
	if err := reporter.releaseNativeClaudeReports(context.Background()); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"pane.release_agent pane=w2:p2 source=herdr:claude agent=claude seq=1000000",
		"pane.report_agent_session pane=w2:p2 source=herdr:claude agent=claude session=s1 seq=1000001",
	}
	if got := herdrRequests(t, rig.herdr)[before:]; !slices.Equal(got, want) {
		t.Fatalf("requests = %q, want %q", got, want)
	}
	got, err := reporter.readActivity("s1")
	if err != nil || !slices.Equal(got.Targets, []herdrActivityTarget{tmuxTarget}) || len(got.RetiredTargets) != 0 ||
		len(got.Stored.Agents) != 1 {
		t.Fatalf(
			"record %s = %+v (err %v), want the tmux target and its agent kept",
			reporter.activityPath("s1"),
			got,
			err,
		)
	}
	sent := len(rig.herdr.requests())
	err = reporter.releaseNativeClaudeReports(context.Background())
	if err != nil || len(rig.herdr.requests()) != sent {
		t.Fatalf("second run: err %v, requests %q, want nothing more than %d", err, rig.herdr.requests(), sent)
	}
	// The tmux chat's own agent lease still settles its pane.
	reporter.deps.now = deps.now
	rig.clock += int64(time.Hour + time.Second)
	if done, err := reporter.expireAgents("s1"); err != nil || !done {
		t.Fatalf("tmux expiry: done=%v err=%v", done, err)
	}
	assertActivityReport(t, rig.herdr, "idle", "")
}
