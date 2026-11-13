package doctor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/fleetdb"
)

func TestDoctorRemoteControlUnattributedRecipe(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	seat := fleetdb.Seat{
		SeatID: "legacy-claude", Backend: "herdr", Engine: "cc", State: "failed", Name: "legacy\nchat",
	}
	if err := db.PutSeat(ctx, seat); err != nil {
		t.Fatal(err)
	}
	run := fleetdb.SeatRun{
		Binary: "claude",
		Args:   []string{"--resume", "old-session"},
		Env:    []string{"ANTHROPIC_API_KEY=secret"},
	}
	if err := db.WriteSeatRun(seat.SeatID, run); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(filepath.Dir(runtime.Paths.StateDB), "seats", seat.SeatID+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	runDoctor(nil, &stdout, &stderr, runtime)
	want := "doctor: remote_control recipe[legacy-claude] BLOCKED: account missing, ambiguous, or no longer configured"
	if !strings.Contains(stdout.String(), want) ||
		!strings.Contains(stdout.String(), `chat="legacy\nchat"; Remote Control not requested`) {
		t.Fatalf("missing separate unattributed-recipe warning: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "doctor: remote_control claude[1] on (knob on") {
		t.Fatalf("unattributed recipe incorrectly changes account intent: %s", stdout.String())
	}
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.HasPrefix(line, "doctor: remote_control ") && strings.Contains(line, "secret") {
			t.Fatalf("recipe environment leaked: %s", line)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("doctor mutated saved recipe")
	}
}

func TestRemoteControlDoctorRecipeStates(t *testing.T) {
	for _, test := range []struct {
		name     string
		env      string
		backend  string
		engine   string
		mode     string
		want     string
		warnings int
		failures int
	}{
		{name: "known account", env: "known", want: "doctor: remote_control recipes checked=1 native Claude stored recipes\n"},
		{name: "missing account", want: "doctor: remote_control recipe[stored] BLOCKED: account missing, ambiguous, or no longer configured; chat=\"\"; Remote Control not requested\ndoctor: remote_control recipes checked=1 native Claude stored recipes\n", warnings: 1},
		{name: "unknown account", env: "CLAUDE_CONFIG_DIR=/fixture/retired", want: "doctor: remote_control recipe[stored] BLOCKED: account missing, ambiguous, or no longer configured; chat=\"\"; Remote Control not requested\ndoctor: remote_control recipes checked=1 native Claude stored recipes\n", warnings: 1},
		{name: "ambiguous account", env: "known", mode: "ambiguous", want: "doctor: remote_control recipe[stored] BLOCKED: account missing, ambiguous, or no longer configured; chat=\"\"; Remote Control not requested\ndoctor: remote_control recipes checked=1 native Claude stored recipes\n", warnings: 1},
		{name: "rosterless", mode: "rosterless", want: "doctor: remote_control recipes checked=1 native Claude stored recipes\n"},
		{name: "not native", backend: "tmux", want: "doctor: remote_control recipes checked=0 native Claude stored recipes\n"},
		{name: "not Claude", engine: "cx", want: "doctor: remote_control recipes checked=0 native Claude stored recipes\n"},
		{name: "missing recipe", mode: "missing", want: "doctor: remote_control recipe[stored] FAIL stored recipe unreadable or malformed; chat=\"\"\ndoctor: remote_control recipes checked=1 native Claude stored recipes\n", failures: 1},
		{name: "malformed recipe", mode: "malformed", want: "doctor: remote_control recipe[stored] FAIL stored recipe unreadable or malformed; chat=\"\"\ndoctor: remote_control recipes checked=1 native Claude stored recipes\n", failures: 1},
		{name: "unreadable recipe", mode: "unreadable", want: "doctor: remote_control recipe[stored] FAIL stored recipe unreadable or malformed; chat=\"\"\ndoctor: remote_control recipes checked=1 native Claude stored recipes\n", failures: 1},
		{name: "empty enumeration", mode: "empty", want: "doctor: remote_control recipes checked=0 native Claude stored recipes\n"},
		{name: "absent database", mode: "absent", want: "doctor: remote_control recipes checked=0 (state database absent)\n"},
		{name: "failed enumeration", mode: "corrupt", want: "doctor: remote_control recipes FAIL enumerate stored seats: open state database {root}/.local/state/pfm/pfm.db read-only: sqlite database {root}/.local/state/pfm/pfm.db is a file this test process did not write; a test that needs SQLite's own file calls testjail.DatabaseFiles\n", failures: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			ctx := context.Background()
			db := fleetdb.OpenSharedState(ctx, runtime.Paths)
			seat := fleetdb.Seat{SeatID: "stored", Backend: "herdr", Engine: "cc", State: "failed"}
			if test.backend != "" {
				seat.Backend = test.backend
			}
			if test.engine != "" {
				seat.Engine = test.engine
			}
			if test.mode != "empty" {
				if err := db.PutSeat(ctx, seat); err != nil {
					t.Fatal(err)
				}
			}
			env := test.env
			if env == "known" {
				env = "CLAUDE_CONFIG_DIR=" + runtime.Config.Accounts[0].ConfigDir
			}
			if test.mode == "ambiguous" {
				other := runtime.Config.Accounts[0]
				other.ID = 900
				runtime.Config.Accounts = append(runtime.Config.Accounts, other)
			}
			if test.mode == "rosterless" {
				runtime.Config.Accounts = nil
			}
			if test.mode != "missing" {
				if err := db.WriteSeatRun(
					seat.SeatID,
					fleetdb.SeatRun{Binary: "claude", Env: []string{env}},
				); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			recipe := filepath.Join(filepath.Dir(runtime.Paths.StateDB), "seats", seat.SeatID+".json")
			switch test.mode {
			case "malformed":
				if err := os.WriteFile(recipe, []byte(`{"binary":{"secret":"secret"}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "unreadable":
				if err := os.Remove(recipe); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(recipe, 0o700); err != nil {
					t.Fatal(err)
				}
			case "absent":
				if err := os.Remove(runtime.Paths.StateDB); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(runtime.Paths.StateDB, []byte("not SQLite"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			tally := &doctorTally{}
			printRemoteControlRecipeDoctor(ctx, &output, runtime.Paths, runtime.Config, tally)
			if got := rooted(output.String(), runtime.Paths.Home); got != test.want ||
				tally.warnings != test.warnings || tally.failures != test.failures {
				t.Errorf("output = %q, want %q; warnings=%d failures=%d, want %d %d",
					got, test.want, tally.warnings, tally.failures, test.warnings, test.failures)
			}
		})
	}
}
