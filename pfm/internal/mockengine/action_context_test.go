package mockengine

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

func TestActionEnvironmentRefreshesEveryHook(t *testing.T) {
	path := filepath.Join(t.TempDir(), "action.json")
	proc := &process{
		script: newScript(Scenario{ActionFile: path}, engineClaude),
		env:    func(string) string { return "birth" },
	}
	base := []string{"PATH=/fixture/bin", obs.EnvBeat + "=stale", obs.EnvRun + "=stale", obs.EnvBeatFile + "=/stale"}
	for _, beat := range []string{"A", "B"} {
		if err := os.WriteFile(path, []byte(`{"beat":"`+beat+`","run":"synthetic"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		actual, err := proc.actionEnvironment(base)
		expected := []string{
			"PATH=/fixture/bin",
			obs.EnvBeat + "=" + beat,
			obs.EnvRun + "=synthetic",
			obs.EnvBeatFile + "=" + path,
		}
		if err != nil || !reflect.DeepEqual(actual, expected) {
			t.Fatalf("%s: environment=%v err=%v, want %v", path, actual, err, expected)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if actual, err := proc.actionEnvironment(base); err == nil {
		t.Fatalf("%s: missing channel accepted as %v", path, actual)
	}
	proc.script.ActionFile = ""
	if actual, err := proc.actionEnvironment(base); err != nil || !reflect.DeepEqual(actual, base) {
		t.Fatalf("unset channel environment=%v err=%v, want %v", actual, err, base)
	}
	for _, raw := range []string{`{`, `{}`, `{"beat":"A"}`, `{"beat":1,"run":"r"}`} {
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		proc.script.ActionFile = path
		if actual, err := proc.actionEnvironment(base); err == nil {
			t.Fatalf("%s: malformed %q accepted as %v", path, raw, actual)
		}
	}
	proc.script.ActionFile = "relative.json"
	if _, err := proc.actionTrace(); err == nil {
		t.Fatal("relative action file accepted")
	}
}
