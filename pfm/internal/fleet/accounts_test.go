package fleet

import (
	"os"
	"path/filepath"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/paths"
)

// TestPrimaryAccountGoesThroughTheStateStore fixtures the OUTCOME of a picker
// account change: the shared store validates the roster and mirrors the choice
// into ~/.claude-primary for the statusline.
func TestPrimaryAccountGoesThroughTheStateStore(t *testing.T) {
	home := t.TempDir()
	values := paths.Values{
		Home:    home,
		FleetDB: filepath.Join(home, ".cc", "fleet.db"),
	}
	machine := config.Defaults(home, []string{
		filepath.Join(home, ".cc", "1", "projects"),
		filepath.Join(home, ".cc", "2", "projects"),
		filepath.Join(home, ".cc", "3", "projects"),
	})
	if err := SetPrimaryAccount(values, machine, 3); err != nil {
		t.Fatalf("SetPrimaryAccount() = %v", err)
	}
	if got, err := PrimaryAccount(values, machine); got != 3 || err != nil {
		t.Fatalf("PrimaryAccount() = %d, %v", got, err)
	}
	content, err := os.ReadFile(filepath.Join(home, ".claude-primary"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "3\n" {
		t.Fatalf("primary mirror = %q", content)
	}

	if err := SetPrimaryAccount(values, machine, 4); err == nil {
		t.Fatal("off-roster account accepted")
	}

	// An unavailable database degrades to the mirror, so account selection is
	// never down because the durable store cannot open.
	bare := t.TempDir()
	blocked := filepath.Join(bare, "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	bareValues := paths.Values{
		Home:    bare,
		FleetDB: filepath.Join(blocked, "fleet.db"),
	}
	if err := SetPrimaryAccount(bareValues, machine, 2); err != nil {
		t.Fatalf("fallback SetPrimaryAccount() = %v", err)
	}
	if got, err := PrimaryAccount(bareValues, machine); got != 2 || err != nil {
		t.Fatalf("fallback PrimaryAccount() = %d, %v", got, err)
	}
	// A stale file naming a retired account reads back as the first account.
	if err := os.WriteFile(
		filepath.Join(bare, ".claude-primary"),
		[]byte("4\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if got, err := PrimaryAccount(bareValues, machine); got != 1 || err != nil {
		t.Fatalf("off-roster file PrimaryAccount() = %d, %v", got, err)
	}
}

// TestCurrentSocketReadsTheCallersOwnTmuxServer pins the $TMUX parse: the
// socket path is the first comma field, and outside tmux there is no socket.
func TestCurrentSocketReadsTheCallersOwnTmuxServer(t *testing.T) {
	for _, test := range []struct{ tmux, want string }{
		{"/tmp/tmux-501/cc-7,12345,0", "cc-7"},
		{"/tmp/tmux-501/vsct", "vsct"},
		{"", ""},
	} {
		t.Setenv("TMUX", test.tmux)
		if got := CurrentSocket(); got != test.want {
			t.Errorf("CurrentSocket() with TMUX=%q = %q, want %q", test.tmux, got, test.want)
		}
	}
}

// TestAccountRootsCanonicalizeProjectDirs pins that a symlinked project dir is
// matched by its target — compose compares transcript paths against these.
func TestAccountRootsCanonicalizeProjectDirs(t *testing.T) {
	realHome := t.TempDir()
	link := filepath.Join(t.TempDir(), "projects")
	if err := os.Symlink(realHome, link); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(realHome)
	if err != nil {
		t.Fatal(err)
	}
	roots := accountRoots([]config.Account{{ID: 2, ProjectDir: link}})
	if len(roots) != 1 || roots[0].Account != 2 || roots[0].Path != canonical {
		t.Fatalf("accountRoots() = %#v, want account 2 at %q", roots, canonical)
	}
	codex := codexAccountRoots([]config.CodexAccount{{ID: 1, Home: "/x/codex"}})
	if len(codex) != 1 || codex[0].Account != 1 || codex[0].Path != "/x/codex" {
		t.Fatalf("codexAccountRoots() = %#v", codex)
	}
}
