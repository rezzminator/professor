package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestRuntimeReloadReadsTheFileAgain: a runtime loaded once sees the file as it
// is now — an edited claude.settings.env value — with its paths kept; a broken
// edit is the error beside the unchanged runtime, and a runtime loaded from no
// file is returned as it is.
func TestRuntimeReloadReadsTheFileAgain(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(t.TempDir(), FileName)
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"version":2,"claude":{"settings":{"env":{"FIXTURE_KNOB":"before"}}}}`)
	machine, err := Load(path, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := Runtime{Config: machine, Paths: paths.Values{Home: home, TmuxDir: "/jail/tmux"}}

	write(`{"version":2,"claude":{"settings":{"env":{"FIXTURE_KNOB":"after"}}}}`)
	fresh, err := runtime.Reload()
	if err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if got := fresh.Config.Claude.Settings["env"]; !reflect.DeepEqual(got, map[string]any{"FIXTURE_KNOB": "after"}) {
		t.Fatalf("reloaded claude.settings.env = %#v, want FIXTURE_KNOB=after (%s)", got, path)
	}
	if fresh.Paths.TmuxDir != "/jail/tmux" {
		t.Fatalf("reloaded TmuxDir = %q, want the runtime's own /jail/tmux", fresh.Paths.TmuxDir)
	}

	// The runtime's Codex roots are the homes the file named at the first
	// load; an edit dropping them reloads to the ambient default home.
	stale := t.TempDir()
	for _, codexHome := range []string{paths.CodexHome(home), stale} {
		if err := os.MkdirAll(codexHome, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(codexHome, "auth.json"),
			[]byte(`{"OPENAI_API_KEY":"fixture"}`),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	codexRuntime := Runtime{Config: machine, Paths: paths.Values{
		Home: home, Roots: map[pfmengine.ID][]string{pfmengine.Codex: {stale}},
	}}
	fromAmbient, err := codexRuntime.Reload()
	if want := []string{
		paths.CodexHome(home),
	}; err != nil ||
		!reflect.DeepEqual(fromAmbient.Config.CodexHomes(), want) ||
		!reflect.DeepEqual(fromAmbient.Paths.Roots[pfmengine.Codex], want) {
		t.Fatalf("Reload Codex homes = %v roots %v (%v), want %v", fromAmbient.Config.CodexHomes(),
			fromAmbient.Paths.Roots[pfmengine.Codex], err, want)
	}

	write(`{"version":2,"claude":{"systemPrompt":"verbose"}}`)
	kept, err := runtime.Reload()
	want := "config " + path + `: claude.systemPrompt must be "production", "lean" or "professor", got "verbose"`
	if err == nil || err.Error() != want || !reflect.DeepEqual(kept.Config.Claude.Settings, machine.Claude.Settings) {
		t.Fatalf("Reload after a broken edit = %v, want %q beside the unchanged runtime", err, want)
	}

	bare := Runtime{Paths: paths.Values{TmuxDir: "/jail/tmux"}}
	if same, err := bare.Reload(); err != nil || !reflect.DeepEqual(same, bare) {
		t.Fatalf("Reload of a runtime from no file = %+v, %v; want it unchanged", same, err)
	}
}
