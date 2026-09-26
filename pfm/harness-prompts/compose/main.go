// Package main is the harness-prompt composer: it writes the per-engine prompts
// under harness-prompts/composed from their shared and engine parts (`make prompts`).
package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/codexgen"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func main() {
	if err := writeComposed("harness-prompts"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func writeComposed(root string) error {
	for _, id := range pfmengine.All() {
		engine := pfmengine.MustLookup(id).LongName
		for _, part := range []string{"share/head.md", engine + "/professor.md", "share/tail.md"} {
			if _, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(part))); err != nil {
				return fmt.Errorf("read harness prompt part %s: %w", part, err)
			}
		}
		var content []byte
		var err error
		if id == pfmengine.Codex {
			var prompt string
			prompt, err = codexgen.FleetPrompt()
			content = []byte(prompt)
		} else {
			content, err = harnessprompts.Composed(engine)
		}
		if err != nil {
			return fmt.Errorf("compose %s prompt: %w", engine, err)
		}
		target := filepath.Join(root, "composed", engine+".md")
		current, err := os.ReadFile(target)
		if err == nil && bytes.Equal(current, content) {
			continue
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read %s: %w", target, err)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create composed prompt directory: %w", err)
		}
		if err := atomicfile.Write(target, content, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
	}
	return nil
}

func firstDifferentLine(left, right []byte) int {
	a, b := bytes.Split(left, []byte("\n")), bytes.Split(right, []byte("\n"))
	for i := 0; i < len(a) && i < len(b); i++ {
		if !bytes.Equal(a[i], b[i]) {
			return i + 1
		}
	}
	if len(a) < len(b) {
		return len(a) + 1
	}
	return len(b) + 1
}
