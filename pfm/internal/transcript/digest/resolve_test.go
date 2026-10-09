package digest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestResolvePicksOneTranscriptOrSaysWhy(t *testing.T) {
	root := t.TempDir()
	realFile := filepath.Join(root, "realFile.jsonl")
	if err := os.WriteFile(realFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.jsonl")
	if err := os.Symlink(realFile, link); err != nil {
		t.Fatal(err)
	}
	resolved := resolvedPath(realFile)
	if got := uniqueResolved([]string{link, realFile}); !slices.Equal(got, []string{resolved}) {
		t.Errorf("uniqueResolved = %q, want the one realFile file", got)
	}
	finder := &resolver{env: Env{ClaudeRoots: []string{root, filepath.Join(root, "absent")}}}
	if got, err := finder.pick("realFile", []string{link, realFile}, nil); err != nil || got != resolved {
		t.Errorf("pick of one file by two paths = %q, %v", got, err)
	}
	other := filepath.Join(root, "other.jsonl")
	if err := os.WriteFile(other, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := finder.pick("r", []string{realFile, other}, nil)
	var failure *Failure
	if !errors.As(err, &failure) || failure.Usage ||
		!strings.HasPrefix(failure.Message, "AMBIGUOUS r — 2 transcripts: ") {
		t.Errorf("pick of two files = %v", err)
	}
	_, err = finder.pick("gone", nil, []string{"x.jsonl (denied)"})
	if !errors.As(err, &failure) ||
		failure.Message != "NOT FOUND gone — searched "+root+"; 1 rollouts unreadable: x.jsonl (denied)" {
		t.Errorf("pick of nothing = %v", err)
	}
}

// TestNameLookupStopsOnceTheContextIsDone pins chat_digest's deadline over a
// chat-title source: the name walk reads the tail of every session file under
// the roots, so a done context ends it, never a found transcript.
func TestNameLookupStopsOnceTheContextIsDone(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "-work-repo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(project, "aaaaaaaa-1111-2222-3333-444444444444.jsonl")
	if err := os.WriteFile(
		session,
		[]byte(`{"type":"custom-title","customTitle":"wanted","sessionId":"x"}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	env := Env{ClaudeRoots: []string{root}}
	if out, err := DigestTranscript(context.Background(), []string{"locate", "wanted"}, env); err != nil ||
		out.Text != resolvedPath(session)+"\n" {
		t.Fatalf("locate by title = %q, %v; want the session", out.Text, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out, err := DigestTranscript(ctx, []string{"locate", "wanted"}, env); !errors.Is(err, context.Canceled) {
		t.Fatalf("locate by title under a done context = %q, %v; want context.Canceled", out.Text, err)
	}
}

// TestNameLookupIsNotFailedByAnUnreadableCodexHome: a Claude chat named by its
// title resolves even when the id search could not walk a Codex home — the
// name lookup reads no Codex home; the place the id search missed is a warning.
func TestNameLookupIsNotFailedByAnUnreadableCodexHome(t *testing.T) {
	root := t.TempDir()
	project := filepath.Join(root, "-work-repo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	session := filepath.Join(project, "aaaaaaaa-1111-2222-3333-444444444444.jsonl")
	if err := os.WriteFile(
		session,
		[]byte(`{"type":"custom-title","customTitle":"night-shift","sessionId":"x"}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	notADir := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(notADir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	env := Env{ClaudeRoots: []string{root}, CodexHomes: []string{filepath.Join(notADir, "codex")}}
	out, err := DigestTranscript(context.Background(), []string{"locate", "night-shift"}, env)
	if err != nil || out.Text != resolvedPath(session)+"\n" {
		t.Fatalf("locate by title beside an unwalkable Codex home = %q, %v; want the session", out.Text, err)
	}
	if len(out.Warnings) != 1 ||
		!strings.HasPrefix(out.Warnings[0], warningPrefix+"2 places unreadable while resolving night-shift: ") {
		t.Errorf("warnings = %q, want the two places the id search missed", out.Warnings)
	}
	_, err = DigestTranscript(context.Background(), []string{"locate", "no-such-chat"}, env)
	var failure *Failure
	if !errors.As(err, &failure) || !strings.HasPrefix(failure.Message, "NOT FOUND no-such-chat — ") ||
		!strings.Contains(failure.Message, "; 2 places unreadable while resolving: ") {
		t.Errorf("an unknown name beside an unwalkable Codex home = %v, want NOT FOUND naming the missed place", err)
	}
}
