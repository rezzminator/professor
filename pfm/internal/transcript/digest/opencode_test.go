package digest

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// invokeOpenCode runs args over an env whose only root is OpenCode's root.
func invokeOpenCode(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Invoke(context.Background(), args, &stdout, &stderr, Env{OpenCodeRoots: []string{root}, Home: t.TempDir()})
	return code, stdout.String(), stderr.String()
}

// A session named in a store that does not exist is a failure naming the
// missing store, never an empty transcript.
func TestOpenCodeMissingStoreIsAFailure(t *testing.T) {
	root := filepath.Join(t.TempDir(), "opencode")
	store := filepath.Join(root, "opencode.db")
	want := "TRANSCRIPT FAILED — cannot read " + store + "#ses_1a2b3c4d5e6fGarden0001: no opencode store at " + store
	for _, verb := range []string{"show", "locate"} {
		code, stdout, stderr := invokeOpenCode(t, root, verb, store+"#ses_1a2b3c4d5e6fGarden0001")
		if code != 1 || stdout != "" || strings.TrimSpace(stderr) != want {
			t.Errorf("%s: exit %d stdout %q stderr %q, want 1, nothing, %q", verb, code, stdout, stderr, want)
		}
	}
	seeded := filepath.Join(t.TempDir(), "opencode")
	seedOpenCodeStore(t, seeded, "garden")
	locator := filepath.Join(seeded, "opencode.db") + "#ses_typo"
	code, stdout, stderr := invokeOpenCode(t, seeded, "locate", locator)
	want = "TRANSCRIPT FAILED — cannot read " + locator + ": no such opencode session ses_typo in " +
		filepath.Join(seeded, "opencode.db")
	if code != 1 || stdout != "" || strings.TrimSpace(stderr) != want {
		t.Errorf("locate an unknown session: exit %d stdout %q stderr %q, want 1, nothing, %q", code, stdout, stderr,
			want)
	}
}

// A Claude id resolves cleanly beside an unreadable OpenCode store: no
// OpenCode session id is shaped like it, so the store is no place it could be.
func TestClaudeIDResolvesBesideAnUnreadableOpenCodeStore(t *testing.T) {
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, "opencode.db"), []byte("not a database\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	claude := filepath.Join(t.TempDir(), "projects")
	chat := filepath.Join(claude, "-work-repo", "11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.MkdirAll(filepath.Dir(chat), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(chat, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Invoke(context.Background(), []string{"locate", "11111111-2222"}, &stdout, &stderr,
		Env{ClaudeRoots: []string{claude}, OpenCodeRoots: []string{broken}, Home: t.TempDir()})
	if code != 0 || strings.TrimSpace(stdout.String()) != resolvedPath(chat) || stderr.String() != "" {
		t.Errorf("exit %d stdout %q stderr %q, want 0, %s, nothing", code, stdout.String(), stderr.String(),
			resolvedPath(chat))
	}
}

// A store that cannot be read makes the lookup INCOMPLETE and names the store:
// an error, never NOT FOUND and never an empty transcript.
func TestOpenCodeUnreadableStoreFailsTheLookup(t *testing.T) {
	root := t.TempDir()
	store := filepath.Join(root, "opencode.db")
	if err := os.WriteFile(store, []byte("not a database\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"show", "agents", "locate"} {
		code, stdout, stderr := invokeOpenCode(t, root, verb, "ses_1a2b3c4d5e6f")
		if code != 1 || stdout != "" ||
			!strings.HasPrefix(stderr, "TRANSCRIPT FAILED — INCOMPLETE name lookup for 'ses_1a2b3c4d5e6f' — 1 "+
				"discovery/read errors: "+store+" (") {
			t.Errorf("%s: exit %d stdout %q stderr %q, want 1 and an INCOMPLETE lookup naming %s", verb, code, stdout,
				stderr, store)
		}
	}
}

// A target is an OpenCode locator only when its store part names opencode.db.
func TestSplitOpenCodeLocator(t *testing.T) {
	cases := []struct {
		target, store, id string
		ok                bool
	}{
		{"/x/opencode.db#ses_1", "/x/opencode.db", "ses_1", true},
		{"rel/opencode.db#ses_1", "rel/opencode.db", "ses_1", true},
		{"/x/opencode.db#", "", "", false},
		{"/x/other.db#ses_1", "", "", false},
		{"/x/session.jsonl", "", "", false},
		{"ses_1", "", "", false},
	}
	for _, c := range cases {
		store, id, ok := splitOpenCodeLocator(c.target)
		if store != c.store || id != c.id || ok != c.ok {
			t.Errorf("splitOpenCodeLocator(%q) = %q, %q, %v; want %q, %q, %v", c.target, store, id, ok, c.store, c.id,
				c.ok)
		}
	}
}
