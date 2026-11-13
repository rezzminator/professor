package fakegit

import (
	"os"
	"path/filepath"
	"testing"
)

// TestFakeGitConfigIsGitsOwnFile: a key set lands in .git/config in git's own
// layout, reads back, and a line git cannot parse fails every verb.
func TestFakeGitConfigIsGitsOwnFile(t *testing.T) {
	git, repo := newFakeGitRepo(t)
	git.Must(t, repo, "remote", "add", "origin", "/remote.git")
	git.Must(t, repo, "config", "core.hooksPath", ".githooks")
	config := filepath.Join(repo, ".git", "config")
	raw, err := os.ReadFile(config)
	want := "[core]\n\tbare = false\n\thooksPath = .githooks\n[user]\n\temail = fixture.invalid\n\tname = fixture-identity\n" +
		"[remote \"origin\"]\n\turl = /remote.git\n"
	if err != nil || string(raw) != want {
		t.Fatalf("%s = %q (%v), want %q", config, raw, err, want)
	}
	if got := git.Must(t, repo, "config", "--get", "remote.origin.url"); got != "/remote.git" {
		t.Fatalf("config --get remote.origin.url = %q, want /remote.git", got)
	}
	if result := fakeGitRun(t, git, repo, []string{}, "config", "--get", "user.signingkey"); result.ExitCode != 1 {
		t.Fatalf("config --get of an unset key: exit %d, want 1", result.ExitCode)
	}
	if err := os.WriteFile(config, []byte("[[[ broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := fakeGitRun(t, git, repo, []string{}, "status", "--porcelain")
	if wantErr := "fatal: bad config line 1 in file " + config + "\n"; result.ExitCode != 128 ||
		string(result.Stderr) != wantErr {
		t.Fatalf("status over a broken config: exit %d stderr %q, want 128 %q", result.ExitCode, result.Stderr, wantErr)
	}
}
