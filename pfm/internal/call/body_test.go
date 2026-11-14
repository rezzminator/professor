package call

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

func TestPersistBodyPrunesExpiredMarkdownByAge(t *testing.T) {
	engine := newTestEngine(t, "cc-body-prune", &fakeTmux{capture: "❯ "})
	now := time.Date(2031, 2, 3, 4, 5, 6, 7, time.UTC)
	engine.options.Clock = fixedClock{Clock: clock.Real, now: now}
	engine.options.BodyMaxAge = 24 * time.Hour
	if err := os.MkdirAll(engine.options.BodyRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(engine.options.BodyRoot, "stale.md")
	fresh := filepath.Join(engine.options.BodyRoot, "fresh.md")
	nonBody := filepath.Join(engine.options.BodyRoot, "stale.txt")
	for _, path := range []string{stale, fresh, nonBody} {
		if err := os.WriteFile(path, []byte(path), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(-48 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(nonBody, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(fresh, now, now); err != nil {
		t.Fatal(err)
	}
	path, warnings, err := engine.persistBody("exact body", "cc-1:%2")
	if err != nil || len(warnings) != 0 {
		t.Fatalf("persist body path=%q warnings=%q err=%v", path, warnings, err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("expired markdown survived: %v", err)
	}
	for _, kept := range []string{fresh, nonBody} {
		if _, err := os.Stat(kept); err != nil {
			t.Fatalf("non-expired/non-body file %q was pruned: %v", kept, err)
		}
	}
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != "exact body" ||
		filepath.Base(path) != "20310203T040506.000000007Z-cc-1--2.md" {
		t.Fatalf("canonical file path=%q body=%q err=%v", path, stored, err)
	}
}

func TestPrepareForResumeDeliversInlineWhenTheBodyCannotBeSaved(t *testing.T) {
	engine := newTestEngine(t, "cc-resume-unsaved", &fakeTmux{capture: "❯ "})
	blocker := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	engine.options.BodyRoot = filepath.Join(blocker, "bodies")
	body := "resume caption\n" + strings.Repeat("r", 8<<10)
	prepared, err := engine.PrepareForResume(
		context.Background(),
		"11111111-2222-4333-8444-555555555555",
		"cc",
		body,
	)
	want := `long body not saved to a file (create call body directory "` + engine.options.BodyRoot +
		`": mkdir ` + blocker + `: not a directory) — delivered inline`
	got := strings.Replace(prepared.Message, body, "{body}", 1)
	if err != nil || prepared.AutoFilePath != "" ||
		got != "{body}\n\n— sid 12345678 · to reply: call Operator <message>" ||
		len(prepared.Warnings) != 1 ||
		prepared.Warnings[0] != want {
		t.Fatalf(
			"message = %q, want %q (err %v file %q warnings %q, want warning %q)",
			got,
			"{body}\n\n— sid 12345678 · to reply: call Operator <message>",
			err,
			prepared.AutoFilePath,
			prepared.Warnings,
			want,
		)
	}
}

func TestPruneBodiesWarnsWhenTheDirectoryCannotBeListed(t *testing.T) {
	engine := newTestEngine(t, "cc-body-unlisted", &fakeTmux{capture: "❯ "})
	notDir := filepath.Join(t.TempDir(), "bodies")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	want := `enumerate call bodies in "` + notDir + `": open ` + notDir + `: not a directory — nothing pruned`
	if got := engine.pruneBodies(notDir); len(got) != 1 || got[0] != want {
		t.Fatalf("warnings=%q, want [%q]", got, want)
	}
}
