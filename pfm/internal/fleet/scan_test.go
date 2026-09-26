package fleet

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// TestScanRecordsATransition: Scan walks the state door (spec § Middleware,
// `state`) — stale to scanned, comp=state, kind=fleet — never the composed
// rows themselves.
func TestScanRecordsATransition(t *testing.T) {
	testjail.Fleet(t)
	recorderCtx, recorder := obs.Test(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	var stderr bytes.Buffer
	if _, err := Scan(recorderCtx, database, Request{View: compose.DefaultView, ReadOnly: true}, &stderr); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "fleet" {
			continue
		}
		if next, _ := record.Field("next"); next == "scanned" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Scan() wrote no fleet->scanned transition: %s", recorder.Raw())
	}
}

func TestMain(m *testing.M) {
	os.Exit(testjail.Run(m))
}

func jailRuntime(t *testing.T) *pfmconfig.Runtime {
	t.Helper()
	home := t.TempDir()
	return &pfmconfig.Runtime{
		Config: pfmconfig.Defaults(home, []string{filepath.Join(home, ".cc", "1", "projects")}),
		Paths:  paths.Values{Home: home, FleetDB: filepath.Join(home, ".cc", "fleet.db")},
	}
}

// TestResolveEnvPinsTheClockUnderTest pins TestNowNSEnv: a jail's fixtures
// carry fixed timestamps, so the scan clock must be the one the test names.
func TestResolveEnvPinsTheClockUnderTest(t *testing.T) {
	t.Setenv(TestNowNSEnv, "172800000000000")
	runtime := jailRuntime(t)
	env, err := ResolveEnv(Request{Runtime: runtime})
	if err != nil {
		t.Fatalf("ResolveEnv() = %v", err)
	}
	if env.NowNS != 172800000000000 {
		t.Fatalf("NowNS = %d, want the pinned clock", env.NowNS)
	}
	if env.Paths.Home != runtime.Paths.Home || env.Primary != 1 {
		t.Fatalf("env = %+v, want the runtime's paths and the first account as primary", env)
	}
	if env.CurrentDir == "" {
		t.Fatal("CurrentDir is empty")
	}
}

// TestResolveEnvRefusesAnUnreadableClock pins the broken state: a clock the
// test named but that does not parse is an error, never the real clock.
func TestResolveEnvRefusesAnUnreadableClock(t *testing.T) {
	t.Setenv(TestNowNSEnv, "yesterday")
	_, err := ResolveEnv(Request{Runtime: jailRuntime(t)})
	if err == nil || !strings.Contains(err.Error(), TestNowNSEnv) {
		t.Fatalf("ResolveEnv() error = %v, want one naming %s", err, TestNowNSEnv)
	}
}

// TestComposeCarriesTheDefaultViewsCachedCounts pins that a capped load's
// totals survive compose: the default view reads capped candidates, so the
// killed and suppressed counts can only come from the load.
func TestComposeCarriesTheDefaultViewsCachedCounts(t *testing.T) {
	output := ComposeFleet(
		Env{Config: pfmconfig.Defaults(t.TempDir(), nil)},
		compose.DefaultView,
		Data{CachedCounts: &store.CachedCounts{Killed: 7, Suppressed: 3}},
		gather.Snapshot{},
	)
	if output.KilledCount != 7 || output.SuppressedCount != 3 {
		t.Fatalf("counts = %d killed, %d suppressed; want 7, 3", output.KilledCount, output.SuppressedCount)
	}
	if env := (Env{Paths: paths.Values{Home: "h"}}); env.Runtime().Paths.Home != "h" {
		t.Fatalf("Env.Runtime() dropped the paths: %+v", env.Runtime())
	}
}
