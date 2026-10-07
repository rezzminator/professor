package workbench

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexgen"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/opencodegen"
)

func TestEnsureMirror(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Codex, pfmengine.OpenCode, pfmengine.Claude} {
		t.Run(string(engine), func(t *testing.T) {
			root, dir := benchFixture(t)
			writeBenchFile(t, filepath.Join(dir, "CLAUDE.md"), "Scribe.\n")
			bench, home := LoadBench(dir, root), t.TempDir()
			if err := EnsureMirror(bench, engine, home); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, "AGENTS.md")
			if engine == pfmengine.OpenCode {
				output = filepath.Join(dir, ".opencode", "opencode.jsonc")
			}
			if engine == pfmengine.Claude {
				for _, path := range []string{output, filepath.Join(dir, ".codex"), filepath.Join(dir, ".opencode")} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Errorf("claude mirror output %s: %v", path, err)
					}
				}
			} else if _, err := os.Stat(output); err != nil {
				t.Errorf("mirror missing: %v", err)
			}
		})
	}
}

func TestEnsureMirrorFails(t *testing.T) {
	for _, tc := range []struct {
		engine pfmengine.ID
		config string
		body   string
		word   string
	}{
		{pfmengine.Codex, ".claude/codex-build.json", "{", "codex"},
		{pfmengine.Codex, ".mcp.json", "{", "codex"},
		{pfmengine.OpenCode, ".claude/opencode-build.json", "{", "opencode"},
	} {
		t.Run(tc.config, func(t *testing.T) {
			root, dir := benchFixture(t)
			writeBenchFile(t, filepath.Join(dir, "CLAUDE.md"), "Scribe.\n")
			writeBenchFile(t, filepath.Join(dir, tc.config), tc.body)
			bench, home := LoadBench(dir, root), t.TempDir()
			var problem string
			if tc.engine == pfmengine.Codex {
				result, err := codexgen.Build(codexgen.Options{Root: dir, Home: home})
				if err != nil {
					problem = err.Error()
				} else {
					problem = strings.Join(result.Problems, "; ")
				}
			} else {
				result, err := opencodegen.Compile(
					opencodegen.Options{Root: dir, Home: home, Mode: opencodegen.ModeBuild},
				)
				if err != nil {
					problem = err.Error()
				} else {
					problem = strings.Join(result.Problems, "; ")
				}
			}
			if problem == "" {
				t.Fatal("fixture did not fail")
			}
			want := "build the " + tc.word + " mirror of workbench " + dir + ": " + problem
			if err := EnsureMirror(bench, tc.engine, home); err == nil || err.Error() != want {
				t.Fatalf("ensure=%v, want %q", err, want)
			}
		})
	}
}

func TestCheckMirror(t *testing.T) {
	for _, engine := range []pfmengine.ID{pfmengine.Codex, pfmengine.OpenCode} {
		t.Run(string(engine), func(t *testing.T) {
			root, dir := benchFixture(t)
			writeBenchFile(t, filepath.Join(dir, "CLAUDE.md"), "Scribe.\n")
			writeBenchFile(
				t,
				filepath.Join(dir, ".claude", "commands", "local.md"),
				"---\ndescription: Local.\n---\nLocal.\n",
			)
			bench, home := LoadBench(dir, root), t.TempDir()
			// Build directly: the check row must fail independently of EnsureMirror's stub.
			if engine == pfmengine.Codex {
				result, err := codexgen.Build(codexgen.Options{Root: dir, Home: home})
				if err != nil || !result.OK {
					t.Fatalf("build=%#v err=%v", result, err)
				}
			} else {
				result, err := opencodegen.Compile(
					opencodegen.Options{Root: dir, Home: home, Mode: opencodegen.ModeBuild},
				)
				if err != nil || !result.OK {
					t.Fatalf("build=%#v err=%v", result, err)
				}
			}
			if check, err := CheckMirror(
				bench,
				engine,
				home,
			); len(check.Rebuildable) != 0 || len(check.Failing) != 0 ||
				err != nil {
				t.Errorf("fresh=%#v err=%v", check, err)
			}
			writeBenchFile(t, filepath.Join(dir, "CLAUDE.md"), "Edited scribe.\n")
			writeBenchFile(
				t,
				filepath.Join(dir, ".claude", "commands", "local.md"),
				"---\ndescription: Local.\n---\nEdited.\n",
			)
			var problems []string
			if engine == pfmengine.Codex {
				result, err := codexgen.Check(codexgen.Options{Root: dir, Home: home})
				if err != nil {
					t.Fatal(err)
				}
				problems = result.Problems
			} else {
				result, err := opencodegen.Compile(
					opencodegen.Options{Root: dir, Home: home, Mode: opencodegen.ModeCheck},
				)
				if err != nil {
					t.Fatal(err)
				}
				problems = result.Problems
			}
			if check, err := CheckMirror(
				bench,
				engine,
				home,
			); !reflect.DeepEqual(check.Rebuildable, problems) || len(check.Failing) != 0 ||
				err != nil {
				t.Errorf("stale=%#v err=%v, want %q", check, err, problems)
			}
			config := "codex-build.json"
			if engine == pfmengine.OpenCode {
				config = "opencode-build.json"
			}
			writeBenchFile(t, filepath.Join(dir, ".claude", config), "{")
			check, err := CheckMirror(bench, engine, home)
			if engine == pfmengine.Codex {
				_, want := codexgen.Check(codexgen.Options{Root: dir, Home: home})
				if len(check.Rebuildable) != 0 || len(check.Failing) != 0 || err == nil || err.Error() != want.Error() {
					t.Errorf("broken=%#v err=%v, want %v", check, err, want)
				}
			} else if len(check.Failing) == 0 || len(check.Rebuildable) != 0 || err != nil {
				t.Errorf("OpenCode config problem is returned in Result: check=%#v err=%v", check, err)
			}
		})
	}
}

func TestCheckMirrorProblemSplit(t *testing.T) {
	for _, scenario := range []string{"Codex orphan", "OpenCode mixed", "OpenCode config"} {
		t.Run(scenario, func(t *testing.T) {
			root, dir := benchFixture(t)
			writeBenchFile(t, filepath.Join(dir, "CLAUDE.md"), "Scribe.\n")
			bench, home := LoadBench(dir, root), t.TempDir()
			engine := pfmengine.OpenCode
			want := MirrorCheck{}
			switch scenario {
			case "Codex orphan":
				engine = pfmengine.Codex
				source := filepath.Join(dir, ".claude", "agents", "old.md")
				writeBenchFile(t, source, "---\nname: old\ndescription: Old.\n---\nOld.\n")
				result, err := codexgen.Build(codexgen.Options{Root: dir, Home: home})
				if err != nil || !result.OK {
					t.Fatalf("build=%#v err=%v", result, err)
				}
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				want.Rebuildable = []string{"ORPHAN " + filepath.Join(dir, ".codex", "agents", "old.toml")}
			case "OpenCode mixed":
				source := filepath.Join(dir, ".claude", "commands", "local.md")
				writeBenchFile(t, source, "---\ndescription: Local.\n---\nLocal.\n")
				result, err := opencodegen.Compile(
					opencodegen.Options{Root: dir, Home: home, Mode: opencodegen.ModeBuild},
				)
				if err != nil || !result.OK {
					t.Fatalf("build=%#v err=%v", result, err)
				}
				writeBenchFile(t, source, "---\ndescription: Local.\n---\nEdited.\n")
				writeBenchFile(
					t,
					filepath.Join(dir, ".claude", "commands", "hand.md"),
					"---\ndescription: Hand.\n---\nHand.\n",
				)
				writeBenchFile(t, filepath.Join(dir, ".opencode", "command", "hand.md"), "hand\n")
				want.Rebuildable = []string{"STALE " + filepath.Join(dir, ".opencode", "command", "local.md")}
				want.Failing = []string{
					"CONFLICT " + filepath.Join(
						dir,
						".opencode",
						"command",
						"hand.md",
					) + " — exists without a generated marker; not touching it",
				}
			case "OpenCode config":
				writeBenchFile(t, filepath.Join(dir, ".claude", "opencode-build.json"), "{")
				result, err := opencodegen.Compile(
					opencodegen.Options{Root: dir, Home: home, Mode: opencodegen.ModeCheck},
				)
				if err != nil || len(result.Problems) == 0 {
					t.Fatalf("config=%#v err=%v", result, err)
				}
				want.Failing = result.Problems
			}
			got, err := CheckMirror(bench, engine, home)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("check=%#v err=%v, want %#v", got, err, want)
			}
		})
	}
}
