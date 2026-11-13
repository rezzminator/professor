package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// branchInheritJail is the shared fixture for the "a fork inherits its
// parent's account and cache TTL" regression suite: two configured Claude
// accounts — 1 is the machine primary (M), 7 is the account the fork's
// parent chat actually lives on (N) — plus a fake `claude` binary that
// records the environment and argv the fork was actually born with, so the
// tests read the REAL run string a live `pfm chat branch` produces instead
// of a stand-in for it.
type branchInheritJail struct {
	root, tmuxDir, sidDir, procRoot string
	primaryDir, parentDir           string
	configPath, argsPath, envPath   string
	fakeClaude                      string
}

// newBranchInheritFixture builds the jail's directories, machine config and
// environment; fakeClaude names the binary the jail tests write (nothing
// here runs it).
func newBranchInheritFixture(t *testing.T) *branchInheritJail {
	t.Helper()
	root := testjail.ShortRoot(t)
	home := filepath.Join(root, "home")
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	sidDir := filepath.Join(root, "sid")
	procRoot := filepath.Join(root, "proc")
	codexHome := filepath.Join(root, "codex")
	binDir := filepath.Join(root, "bin")
	primaryDir := filepath.Join(root, "acct1")
	parentDir := filepath.Join(root, "acct7")
	for _, directory := range []string{
		home, tmuxDir, sidDir, procRoot, codexHome, binDir,
		filepath.Join(primaryDir, "projects"), filepath.Join(parentDir, "projects"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	argsPath := filepath.Join(root, "claude.args")
	envPath := filepath.Join(root, "claude.env")
	fakeClaude := filepath.Join(binDir, "claude")
	configPath := filepath.Join(root, "machine.json")
	config := fmt.Sprintf(
		`{"version":1,"accounts":[{"id":1,"configDir":%q},{"id":7,"configDir":%q}]}`,
		primaryDir, parentDir,
	)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))
	t.Setenv("TMUX_TMPDIR", root)
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_CACHE_DB", filepath.Join(root, "pfm-cache.db"))
	t.Setenv("PFM_STATE_DB", filepath.Join(root, "shared.db"))
	t.Setenv("PFM_SID_DIR", sidDir)
	// Every fork resolves its session id at once unless a test clears this.
	t.Setenv("PFM_TEST_BRANCH_FORK_ID", "f2000000-0000-4000-8000-000000000002")
	t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(root, "unused-claude-roots"))
	t.Setenv("PFM_CODEX_ROOT", codexHome)
	t.Setenv("PFM_TMUX_DIR", tmuxDir)
	t.Setenv("PFM_TMUX_CONF", "/dev/null")
	t.Setenv("PFM_PROC_ROOT", procRoot)
	t.Setenv("PFM_TEST_BRANCH_ARGS", argsPath)
	t.Setenv("PFM_TEST_BRANCH_ENV", envPath)

	return &branchInheritJail{
		root: root, tmuxDir: tmuxDir, sidDir: sidDir, procRoot: procRoot,
		primaryDir: primaryDir, parentDir: parentDir,
		configPath: configPath, argsPath: argsPath, envPath: envPath, fakeClaude: fakeClaude,
	}
}

func runBranchForked(t *testing.T, jail *branchInheritJail, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	full := append([]string{"--config", jail.configPath, "chat", "branch"}, args...)
	var out, err bytes.Buffer
	code = run(full, &out, &err)
	return out.String(), err.String(), code
}

// TestChatBranchFallsToTheAccountRefusalWhenTheParentScanFails is the
// absence-vs-error proof: when the parent-row scan itself cannot run (the
// store fails to open), that must surface as a reported failure, never
// silently fold into "parent not found, fork on primary". ENOTDIR is used
// instead of a permission bit so the injection is honest for a root-run
// suite (a chmod-based block is a silent no-op for uid 0).
func TestChatBranchFallsToTheAccountRefusalWhenTheParentScanFails(t *testing.T) {
	jail := newBranchInheritFixture(t)
	blocker := filepath.Join(jail.root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_CACHE_DB", filepath.Join(blocker, "pfm-cache.db"))

	const someID = "a7000000-7777-4777-8777-777777777777"
	stdout, stderr, code := runBranchForked(t, jail,
		"--engine", "claude", "--session-id", someID,
		"--cwd", jail.root, "--name", "scan-failure-branch",
	)
	refusal := "pfm chat branch: the account of session " + someID +
		" could not be resolved (no launch record, no live seat naming it); pass --account N\n"
	if code != 1 || stdout != "" || !strings.HasPrefix(stderr, "pfm chat branch: resolve parent session: ") ||
		!strings.HasSuffix(stderr, refusal) {
		t.Fatalf("chat branch rc=%d stdout=%q stderr=%q, want rc=1, the scan failure named, then %q",
			code, stdout, stderr, refusal)
	}
}
