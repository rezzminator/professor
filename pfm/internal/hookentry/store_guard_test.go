package hookentry

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// storeGuardHome is a HOME holding the store (~/.claude), an account dir
// (~/.cc/3) and a symlink to the store (~/link), made the process's HOME with
// no ambient CLAUDE_CONFIG_DIR.
func storeGuardHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, dir := range []string{".claude/plugins", ".cc/3"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(home, ".claude"), filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	return home
}

func runStoreGuard(payload string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := StoreGuard(strings.NewReader(payload), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestStoreGuardDeniesClaudeRunOnTheStore(t *testing.T) {
	home := storeGuardHome(t)
	for _, command := range []string{
		`CLAUDE_CONFIG_DIR=~/.claude claude plugin marketplace update x`,
		`CLAUDE_CONFIG_DIR=~/.claude/ claude plugin list`,
		`CLAUDE_CONFIG_DIR=$HOME/.claude claude plugin list`,
		`CLAUDE_CONFIG_DIR=` + home + `/.claude claude plugin list`,
		`CLAUDE_CONFIG_DIR=` + home + `/.claude/ claude plugin list`,
		`CLAUDE_CONFIG_DIR=` + home + `/.claude/plugins claude plugin list`,
		`CLAUDE_CONFIG_DIR=` + home + `/link claude plugin list`,
		`CLAUDE_CONFIG_DIR=$HOME/.cc/../.claude claude plugin list`,
		`CLAUDE_CONFIG_DIR=$HOME/.claude /usr/local/bin/claude plugin list`,
		`CLAUDE_CONFIG_DIR=$HOME/.CLAUDE claude plugin list`,
	} {
		t.Run(command, func(t *testing.T) {
			code, stdout, stderr := runStoreGuard(gitGuardPayload(t, "Bash", command, "/tmp", ""))
			if code != 0 || !strings.Contains(stdout, gitGuardDenied) || !strings.Contains(stdout, storeGuardReason) {
				t.Fatalf("code=%d stdout=%q stderr=%q, want deny with %q", code, stdout, stderr, storeGuardReason)
			}
		})
	}
}

func TestStoreGuardAllowsEverythingElse(t *testing.T) {
	storeGuardHome(t)
	for _, command := range []string{
		`claude plugin list`,
		`CLAUDE_CONFIG_DIR=$HOME/.cc/3 claude plugin list`,
		`CLAUDE_CONFIG_DIR= claude plugin list`,
		`CLAUDE_CONFIG_DIR=$HOME/.claude-dev claude plugin list`,
		`grep -rn 'CLAUDE_CONFIG_DIR=$HOME/.claude claude' docs`,
		`echo CLAUDE_CONFIG_DIR=$HOME/.claude claude`,
		`CLAUDE_CONFIG_DIR=$HOME/.claude; echo set`,
	} {
		t.Run(command, func(t *testing.T) {
			code, stdout, stderr := runStoreGuard(gitGuardPayload(t, "Bash", command, "/tmp", ""))
			if code != 0 || stdout != "" || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q, want allowed in silence", code, stdout, stderr)
			}
		})
	}
}

func TestStoreGuardAllowsTheOverrideWhenTheChatAlreadyRunsOnTheStore(t *testing.T) {
	home := storeGuardHome(t)
	t.Setenv("CLAUDE_CONFIG_DIR", home+"/.claude")
	code, stdout, stderr := runStoreGuard(
		gitGuardPayload(t, "Bash", `CLAUDE_CONFIG_DIR=$HOME/.claude claude plugin list`, "/tmp", ""),
	)
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q, want allowed: the override changes nothing", code, stdout, stderr)
	}
}

func TestStoreGuardFailsOpenAndSaysWhy(t *testing.T) {
	storeGuardHome(t)
	denied := `CLAUDE_CONFIG_DIR=$HOME/.claude claude plugin list`
	payload := func(command string) string { return gitGuardPayload(t, "Bash", command, "/tmp", "") }
	for _, test := range []struct{ name, payload, stderr string }{
		{"malformed payload", "not-json", "decode hook payload (fail-open)"},
		{"unparseable command", gitGuardPayload(t, "Bash", `CLAUDE_CONFIG_DIR=$HOME/.claude claude "unterminated`, "/tmp", ""), "could not parse command (fail-open)"},
		{"not a Bash call", gitGuardPayload(t, "Read", denied, "/tmp", ""), ""},
		{"empty payload", "", ""},
		{"unknown variable", payload(`CLAUDE_CONFIG_DIR=$NOT_SET/.claude claude plugin list`), "cannot resolve CLAUDE_CONFIG_DIR=$NOT_SET/.claude (fail-open)"},
		{"command substitution", payload(`CLAUDE_CONFIG_DIR=$(echo ~/.claude) claude plugin list`), "cannot resolve CLAUDE_CONFIG_DIR="},
		{"default expansion", payload(`CLAUDE_CONFIG_DIR=${HOME:-/x}/.claude claude plugin list`), "cannot resolve CLAUDE_CONFIG_DIR="},
		{"relative after cd", payload(`cd ~ && CLAUDE_CONFIG_DIR=.claude claude plugin list`), "cannot resolve CLAUDE_CONFIG_DIR=.claude (fail-open)"},
		{"eval", payload(`eval "CLAUDE_CONFIG_DIR=~/.claude claude plugin list"`), "cannot read the command eval runs (fail-open)"},
		{"piped shell", payload(`echo 'CLAUDE_CONFIG_DIR=~/.claude claude plugin list' | bash`), "cannot read the command bash runs (fail-open)"},
		{"heredoc shell", payload("bash <<'EOF'\nCLAUDE_CONFIG_DIR=~/.claude claude plugin list\nEOF"), "cannot read the command bash runs (fail-open)"},
		{"env -S", payload(`env -S 'CLAUDE_CONFIG_DIR=~/.claude claude plugin list'`), "cannot read the command env runs (fail-open)"},
		{"function", payload(`f() { claude plugin list; }; CLAUDE_CONFIG_DIR=~/.claude f`), "f runs on the store and may run claude unseen (fail-open)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, stdout, stderr := runStoreGuard(test.payload)
			if code != 0 || stdout != "" || (test.stderr == "") != (stderr == "") ||
				!strings.Contains(stderr, test.stderr) {
				t.Fatalf("code=%d stdout=%q stderr=%q, want allowed and stderr %q", code, stdout, stderr, test.stderr)
			}
		})
	}
	t.Run("no HOME", func(t *testing.T) {
		t.Setenv("HOME", "")
		code, stdout, stderr := runStoreGuard(gitGuardPayload(t, "Bash", denied, "/tmp", ""))
		if code != 0 || stdout != "" || !strings.Contains(stderr, "HOME is empty (fail-open)") {
			t.Fatalf("code=%d stdout=%q stderr=%q, want allowed with the HOME cause on stderr", code, stdout, stderr)
		}
	})
}

func TestStoreGuardDeniesTheStoreBehindItsSymlink(t *testing.T) {
	home := t.TempDir()
	target := filepath.Join(home, "dotfiles", "claude")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(home, ".claude")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, command := range []string{
		`CLAUDE_CONFIG_DIR=$HOME/.claude claude plugin list`,
		`CLAUDE_CONFIG_DIR=$HOME/dotfiles/claude claude plugin list`,
	} {
		code, stdout, stderr := runStoreGuard(gitGuardPayload(t, "Bash", command, home, ""))
		if code != 0 || !strings.Contains(stdout, storeGuardReason) {
			t.Errorf("%s: code=%d stdout=%q stderr=%q, want deny on a symlinked store", command, code, stdout, stderr)
		}
	}
	relative := gitGuardPayload(t, "Bash", `CLAUDE_CONFIG_DIR=.claude claude plugin list`, home, "")
	code, stdout, _ := runStoreGuard(relative)
	if code != 0 || !strings.Contains(stdout, storeGuardReason) {
		t.Errorf("relative .claude from HOME: code=%d stdout=%q, want deny", code, stdout)
	}
}
