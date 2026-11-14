package deps_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestEngineProbeHomeSweepsAKilledProbesLeftover: a probe killed before its
// deferred Remove (a usage hook timed out under load) leaves its home behind;
// the next probe home clears every leftover past ProbeHomeLeakAge and keeps a
// live probe's home and anything that is not a probe home.
func TestEngineProbeHomeSweepsAKilledProbesLeftover(t *testing.T) {
	sid := filepath.Join(t.TempDir(), "sid")
	t.Setenv(paths.EnvSIDDir, sid)
	now := time.Now()
	ages := map[string]time.Duration{
		paths.SIDEngineProbeHomePrefix + "leaked":  deps.ProbeHomeLeakAge + time.Minute,
		paths.SIDEngineProbeHomePrefix + "running": deps.ProbeHomeLeakAge - time.Minute,
		"scratch-not-a-probe-home":                 deps.ProbeHomeLeakAge + time.Hour,
	}
	for name, age := range ages {
		dir := filepath.Join(sid, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, now.Add(-age), now.Add(-age)); err != nil {
			t.Fatal(err)
		}
	}
	home, err := deps.NewEngineProbeHome(pfmengine.Claude)
	if err != nil {
		t.Fatalf("NewEngineProbeHome: %v", err)
	}
	t.Cleanup(func() { _ = home.Remove() })
	want := map[string]bool{
		paths.SIDEngineProbeHomePrefix + "leaked":  false,
		paths.SIDEngineProbeHomePrefix + "running": true,
		"scratch-not-a-probe-home":                 true,
		filepath.Base(home.Dir):                    true,
	}
	for name, present := range want {
		_, statErr := os.Stat(filepath.Join(sid, name))
		if got := statErr == nil; got != present {
			t.Errorf("%s present = %v, want %v (stat: %v)", filepath.Join(sid, name), got, present, statErr)
		}
	}
}
