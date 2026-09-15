package engine

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestNoEngineLiteralOutsideEnginePackage(t *testing.T) {
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate engine sweep source")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	allow := map[string]string{
		"cmd/pfm/engines.go": "(b) the composition root is the one permitted engine wiring roster",
	}
	literals := []string{
		"cc", "cx", "ox", "claude", "codex", "opencode",
		"cc-", "cx-", "ox-", "Claude", "Codex", "OpenCode",
	}
	quoted := make([]string, 0, len(literals))
	for _, literal := range literals {
		quoted = append(quoted, strconv.Quote(literal))
	}

	var hits []string
	files := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel == "internal/engine" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		if reason, ok := allow[rel]; ok {
			if strings.TrimSpace(reason) == "" {
				return fmt.Errorf("allow-list entry %s has no reason", rel)
			}
			return nil
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() {
			if err := file.Close(); err != nil {
				t.Errorf("close file: %v", err)
			}
		}()
		scanner := bufio.NewScanner(file)
		lineNumber := 0
		for scanner.Scan() {
			lineNumber++
			line := scanner.Text()
			for _, literal := range quoted {
				if strings.Contains(line, literal) {
					hits = append(hits, fmt.Sprintf("%s:%d: %s", rel, lineNumber, strings.TrimSpace(line)))
					files[rel] = struct{}{}
					break
				}
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatalf("sweep of %s failed — a failure to look, not a clean result: %v", root, err)
	}
	if len(hits) == 0 {
		return
	}
	sort.Strings(hits)
	for _, hit := range hits {
		t.Error(hit)
	}
	t.Fatalf("engine literal sweep: %d literals remaining in %d files", len(hits), len(files))
}
