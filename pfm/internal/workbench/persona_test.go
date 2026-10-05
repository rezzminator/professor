package workbench

import (
	"path/filepath"
	"reflect"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestForLaunchNewPersona(t *testing.T) {
	root, dir := benchFixture(t)
	got, err := ForLaunch(dir, pfmengine.Claude, New)
	want := Persona{
		Bench:  LoadBench(dir, root),
		Prompt: filepath.Join(dir, ".professor", "scribe.md"),
		Body:   "You are scribe.",
		Effort: "xhigh",
	}
	if err != nil || !reflect.DeepEqual(got, want) || !got.Applies() {
		t.Fatalf("ForLaunch = %#v, %v; want %#v", got, err, want)
	}
	writeBenchFile(t, got.Prompt, "Edited prompt.")
	updated, err := ForLaunch(dir, pfmengine.Claude, New)
	if err != nil || updated.Body != "Edited prompt." {
		t.Fatalf("prompt edit = %#v, %v", updated, err)
	}
}

func TestForLaunchDisabledNew(t *testing.T) {
	_, dir := benchFixture(t)
	_, err := ForLaunch(dir, pfmengine.Codex, New)
	want := "workbench " + dir + ` does not enable codex: add "codex" to "engines" in ` + paths.WorkbenchManifest(dir)
	if err == nil || err.Error() != want {
		t.Fatalf("disabled new = %v; want %q", err, want)
	}
}

func TestForLaunchDisabledResume(t *testing.T) {
	_, dir := benchFixture(t)
	control, err := ForLaunch(dir, pfmengine.Claude, Resume)
	if err != nil || !control.Applies() {
		t.Fatalf("enabled resume = %#v, %v", control, err)
	}
	got, err := ForLaunch(dir, pfmengine.Codex, Resume)
	if err != nil || !reflect.DeepEqual(got, Persona{}) {
		t.Fatalf("disabled resume = %#v, %v", got, err)
	}
}

func TestForLaunchInvalidManifest(t *testing.T) {
	_, dir := benchFixture(t)
	writeBenchFile(t, paths.WorkbenchManifest(dir), `{"prompt":""}`)
	for _, engine := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex, pfmengine.OpenCode} {
		for _, mode := range []Mode{New, Resume} {
			_, err := ForLaunch(dir, engine, mode)
			want := paths.WorkbenchManifest(dir) + `: "prompt" is required`
			if err == nil || err.Error() != want {
				t.Errorf("invalid %s/%d = %v; want %q", engine, mode, err, want)
			}
		}
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

func TestForLaunchOutside(t *testing.T) {
	root, dir := benchFixture(t)
	control, err := ForLaunch(dir, pfmengine.Claude, New)
	if err != nil || !control.Applies() {
		t.Fatalf("inside control = %#v, %v", control, err)
	}
	got, err := ForLaunch(filepath.Join(root, "src"), pfmengine.Claude, New)
	if err != nil || !reflect.DeepEqual(got, Persona{}) {
		t.Fatalf("outside = %#v, %v", got, err)
	}
}
