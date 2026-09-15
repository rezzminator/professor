package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"hostops/pfm/internal/testjail"
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
}

func newBranchInheritJail(t *testing.T) *branchInheritJail {
	t.Helper()
	root := testjail.ShortRoot(t)
	home := filepath.Join(root, "home")
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	sidDir := filepath.Join(root, "sid")
	procRoot := filepath.Join(root, "proc")
	codexRoot := filepath.Join(root, "codex")
	binDir := filepath.Join(root, "bin")
	primaryDir := filepath.Join(root, "acct1")
	parentDir := filepath.Join(root, "acct7")
	for _, directory := range []string{
		home, tmuxDir, sidDir, procRoot, codexRoot, binDir,
		filepath.Join(primaryDir, "projects"), filepath.Join(parentDir, "projects"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	argsPath := filepath.Join(root, "claude.args")
	envPath := filepath.Join(root, "claude.env")
	fakeClaude := filepath.Join(binDir, "claude")
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$@\" > \"$PFM_TEST_BRANCH_ARGS\"\n" +
		"env > \"$PFM_TEST_BRANCH_ENV\"\n" +
		"exec sleep 120\n"
	if err := os.WriteFile(fakeClaude, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

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
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
	t.Setenv("PFM_SHARED_DB", filepath.Join(root, "shared.db"))
	t.Setenv("PFM_SID_DIR", sidDir)
	t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(root, "unused-claude-roots"))
	t.Setenv("PFM_CODEX_ROOT", codexRoot)
	t.Setenv("PFM_TMUX_DIR", tmuxDir)
	t.Setenv("PFM_TMUX_CONF", "/dev/null")
	t.Setenv("PFM_PROC_ROOT", procRoot)
	t.Setenv("PFM_TEST_BRANCH_ARGS", argsPath)
	t.Setenv("PFM_TEST_BRANCH_ENV", envPath)

	return &branchInheritJail{
		root: root, tmuxDir: tmuxDir, sidDir: sidDir, procRoot: procRoot,
		primaryDir: primaryDir, parentDir: parentDir,
		configPath: configPath, argsPath: argsPath, envPath: envPath,
	}
}

// registerParentTranscript writes a REAL (if minimal) parent transcript file
// under the given account directory, without starting any tmux pane or
// process — the shape `pfm chat branch` must resolve an account for even
// when the parent chat is not currently running. A real file (rather than a
// bare store row) is deliberate: pfmchat.Rows runs the actual
// incremental indexer over the account's project roots on every call, and an
// indexed row whose file does not exist on disk is stale-swept by that same
// pass — a DB-only fixture would be pruned out from under the test.
func registerParentTranscript(t *testing.T, accountDir, id string) {
	t.Helper()
	path := filepath.Join(accountDir, "projects", "proj", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := `{"type":"user","cwd":` + fmt.Sprintf("%q", filepath.Dir(path)) +
		`,"message":{"content":"parent seat prompt, long enough to not be junk"}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// registerLiveParent starts a REAL tmux pane and a REAL (jailed) /proc claude
// process bound to it, plus the SID crumb pointing at the given account's
// transcript path — the same three ingredients booting_row_jail_test.go and
// kill_cli_engine_jail_test.go use to prove a LiveClaude row, here reused so
// forkCache1H sees a genuinely live parent instead of a stand-in for one.
// pid must be unique within the test's own /proc fixture. socket MUST satisfy
// gather's strict crumb-filename grammar (ParseCrumbName / validCrumbSocket:
// "cc-<digits>-<digits>-<digits>") — freshClaudeSocketName builds one. An
// arbitrary "probe-..." socket silently fails ParseCrumbName and the crumb is
// dropped, which reads as "no live parent" rather than an error.
func registerLiveParent(
	t *testing.T,
	jail *branchInheritJail,
	socket, accountDir, id string,
	pid int,
	environ map[string]string,
) {
	t.Helper()
	panePID := startBootingPane(t, socket)
	paneID := strings.TrimSpace(runTmuxOutput(t, socket, "list-panes", "-F", "#{pane_id}"))
	path := filepath.Join(accountDir, "projects", "proj", id+".jsonl")
	crumb := filepath.Join(jail.sidDir, socket+"."+paneID)
	if err := os.WriteFile(crumb, []byte(path+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFakeProcess(t, jail.procRoot, fakeProcessSpec{
		pid: pid, parentPID: panePID, comm: "claude",
		cmdline: []string{"/opt/claude"}, environ: environ,
	})
}

// freshClaudeSocketName builds a socket name satisfying gather's crumb-name
// grammar (cc-<epoch>-<pid>-<n>), matching booting_row_jail_test.go's own
// convention — suffix only needs to be unique WITHIN one test process.
func freshClaudeSocketName(suffix int) string {
	return fmt.Sprintf("cc-%d-%d-%d", time.Now().Unix(), os.Getpid(), suffix)
}

// parseEnvDump turns a `env`-style NAME=value dump (one assignment per line,
// as the fake `claude` binary captures it) into a lookup map.
func parseEnvDump(content string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(content, "\n") {
		if line == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		result[key] = value
	}
	return result
}

func cleanupBranchSocket(t *testing.T, jail *branchInheritJail, socket string) {
	t.Helper()
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", filepath.Join(jail.tmuxDir, socket), "kill-server").Run()
	})
}

func runBranchForked(t *testing.T, jail *branchInheritJail, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	full := append([]string{"--config", jail.configPath, "chat", "branch"}, args...)
	var out, err bytes.Buffer
	code = run(full, &out, &err)
	return out.String(), err.String(), code
}

// forkedEnvironment blocks for the fake claude binary's captured
// environment, the real run string `pfm chat branch` produced.
func forkedEnvironment(t *testing.T, jail *branchInheritJail) map[string]string {
	t.Helper()
	content, err := waitForFile(t, jail.envPath, 10*time.Second)
	if err != nil {
		t.Fatalf("read forked claude environment: %v", err)
	}
	return parseEnvDump(string(content))
}

// TestChatBranchInheritsParentAccountWhenNoFlagGiven is the account half of
// the user's report ("branch ... does not inherit the ... account number, it
// has to be exactly the same"): a parent chat on account 7 (N), a machine
// primary of account 1 (M), and no --account. Before the fix, runChatBranch
// fed the primary straight into resolveRunEngineIDAccount and never
// consulted the parent at all, so the fork landed on M.
func TestChatBranchInheritsParentAccountWhenNoFlagGiven(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newBranchInheritJail(t)
	const parentID = "a1000000-1111-4111-8111-111111111111"
	const branchSocket = "probe-branch-acct-inherit"
	t.Setenv("PFM_TEST_FRESH_SOCKET", branchSocket)
	cleanupBranchSocket(t, jail, branchSocket)

	registerParentTranscript(t, jail.parentDir, parentID)

	stdout, stderr, code := runBranchForked(t, jail,
		"--engine", "claude", "--session-id", parentID,
		"--cwd", jail.root, "--name", "acct-inherit-branch",
	)
	if code != 0 {
		t.Fatalf("chat branch rc=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stderr, "could not be resolved") {
		t.Fatalf("unexpected unresolved-parent warning for an indexed parent: stderr=%q", stderr)
	}
	environment := forkedEnvironment(t, jail)
	if got := environment["CLAUDE_CONFIG_DIR"]; got != jail.parentDir {
		t.Fatalf(
			"CLAUDE_CONFIG_DIR=%q, want the parent's own account dir %q (machine primary is %q) — the fork did not inherit the parent's account",
			got,
			jail.parentDir,
			jail.primaryDir,
		)
	}
}

// TestChatBranchExplicitAccountFlagOverridesParent pins that an explicit
// --account still wins over a resolved parent account — the fix must not
// over-correct into always forking onto the parent no matter what the
// caller asked for.
func TestChatBranchExplicitAccountFlagOverridesParent(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newBranchInheritJail(t)
	const parentID = "a2000000-2222-4222-8222-222222222222"
	const branchSocket = "probe-branch-acct-explicit"
	t.Setenv("PFM_TEST_FRESH_SOCKET", branchSocket)
	cleanupBranchSocket(t, jail, branchSocket)

	registerParentTranscript(t, jail.parentDir, parentID)

	stdout, stderr, code := runBranchForked(t, jail,
		"--engine", "claude", "--session-id", parentID, "--account", "1",
		"--cwd", jail.root, "--name", "acct-explicit-branch",
	)
	if code != 0 {
		t.Fatalf("chat branch rc=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	environment := forkedEnvironment(t, jail)
	if got := environment["CLAUDE_CONFIG_DIR"]; got != jail.primaryDir {
		t.Fatalf(
			"CLAUDE_CONFIG_DIR=%q, want the EXPLICIT --account 1 dir %q, not the parent's account 7 dir %q",
			got, jail.primaryDir, jail.parentDir,
		)
	}
}

// TestChatBranchInheritsOneHourCacheFromLiveParentThroughCachingOffShell is
// the user's exact reported symptom: the invoking shell is itself a chat, so
// it carries the "CC_ARM_1H=0 ENABLE_PROMPT_CACHING_1H=1 CLAUDECODE=1"
// synth.go re-exports to force caching off DOWNSTREAM work — but a branch is
// a peer, not downstream work, and its LIVE 1h parent (present in
// Cache1HSockets) must still win.
func TestChatBranchInheritsOneHourCacheFromLiveParentThroughCachingOffShell(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newBranchInheritJail(t)
	const parentID = "a3000000-3333-4333-8333-333333333333"
	parentSocket := freshClaudeSocketName(31)
	const branchSocket = "probe-branch-cache1h-fork"
	t.Setenv("PFM_TEST_FRESH_SOCKET", branchSocket)
	cleanupBranchSocket(t, jail, branchSocket)

	// The reported symptom's exact invoking-shell shape.
	t.Setenv("CC_ARM_1H", "0")
	t.Setenv("ENABLE_PROMPT_CACHING_1H", "1")
	t.Setenv("CLAUDECODE", "1")

	registerLiveParent(t, jail, parentSocket, jail.parentDir, parentID, 95001, nil)

	stdout, stderr, code := runBranchForked(t, jail,
		"--engine", "claude", "--session-id", parentID,
		"--cwd", jail.root, "--name", "cache1h-inherit-branch",
	)
	if code != 0 {
		t.Fatalf("chat branch rc=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	environment := forkedEnvironment(t, jail)
	if got := environment["ENABLE_PROMPT_CACHING_1H"]; got != "1" {
		t.Fatalf(
			"ENABLE_PROMPT_CACHING_1H=%q, want \"1\" — the fork must inherit the LIVE 1h parent, not the caching-off invoking shell",
			got,
		)
	}
	if got, present := environment["FORCE_PROMPT_CACHING_5M"]; present {
		t.Fatalf("FORCE_PROMPT_CACHING_5M=%q present, want absent — fork was born 5m despite a live 1h parent", got)
	}
}

// TestChatBranchInheritsFiveMinuteCacheFromLiveParentAbsentFromCache1HSockets
// guards the opposite boundary: a live parent explicitly born 5m (absent
// from Cache1HSockets) must fork 5m too, even when the invoking shell claims
// 1h is armed — proving the fix does not over-correct into always 1h.
func TestChatBranchInheritsFiveMinuteCacheFromLiveParentAbsentFromCache1HSockets(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newBranchInheritJail(t)
	const parentID = "a4000000-4444-4444-8444-444444444444"
	parentSocket := freshClaudeSocketName(41)
	const branchSocket = "probe-branch-cache5m-fork"
	t.Setenv("PFM_TEST_FRESH_SOCKET", branchSocket)
	cleanupBranchSocket(t, jail, branchSocket)

	// The opposite of the reported symptom's shell: claims 1h is armed, to
	// prove the fork does not read the invoking shell at all.
	t.Setenv("CC_ARM_1H", "1")
	t.Setenv("ENABLE_PROMPT_CACHING_1H", "")
	t.Setenv("CLAUDECODE", "")

	registerLiveParent(t, jail, parentSocket, jail.parentDir, parentID, 95002, map[string]string{
		"FORCE_PROMPT_CACHING_5M": "1",
	})

	stdout, stderr, code := runBranchForked(t, jail,
		"--engine", "claude", "--session-id", parentID,
		"--cwd", jail.root, "--name", "cache5m-inherit-branch",
	)
	if code != 0 {
		t.Fatalf("chat branch rc=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	environment := forkedEnvironment(t, jail)
	if got := environment["FORCE_PROMPT_CACHING_5M"]; got != "1" {
		t.Fatalf(
			"FORCE_PROMPT_CACHING_5M=%q, want \"1\" — the fork must inherit the LIVE 5m parent, not an invoking shell claiming 1h",
			got,
		)
	}
	if got, present := environment["ENABLE_PROMPT_CACHING_1H"]; present {
		t.Fatalf("ENABLE_PROMPT_CACHING_1H=%q present, want absent — fork was born 1h despite a live 5m parent", got)
	}
}

// TestChatBranchNonLiveParentTakesConfiguredDefaultNotFalse proves that a
// dead/resumable parent's zero-value C1H (never observed, since C1H is only
// ever set while a process is live) is read as "unknown" and resolved to the
// account's CONFIGURED posture — never collapsed into "5m", which is exactly
// the absence-vs-error trap pfm/CLAUDE.md names.
func TestChatBranchNonLiveParentTakesConfiguredDefaultNotFalse(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newBranchInheritJail(t)
	// Top-level default is FALSE (5m); the parent's own account is
	// overridden to TRUE (1h). A non-live row's C1H field is always the
	// zero value false, so only reading the account's config — not the
	// row — can produce 1h here.
	config := fmt.Sprintf(
		`{"version":1,"claude":{"cache1h":false},"accounts":[{"id":1,"configDir":%q},{"id":7,"configDir":%q,"claude":{"cache1h":true}}]}`,
		jail.primaryDir,
		jail.parentDir,
	)
	if err := os.WriteFile(jail.configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	clearCache1HEnv(t)

	const parentID = "a5000000-5555-4555-8555-555555555555"
	const branchSocket = "probe-branch-cache-default"
	t.Setenv("PFM_TEST_FRESH_SOCKET", branchSocket)
	cleanupBranchSocket(t, jail, branchSocket)

	registerParentTranscript(t, jail.parentDir, parentID)

	stdout, stderr, code := runBranchForked(t, jail,
		"--engine", "claude", "--session-id", parentID,
		"--cwd", jail.root, "--name", "cache-default-branch",
	)
	if code != 0 {
		t.Fatalf("chat branch rc=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	environment := forkedEnvironment(t, jail)
	if got := environment["ENABLE_PROMPT_CACHING_1H"]; got != "1" {
		t.Fatalf(
			"ENABLE_PROMPT_CACHING_1H=%q, want \"1\" — a non-live parent's unobserved C1H=false must fall back to the account's configured posture (true here), not read as 5m",
			got,
		)
	}
	if got, present := environment["FORCE_PROMPT_CACHING_5M"]; present {
		t.Fatalf(
			"FORCE_PROMPT_CACHING_5M=%q present, want absent — the dead parent's zero-value C1H was read as 5m",
			got,
		)
	}
}

// TestChatBranchUnresolvableParentAccountWarnsAndUsesPrimary is the
// silent-substitution guard: when the parent session is not indexed at all,
// the fork must still land somewhere usable (the primary account), but it
// must say so on stderr — an absent parent row must never look identical to
// a parent that genuinely sits on the primary account.
func TestChatBranchUnresolvableParentAccountWarnsAndUsesPrimary(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newBranchInheritJail(t)
	// Deliberately never indexed anywhere — a fork of a session pfm has
	// never seen.
	const unknownID = "a6000000-6666-4666-8666-666666666666"
	const branchSocket = "probe-branch-acct-unresolvable"
	t.Setenv("PFM_TEST_FRESH_SOCKET", branchSocket)
	cleanupBranchSocket(t, jail, branchSocket)

	stdout, stderr, code := runBranchForked(t, jail,
		"--engine", "claude", "--session-id", unknownID,
		"--cwd", jail.root, "--name", "acct-unresolvable-branch",
	)
	if code != 0 {
		t.Fatalf("chat branch rc=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "parent account for session") || !strings.Contains(stderr, "could not be resolved") {
		t.Fatalf("stderr=%q, want the unresolved-parent-account warning naming the fallback", stderr)
	}
	if !strings.Contains(stderr, "forking on primary account 1") {
		t.Fatalf("stderr=%q, want the warning to name primary account 1 as the fallback used", stderr)
	}
	environment := forkedEnvironment(t, jail)
	if got := environment["CLAUDE_CONFIG_DIR"]; got != jail.primaryDir {
		t.Fatalf(
			"CLAUDE_CONFIG_DIR=%q, want the primary account dir %q — the warned fallback did not actually land there",
			got,
			jail.primaryDir,
		)
	}
}

// TestChatBranchReportsAFailedParentScanInsteadOfForkingBlind is the
// absence-vs-error proof: when the parent-row scan itself cannot run (the
// store fails to open), that must surface as a reported failure, never
// silently fold into "parent not found, fork on primary". ENOTDIR is used
// instead of a permission bit so the injection is honest for a root-run
// suite (a chmod-based block is a silent no-op for uid 0).
func TestChatBranchReportsAFailedParentScanInsteadOfForkingBlind(t *testing.T) {
	jail := newBranchInheritJail(t)
	blocker := filepath.Join(jail.root, "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_DB", filepath.Join(blocker, "fleet.db"))

	const someID = "a7000000-7777-4777-8777-777777777777"
	stdout, stderr, code := runBranchForked(t, jail,
		"--engine", "claude", "--session-id", someID,
		"--cwd", jail.root, "--name", "scan-failure-branch",
	)
	if code != 1 {
		t.Fatalf(
			"chat branch rc=%d, want 1 (a scan failure must be reported, not silently forked): stdout=%q stderr=%q",
			code,
			stdout,
			stderr,
		)
	}
	if !strings.Contains(stderr, "resolve parent session:") {
		t.Fatalf("stderr=%q, want it to name the failed parent-session resolve, not silence", stderr)
	}
	if strings.Contains(stderr, "forking on primary account") {
		t.Fatalf("stderr=%q, a scan failure must not be reported as a resolved-but-unknown parent", stderr)
	}
	if strings.Contains(stdout, "Branched") {
		t.Fatalf("stdout=%q, a scan failure must not still fork the chat", stdout)
	}
}
