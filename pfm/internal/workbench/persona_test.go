package workbench

import (
	"path/filepath"
	"reflect"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestForLaunch(t *testing.T) {
	for _, test := range []struct {
		name    string
		engines []pfmengine.ID
		modes   []Mode
	}{
		{"New enabled", []pfmengine.ID{pfmengine.Claude}, []Mode{New}},
		{"New disabled engine", []pfmengine.ID{pfmengine.Codex}, []Mode{New}},
		{"Resume disabled engine", []pfmengine.ID{pfmengine.Codex}, []Mode{Resume}},
		{"invalid manifest", []pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.OpenCode}, []Mode{New, Resume}},
		{"outside a bench", []pfmengine.ID{pfmengine.Claude}, []Mode{New}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, dir := benchFixture(t)
			cwd, wantError := dir, ""
			want := Persona{}
			switch test.name {
			case "New enabled":
				want = Persona{
					Bench: LoadBench(dir, root), Prompt: filepath.Join(dir, ".professor", "scribe.md"),
					Body: "You are scribe.", Effort: "xhigh",
				}
			case "New disabled engine":
				wantError = "workbench " + dir + ` does not enable codex: add "codex" to "engines" in ` + paths.WorkbenchManifest(
					dir,
				)
			case "invalid manifest":
				writeBenchFile(t, paths.WorkbenchManifest(dir), `{"prompt":""}`)
				wantError = paths.WorkbenchManifest(dir) + `: "prompt" is required`
			case "outside a bench":
				cwd = filepath.Join(root, "src")
			}
			for _, engine := range test.engines {
				for _, mode := range test.modes {
					got, err := ForLaunch(cwd, engine, mode)
					if wantError != "" {
						if err == nil || err.Error() != wantError {
							t.Errorf("ForLaunch %s/%d = %v; want %q", engine, mode, err, wantError)
						}
						continue
					}
					if err != nil || !reflect.DeepEqual(got, want) || got.Applies() != want.Applies() {
						t.Fatalf("ForLaunch = %#v, %v; want %#v", got, err, want)
					}
					if test.name == "New enabled" {
						writeBenchFile(t, got.Prompt, "Edited prompt.")
						updated, err := ForLaunch(dir, pfmengine.Claude, New)
						if err != nil || updated.Body != "Edited prompt." {
							t.Fatalf("prompt edit = %#v, %v", updated, err)
						}
					}
				}
			}
		})
	}
}

func TestPersonaPrecedence(t *testing.T) {
	persona := Persona{Effort: "xhigh", Model: "bench-model"}
	if persona.EffortOr("high") != "high" || persona.EffortOr("") != "xhigh" ||
		persona.ModelOr(
			"explicit-model",
		) != "explicit-model" || persona.ModelOr("") != "bench-model" || (Persona{}).ModelOr("") != "" {
		t.Fatal("explicit > persona > empty precedence failed")
	}
}
