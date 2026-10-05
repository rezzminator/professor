package workbench

import (
	"os"
	"path/filepath"
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
			if stale, err := CheckMirror(bench, engine, home); stale != "" || err != nil {
				t.Errorf("fresh=%q err=%v", stale, err)
			}
			writeBenchFile(t, filepath.Join(dir, "CLAUDE.md"), "Edited scribe.\n")
			writeBenchFile(
				t,
				filepath.Join(dir, ".claude", "commands", "local.md"),
				"---\ndescription: Local.\n---\nEdited.\n",
			)
			var first string
			if engine == pfmengine.Codex {
				result, err := codexgen.Check(codexgen.Options{Root: dir, Home: home})
				if err != nil {
					t.Fatal(err)
				}
				first = result.Problems[0]
			} else {
				result, err := opencodegen.Compile(
					opencodegen.Options{Root: dir, Home: home, Mode: opencodegen.ModeCheck},
				)
				if err != nil {
					t.Fatal(err)
				}
				first = result.Problems[0]
			}
			if stale, err := CheckMirror(bench, engine, home); stale != first || err != nil {
				t.Errorf("stale=%q err=%v, want %q", stale, err, first)
			}
			config := "codex-build.json"
			if engine == pfmengine.OpenCode {
				config = "opencode-build.json"
			}
			writeBenchFile(t, filepath.Join(dir, ".claude", config), "{")
			stale, err := CheckMirror(bench, engine, home)
			if engine == pfmengine.Codex {
				_, want := codexgen.Check(codexgen.Options{Root: dir, Home: home})
				if stale != "" || err == nil || err.Error() != want.Error() {
					t.Errorf("broken=%q err=%v, want %v", stale, err, want)
				}
			} else if stale == "" || err != nil {
				t.Errorf("OpenCode config problem is returned in Result: stale=%q err=%v", stale, err)
			}
		})
	}
}
