package reload

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

func TestNativeReloadPublishesCurrentRemoteControlPolicy(t *testing.T) {
	f := newReloadHerdrFixture(t)
	f.request.Machine.Claude.RemoteControl = true
	// A pre-knob recipe is deliberately reused: the worker must render anew,
	// not perpetuate the birth recipe's lack of the interactive flag.
	_, err := RunHerdr(context.Background(), f.request, f.options, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	run, err := f.db.ReadSeatRun(f.seat.SeatID)
	if err != nil {
		t.Fatal(err)
	}
	at := slices.Index(run.Args, "--remote-control")
	if at < 0 || at+1 == len(run.Args) || run.Args[at+1] != f.seat.Name || slices.Contains(run.Args, "--name") {
		t.Fatalf("reload published a legacy recipe without Remote Control: %q", run.Args)
	}
}

func TestSavedRecipeRemoteControlAccountPolicy(t *testing.T) {
	for _, scenario := range []string{"machine on", "machine off", "account off", "account on", "unknown", "ambiguous", "missing", "print", "codex", "opencode"} {
		t.Run(scenario, func(t *testing.T) {
			machine := pfmconfig.Defaults(t.TempDir(), nil)
			machine.Accounts = nil
			run := fleetdb.SeatRun{
				Binary:     "/fixture/claude",
				CWD:        "/work",
				Args:       []string{"--remote-control", "old label", "--resume", "session"},
				Env:        []string{"CLAUDE_CONFIG_DIR=/fixture/seat"},
				CostLedger: true,
			}
			engine := pfmengine.Claude
			want := true
			switch scenario {
			case "machine off":
				machine.Claude.RemoteControl, want = false, false
			case "account off", "account on":
				prefs := machine.Claude
				prefs.RemoteControl = scenario == "account on"
				machine.Claude.RemoteControl = !prefs.RemoteControl
				machine.Accounts = []pfmconfig.Account{{ID: 7, ConfigDir: "/fixture/seat", Claude: &prefs}}
				want = prefs.RemoteControl
			case "unknown", "missing", "ambiguous":
				machine.Accounts = []pfmconfig.Account{{ID: 7, ConfigDir: "/fixture/seat"}}
				if scenario == "unknown" {
					run.Env = []string{"CLAUDE_CONFIG_DIR=/fixture/retired"}
				}
				if scenario == "missing" {
					run.Env = nil
				}
				if scenario == "ambiguous" {
					machine.Accounts = append(machine.Accounts, pfmconfig.Account{ID: 8, ConfigDir: "/fixture/seat"})
				}
			case "print":
				run.Args = append(run.Args, "--print", "query")
				want = false
			case "codex":
				engine = pfmengine.Codex
			case "opencode":
				engine = pfmengine.OpenCode
			}
			before := run
			_, recorder := obs.Test(t)
			var warning bytes.Buffer
			got := RefreshRecipeRemoteControl(run, engine, machine, "current label", &warning)
			if scenario == "unknown" || scenario == "missing" || scenario == "ambiguous" {

				if slices.Contains(got.Args, "--remote-control") {
					t.Fatalf("unresolved account retained remote access: %q", got.Args)
				}
				if !strings.Contains(warning.String(), "WARN") ||
					!strings.Contains(warning.String(), "current label") ||
					!strings.Contains(warning.String(), "recipe account") {
					t.Fatalf("missing visible policy warning: %s", warning.String())
				}
				records := recorder.Records()
				if len(records) != 1 || records[0].Level != "WARN" {
					t.Fatalf("missing activity warning: %+v", records)
				}
				return
			}
			if engine != pfmengine.Claude {
				if !reflect.DeepEqual(got, before) {
					t.Fatalf("non-Claude recipe changed: %+v", got)
				}
				return
			}
			at := slices.Index(got.Args, "--remote-control")
			if (at >= 0) != want {
				t.Fatalf("Remote Control present=%t, want %t: %q", at >= 0, want, got.Args)
			}
			if at >= 0 && (at+1 >= len(got.Args) || got.Args[at+1] != "current label") {
				t.Fatalf("stale Remote Control name: %q", got.Args)
			}
			got.Args = before.Args
			if !reflect.DeepEqual(got, before) {
				t.Fatalf("policy refresh changed unrelated launch fields: %+v", got)
			}
		})
	}
}
