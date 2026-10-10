package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/archive"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestArchiveCommandFailsVisiblyOnIncompleteArchive(t *testing.T) {
	for _, failure := range []string{"occupied target", "manifest unavailable", "unresolved apply", "unresolved preview", "sidecar backup"} {
		t.Run(failure, func(t *testing.T) {
			jailTest(t)
			values := jailPaths(t)
			const id = "11111111-1111-4111-8111-111111111111"
			source := filepath.Join(values.Roots[pfmengine.Claude][0], "-p", id+".jsonl")
			if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			switch failure {
			case "occupied target":
				target := filepath.Join(
					values.ArchiveDir,
					pfmengine.MustLookup(pfmengine.Claude).LongName,
					"-p",
					id+".jsonl",
				)
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte("existing\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "manifest unavailable":
				if err := os.MkdirAll(archive.ManifestPath(values.ArchiveDir), 0o700); err != nil {
					t.Fatal(err)
				}
			case "sidecar backup":
				if err := os.RemoveAll(filepath.Join(values.Home, ".claude")); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(filepath.Join(values.Home, ".claude", "history.jsonl"), 0o700); err != nil {
					t.Fatal(err)
				}
			default:
				if err := os.Remove(source); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Base(source), source); err != nil {
					t.Fatal(err)
				}
			}
			database, err := store.Open()
			if err != nil {
				t.Fatal(err)
			}
			if err := database.Kill(
				context.Background(),
				store.Killed{ID: id, Engine: pfmengine.Claude, KilledAt: 42},
			); err != nil {
				t.Fatal(err)
			}
			if err := database.Close(); err != nil {
				t.Fatal(err)
			}
			args := []string{"archive", "--apply"}
			if failure == "unresolved preview" {
				args = []string{"archive"}
			}
			var stdout, stderr bytes.Buffer
			code := run(args, &stdout, &stderr)
			if code != 1 {
				t.Errorf("incomplete archive exit=%d, want 1; stdout=%s stderr=%s", code, &stdout, &stderr)
			}
			switch {
			case strings.HasPrefix(failure, "unresolved"):
				if !strings.Contains(stdout.String(), id) || !strings.Contains(stdout.String(), "symbolic links") {
					t.Errorf("unresolved cause not printed: stdout=%s stderr=%s", &stdout, &stderr)
				}
			case failure == "sidecar backup":
				if strings.Contains(stdout.String(), "moved  ") || !strings.Contains(stdout.String(), "plan   ") {
					t.Errorf("unperformed partial plan printed as moved: %s", &stdout)
				}
				if _, err := os.Stat(source); err != nil {
					t.Fatalf("backup failure lost original transcript: %v", err)
				}
			case !strings.Contains(stdout.String(), "FAILED"):
				t.Errorf("failed move report missing: stdout=%s stderr=%s", &stdout, &stderr)
			}
		})
	}
}
