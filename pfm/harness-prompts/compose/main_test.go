package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	"github.com/rezzminator/professor/pfm/internal/codexgen"
)

func promptRoot(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(file))
}

func TestComposedPromptsMatchSources(t *testing.T) {
	root := promptRoot(t)
	for _, engine := range []string{"claude", "codex", "opencode"} {
		t.Run(engine, func(t *testing.T) {
			var want []byte
			var err error
			if engine == "codex" {
				var prompt string
				prompt, err = codexgen.FleetPrompt()
				want = []byte(prompt)
			} else {
				want, err = harnessprompts.Composed(engine)
			}
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "composed", engine+".md")
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("%s differs from parts at line %d", path, firstDifferentLine(got, want))
			}
		})
	}
}

func TestComposeWritesIdempotentlyAndNamesMissingPart(t *testing.T) {
	source, root := promptRoot(t), t.TempDir()
	for _, engine := range []string{"claude", "codex", "opencode"} {
		for _, part := range []string{"share/head.md", engine + "/professor.md", "share/tail.md"} {
			from := filepath.Join(source, filepath.FromSlash(part))
			content, err := os.ReadFile(from)
			if err != nil {
				t.Fatal(err)
			}
			to := filepath.Join(root, filepath.FromSlash(part))
			if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(to, content, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writeComposed(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "composed", "claude.md")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeComposed(root); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("second compose rewrote unchanged prompt")
	}
	missingRoot := t.TempDir()
	if err := writeComposed(missingRoot); err == nil || !strings.Contains(err.Error(), "share/head.md") {
		t.Fatalf("missing part error = %v", err)
	}
}

func TestFirstDifferentLineNamesAppendedLine(t *testing.T) {
	if got := firstDifferentLine([]byte("first"), []byte("first\nsecond")); got != 2 {
		t.Fatalf("first different line = %d, want 2", got)
	}
}
