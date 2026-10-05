package agentrole

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func roleWorkbenchFixture(t *testing.T) (string, string, string) {
	t.Helper()
	root, home := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "docs", "scribe")
	mustWrite(t, filepath.Join(root, ".professor", "baseline.json"), "{}")
	mustWrite(
		t,
		filepath.Join(dir, ".professor", "workbench.json"),
		`{"prompt":"scribe.md","title":"Scribe","name":"_SCRIBE","effort":"xhigh"}`,
	)
	mustWrite(t, filepath.Join(dir, ".professor", "scribe.md"), "You are scribe.")
	mustWrite(t, filepath.Join(root, ".claude", "agents", "r.md"), "ROLE R")
	mustWrite(t, filepath.Join(root, ".codex", "agents", "r.toml"), `developer_instructions = "ROLE R"`)
	if err := paths.WriteSourceRepoMarker(home, root); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(root, "pfm", "harness-prompts", "composed", "claude.md"), "FLEET")
	return root, dir, home
}

func TestBasePrompt(t *testing.T) {
	for _, name := range []string{"workbench Claude", "workbench Codex", "disabled", "invalid", "outside Claude", "outside Codex"} {
		t.Run(name, func(t *testing.T) {
			root, dir, home := roleWorkbenchFixture(t)
			engine, want := pfmengine.Claude, "You are scribe."
			if strings.Contains(name, "Codex") || name == "disabled" {
				engine = pfmengine.Codex
			}
			if name == "workbench Codex" {
				mustWrite(t, paths.WorkbenchManifest(dir), `{"prompt":"scribe.md","engines":["codex","claude"]}`)
			}
			if strings.HasPrefix(name, "outside") {
				dir = root
				want = "FLEET"
			}
			if name == "disabled" || name == "outside Codex" {
				var err error
				want, err = codexgen.FleetPrompt()
				if err != nil {
					t.Fatal(err)
				}
			}
			if name == "invalid" {
				mustWrite(t, paths.WorkbenchManifest(dir), `{"prompt":""}`)
			}
			got, err := BasePrompt(engine, dir, home)
			if name == "invalid" {
				if err == nil || err.Error() != paths.WorkbenchManifest(dir)+`: "prompt" is required` {
					t.Fatalf("invalid base = %q, %v", got, err)
				}
				return
			}
			if err != nil || got != want {
				t.Fatalf("base = %q, %v; want %q", got, err, want)
			}
		})
	}
}
