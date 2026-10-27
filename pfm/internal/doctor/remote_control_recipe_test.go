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
		{name: "known account", env: "known", want: "recipes checked=1"},
		{name: "missing account", want: "recipe[stored] BLOCKED: account missing", warnings: 1},
		{name: "unknown account", env: "CLAUDE_CONFIG_DIR=/fixture/retired", want: "recipe[stored] BLOCKED", warnings: 1},
		{name: "ambiguous account", env: "known", mode: "ambiguous", want: "recipe[stored] BLOCKED", warnings: 1},
		{name: "rosterless", mode: "rosterless", want: "recipes checked=1"},
		{name: "not native", backend: "tmux", want: "recipes checked=0"},
		{name: "not Claude", engine: "cx", want: "recipes checked=0"},
		{name: "missing recipe", mode: "missing", want: "recipe[stored] FAIL stored recipe unreadable", failures: 1},
		{name: "malformed recipe", mode: "malformed", want: "recipe[stored] FAIL stored recipe unreadable", failures: 1},
		{name: "unreadable recipe", mode: "unreadable", want: "recipe[stored] FAIL stored recipe unreadable", failures: 1},
		{name: "empty enumeration", mode: "empty", want: "recipes checked=0"},
		{name: "absent database", mode: "absent", want: "recipes checked=0 (state database absent)"},
		{name: "failed enumeration", mode: "corrupt", want: "recipes FAIL enumerate stored seats", failures: 1},
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
			if !strings.Contains(output.String(), "doctor: remote_control "+test.want) ||
				tally.warnings != test.warnings || tally.failures != test.failures || strings.Contains(output.String(), "secret") {
				t.Fatalf("warnings=%d failures=%d, want %d and %d; output=%s",
					tally.warnings, tally.failures, test.warnings, test.failures, output.String())
			}
		})
	}
}
