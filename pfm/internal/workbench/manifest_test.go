package workbench

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/professor"
)

const exampleManifest = `{"prompt":"scribe.md","title":"Scribe","name":"_SCRIBE","effort":"xhigh"}`

func benchFixture(t *testing.T) (string, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "acme")
	writeBenchFile(t, professor.BaselinePath(root), "{}")
	dir := filepath.Join(root, "docs", "scribe")
	seedBench(t, dir, exampleManifest)
	return root, dir
}

func writeBenchFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedBench(t *testing.T, dir, manifest string) {
	t.Helper()
	writeBenchFile(t, paths.WorkbenchManifest(dir), manifest)
	writeBenchFile(t, filepath.Join(dir, ".professor", "scribe.md"), "You are scribe.")
}

func TestLoadFullManifest(t *testing.T) {
	root, dir := benchFixture(t)
	got := LoadBench(dir, root)
	want := Bench{
		Dir: dir, Root: root, Project: "acme", Title: "Scribe", Prefix: "_SCRIBE",
		Key: "acme › Scribe", Prompt: filepath.Join(dir, ".professor", "scribe.md"),
		Engines: []pfmengine.ID{pfmengine.Claude}, Effort: "xhigh",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LoadBench() = %#v, want %#v", got, want)
	}
}

func TestLoadDefaults(t *testing.T) {
	root, _ := benchFixture(t)
	dir := filepath.Join(root, "tools", "lab")
	seedBench(t, dir, `{"prompt":"p.md"}`)
	writeBenchFile(t, filepath.Join(dir, ".professor", "p.md"), "Lab prompt.")
	got := LoadBench(dir, root)
	if got.Err != nil || got.Title != "lab" || got.Prefix != "LAB" ||
		got.Key != "acme › lab" || !reflect.DeepEqual(got.Engines, []pfmengine.ID{pfmengine.Claude}) {
		t.Fatalf("defaults = %#v", got)
	}
}

func TestLoadFaults(t *testing.T) {
	for _, tc := range []struct{ name, manifest, fault string }{
		{"no prompt", `{}`, `"prompt" is required`},
		{"empty prompt", `{"prompt":""}`, `"prompt" is required`},
		{"absolute prompt", `{"prompt":"/work/acme/p.md"}`, `"prompt" must name a file inside .professor/ (got "/work/acme/p.md")`},
		{"escaping prompt", `{"prompt":"../p.md"}`, `"prompt" must name a file inside .professor/ (got "../p.md")`},
		{"missing prompt", `{"prompt":"missing.md"}`, "missing"},
		{"directory prompt", `{"prompt":"folder"}`, "directory"},
		{"empty prompt file", `{"prompt":"empty.md"}`, "empty"},
		{"empty engines", `{"prompt":"scribe.md","engines":[]}`, `"engines" is empty; omit it for ["claude"]`},
		{"unknown engine", `{"prompt":"scribe.md","engines":["other"]}`, `"engines" names unknown engine "other"; allowed: claude, codex, opencode`},
		{"engine alias", `{"prompt":"scribe.md","engines":["cc"]}`, `"engines" names unknown engine "cc"; allowed: claude, codex, opencode`},
		{"repeated engine", `{"prompt":"scribe.md","engines":["claude","claude"]}`, `"engines" lists "claude" twice`},
		{"empty title", `{"prompt":"scribe.md","title":""}`, `"title" is empty`},
		{"space in name", `{"prompt":"scribe.md","name":"two words"}`, `"name" must be one word without ":" (got "two words")`},
		{"colon in name", `{"prompt":"scribe.md","name":"ONE:2"}`, `"name" must be one word without ":" (got "ONE:2")`},
		{"empty name", `{"prompt":"scribe.md","name":""}`, `"name" must be one word without ":" (got "")`},
		{"unknown field", `{"prompt":"scribe.md","extra":true}`, `not valid: json: unknown field "extra"`},
		{"mistyped field", `{"prompt":42}`, `not valid: json: cannot unmarshal number into Go struct field manifest.prompt of type string`},
		{"JSON", `{"prompt":`, `not valid: unexpected EOF`},
		{"trailing value", `{"prompt":"scribe.md"}{"title":"Other"}`, `not valid: trailing JSON value`},
		{"trailing brace", `{"prompt":"scribe.md"}}`, `not valid: invalid character '}' looking for beginning of value`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, dir := benchFixture(t)
			writeBenchFile(t, paths.WorkbenchManifest(dir), tc.manifest)
			fault := tc.fault
			switch fault {
			case "missing":
				fault = "prompt file " + filepath.Join(dir, ".professor", "missing.md") + " is missing"
			case "directory":
				path := filepath.Join(dir, ".professor", "folder")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				fault = "prompt file " + path + " is a directory"
			case "empty":
				path := filepath.Join(dir, ".professor", "empty.md")
				writeBenchFile(t, path, "")
				fault = "prompt file " + path + " is empty"
			}
			got := LoadBench(dir, root)
			want := paths.WorkbenchManifest(dir) + ": " + fault
			if got.Err == nil || got.Err.Error() != want || got.Key != "acme › scribe" ||
				got.Dir != dir || got.Root != root || got.Project != "acme" || got.Title != "scribe" {
				t.Fatalf("fault = %#v, want %q and retained identity", got, want)
			}
		})
	}
	for _, unreadable := range []bool{false, true} {
		t.Run(map[bool]string{false: "gone", true: "unreadable"}[unreadable], func(t *testing.T) {
			root, dir := benchFixture(t)
			path := paths.WorkbenchManifest(dir)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			if unreadable {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			_, readErr := os.ReadFile(path)
			got := LoadBench(dir, root)
			want := path + ": read: " + readErr.Error()
			if got.Err == nil || got.Err.Error() != want || got.Key != "acme › scribe" ||
				!errors.Is(got.Err, readErr.(*os.PathError).Err) {
				t.Fatalf("read fault = %#v, want %q", got, want)
			}
		})
	}
}

func TestBenchEnables(t *testing.T) {
	root, dir := benchFixture(t)
	writeBenchFile(t, paths.WorkbenchManifest(dir), `{"prompt":"scribe.md","engines":["codex","claude"]}`)
	got := LoadBench(dir, root)
	if got.Err != nil || !got.Enables(pfmengine.Codex) || got.Enables(pfmengine.OpenCode) ||
		!reflect.DeepEqual(got.Engines, []pfmengine.ID{pfmengine.Codex, pfmengine.Claude}) {
		t.Fatalf("Enables/order = %#v", got)
	}
}
