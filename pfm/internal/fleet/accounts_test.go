package fleet

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestPrimaryAccountGoesThroughTheStateStore fixtures the OUTCOME of a picker
// account change: the shared store validates the roster and mirrors the choice
// into ~/.claude-primary for the statusline.
func TestPrimaryAccountGoesThroughTheStateStore(t *testing.T) {
	home := t.TempDir()
	values := paths.Values{
		Home:    home,
		StateDB: filepath.Join(home, ".local", "state", "pfm", "pfm.db"),
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
		StateDB: filepath.Join(blocked, "pfm.db"),
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

func TestClaudeSeatsKeepConfiguredDirs(t *testing.T) {
	seats := claudeSeats([]config.Account{{ID: 2, ConfigDir: "/x/seat"}}, "/home")
	if len(seats) != 1 || seats[0].Account != 2 || seats[0].ConfigDir != "/x/seat" {
		t.Fatalf("claudeSeats() = %#v", seats)
	}
	implicit := claudeSeats([]config.Account{{ID: 1, ConfigDir: "/x/seat", Implicit: true}}, "/home")
	if len(implicit) != 1 || implicit[0].ConfigDir != "/home/.claude" || !implicit[0].Implicit {
		t.Fatalf("implicit claudeSeats() = %#v, want the process default config dir", implicit)
	}
	codex := codexAccountRoots([]config.CodexAccount{{ID: 1, Home: "/x/codex"}})
	if len(codex) != 1 || codex[0].Account != 1 || codex[0].Path != "/x/codex" {
		t.Fatalf("codexAccountRoots() = %#v", codex)
	}
}
