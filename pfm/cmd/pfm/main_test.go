package main

import (
	"bytes"
	"context"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"hostops/pfm/internal/doctor"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/spawn"
	"hostops/pfm/internal/store"
	"hostops/pfm/internal/testjail"
)

func TestVersion(t *testing.T) {
	jailTest(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(version) code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "pfm dev\n"; got != want {
		t.Fatalf("run(version) stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("run(version) stderr = %q, want empty", stderr.String())
	}
}

func TestSettledRootInterface(t *testing.T) {
	jailTest(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--version) code = %d; stderr = %q", code, stderr.String())
	}
	if stdout.String() != "pfm dev\n" {
		t.Fatalf("run(--version) stdout = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--help) code = %d; stderr = %q", code, stderr.String())
	}
	help := stdout.String()
	for _, want := range []string{
		"operator commands:", "  chat", "  install", "  update", "  init", "wiring commands:",
		"  statusline", "  usage-hook",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("root help missing %q:\n%s", want, help)
		}
	}
	for _, retired := range []string{"  open ", "  kill ", "  unkill ", "  killed ", "  resolve ", "  bb ", "chat bb"} {
		if strings.Contains(help, retired) {
			t.Fatalf("root help still advertises %q:\n%s", retired, help)
		}
	}
}

func TestChatShimWhoamiCompatibilityRoute(t *testing.T) {
	jailTest(t)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")

	var stdout, stderr bytes.Buffer
	code := runChat([]string{"whoami"}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("chat whoami code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "unknown command") ||
		!strings.Contains(stderr.String(), "not inside tmux") {
		t.Fatalf("chat whoami did not reach the root identity command: %q", stderr.String())
	}
}

func TestLSKilledAbsorbsTheOldKilledListing(t *testing.T) {
	jailTest(t)
	const id = "88888888-8888-4888-8888-888888888888"
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Kill(context.Background(), store.Killed{
		ID: id, Engine: "cc", KilledAt: 42,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"ls", "--killed"},
		{"ls", "--killed", "--tsv"},
		{"ls", "--tsv", "--killed"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v code=%d stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != id+"\t\t42\n" {
			t.Fatalf("%v stdout=%q", args, stdout.String())
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	jailTest(t)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"no-such-command"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(unknown) code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("run(unknown) stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `unknown command "no-such-command"`) {
		t.Fatalf("run(unknown) stderr = %q, want unknown-command message", got)
	}
}

func TestKillKilledUnkillCLI(t *testing.T) {
	root := jailTest(t)
	const id = "99999999-9999-4999-8999-999999999999"
	transcriptPath := filepath.Join(root, "claude", "project", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		transcriptPath,
		[]byte(`{"type":"user","cwd":"/work/example","message":{"content":"kill fixture"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(
		context.Background(),
		store.Transcript{
			UUID:        id,
			Path:        transcriptPath,
			PromptCount: 12,
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "kill", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("kill code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	// The kill names its own mechanism (chat.KillOutcome): this fixture has no
	// live pane, so it de-lists and says so.
	if stdout.String() != "killed "+id+"\tde-listed only, no live pane closed\n" || stderr.Len() != 0 {
		t.Fatalf("kill stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ls", "--killed"}, &stdout, &stderr); code != 0 {
		t.Fatalf("killed code=%d stderr=%q", code, stderr.String())
	}
	fields := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\t")
	if len(fields) != 3 || fields[0] != id || fields[1] != "cc" {
		t.Fatalf("killed stdout=%q, want id/engine/killed_at only", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"chat", "unkill", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("unkill code=%d stderr=%q", code, stderr.String())
	}
	if stdout.String() != "unkilled "+id+"\n" {
		t.Fatalf("unkill stdout=%q", stdout.String())
	}
}

func TestKilledPruneOrphansCLI(t *testing.T) {
	jailTest(t)
	const (
		live    = "11111111-1111-4111-8111-111111111111"
		orphan  = "22222222-2222-4222-8222-222222222222"
		orphan2 = "33333333-3333-4333-8333-333333333333"
	)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := database.UpsertTranscript(ctx, store.Transcript{
		UUID: live,
		Path: "/jailed/" + live + ".jsonl",
	}); err != nil {
		t.Fatal(err)
	}
	for _, killed := range []store.Killed{
		{ID: live, Engine: "cc", KilledAt: 10},
		{ID: orphan, Engine: "cc", KilledAt: 20},
		{ID: orphan2, Engine: "cx", KilledAt: 30},
	} {
		if err := database.Kill(ctx, killed); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"archive", "--prune-orphans"}, &stdout, &stderr); code != 0 {
		t.Fatalf("prune dry run code=%d stderr=%q", code, stderr.String())
	}
	// The engine column is empty for every orphan, and that IS the report: an
	// engine is derived from whichever index table claims the id, and an orphan
	// is precisely a kill no index table claims any more.
	dryRun := stdout.String()
	if !strings.Contains(dryRun, "would prune\t"+orphan+"\t\t20\n") ||
		!strings.Contains(dryRun, "would prune\t"+orphan2+"\t\t30\n") ||
		!strings.Contains(dryRun, "2 orphaned kill(s); re-run with --yes to delete\n") {
		t.Fatalf("prune dry run stdout=%q", dryRun)
	}
	if strings.Contains(dryRun, live) {
		t.Fatalf("prune dry run named the live kill: stdout=%q", dryRun)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ls", "--killed"}, &stdout, &stderr); code != 0 {
		t.Fatalf("killed after dry run code=%d stderr=%q", code, stderr.String())
	}
	if lines := strings.Count(stdout.String(), "\n"); lines != 3 {
		t.Fatalf("dry run deleted rows: killed stdout=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"archive", "--prune-orphans", "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("prune code=%d stderr=%q", code, stderr.String())
	}
	pruned := stdout.String()
	if !strings.Contains(pruned, "pruned\t"+orphan+"\t\t20\n") ||
		!strings.Contains(pruned, "pruned 2 orphaned kill(s)\n") {
		t.Fatalf("prune stdout=%q", pruned)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ls", "--killed"}, &stdout, &stderr); code != 0 {
		t.Fatalf("killed after prune code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.HasPrefix(got, live+"\tcc\t10\n") ||
		strings.Count(got, "\n") != 1 {
		t.Fatalf("killed after prune stdout=%q, want only the live kill", got)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ls", "--killed", "--yes"}, &stdout, &stderr); code != 2 {
		t.Fatalf("killed --yes without --prune-orphans code=%d, want 2", code)
	}
}

func TestKillSelfResolveAndInternalCLI(t *testing.T) {
	jailTest(t)
	const id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(
		context.Background(),
		store.Transcript{
			UUID: id,
			Path: "/jailed/" + id + ".jsonl",
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", filepath.Join(t.TempDir(), "cc-1-1-1")+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv("CLAUDE_CODE_SESSION_ID", id)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "kill", "self"}, &stdout, &stderr); code != 0 {
		t.Fatalf("kill --self code=%d stderr=%q", code, stderr.String())
	}
	// A self-kill DOES carry a tmux address, so its line names the pane it is
	// closing rather than a de-listing.
	if stdout.String() != "killed "+id+"\tclosing pane %1 on socket cc-1-1-1\n" {
		t.Fatalf("kill --self stdout=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run(
		[]string{"chat", "resolve", "missing"},
		&stdout,
		&stderr,
	); code != codeUnknownChat {
		t.Fatalf(
			"resolve miss code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if stdout.String() != "missing\tnot-found\n" ||
		!strings.Contains(stderr.String(), "no chat named") {
		t.Fatalf("resolve miss stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code := run([]string{
		"internal",
		"kill-exit",
		"--engine", "invalid",
		"--id", id,
		"--path", "/jailed/transcript.jsonl",
		"--socket", "/jailed/socket",
		"--socket-name", "cc-1-1-1",
		"--pane", "%1",
	}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), `unknown engine "invalid"`) {
		t.Fatalf("internal code=%d stderr=%q", code, stderr.String())
	}
}

func TestWiredIndexListOpenAndDoctor(t *testing.T) {
	root := jailTest(t)
	t.Setenv(spawn.TestFreshSocketEnv, "cc-1700000000-1-1")
	project := filepath.Join(root, "work", "project")
	transcriptDir := filepath.Join(root, "claude", "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(transcriptDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "11111111-1111-4111-8111-111111111111"
	content := `{"type":"user","cwd":` +
		strconv.Quote(project) +
		`,"message":{"content":"Wired prompt"}}` + "\n"
	if err := os.WriteFile(
		filepath.Join(transcriptDir, id+".jsonl"),
		[]byte(content),
		0o600,
	); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"index", "--full"}, &stdout, &stderr); code != 0 {
		t.Fatalf("index code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "files=1") ||
		!strings.Contains(stdout.String(), "full=1") ||
		!strings.Contains(stdout.String(), "touched=2") {
		t.Fatalf("index stdout=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ls", "--tsv"}, &stdout, &stderr); code != 0 {
		t.Fatalf("ls code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "\t"+id+"\t") ||
		!strings.Contains(stdout.String(), "resume-claude") {
		t.Fatalf("ls stdout=%q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	readServer := holdClaudeOpen(t, root, "cc-1700000000-1-1")
	if code := run([]string{"chat", "open", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("open code=%d stderr=%q", code, stderr.String())
	}
	if lines := strings.Count(stdout.String(), "\n"); lines != 1 {
		t.Fatalf("open emitted %d lines: %q", lines, stdout.String())
	}
	if !strings.Contains(stdout.String(), "attach -t 'cc-1700000000-1-1'") {
		t.Fatalf("open stdout=%q", stdout.String())
	}
	// The resume is born through the one chat-server creator: its window
	// carries the engine's name, never one its pane command chose.
	if run := readServer("#{pane_start_command}"); !strings.Contains(run, "--resume") || !strings.Contains(run, id) {
		t.Fatalf("opened server runs %q, want the resume of %s", run, id)
	}
	if window := readServer("#{window_name}"); window != "Claude" {
		t.Fatalf("opened window = %q, want Claude", window)
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"doctor"}, &stdout, &stderr); code != 0 {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "doctor: clean") ||
		!strings.Contains(stdout.String(), "transcripts=1") {
		t.Fatalf("doctor stdout=%q", stdout.String())
	}
}

// TestCheckRefusesALiveCodexSocketMissingFromTheGoRows is the regression this
// checker existed to catch and did not. A legacy-only live-codex row means the
func TestDoctorReportsDamagedDatabaseWithoutPanic(t *testing.T) {
	jailTest(t)
	dbPath := os.Getenv(paths.EnvDB)
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"doctor"}, &stdout, &stderr); code != 3 {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "doctor: config path=") ||
		!strings.Contains(stdout.String(), "doctor: unhealthy database:") {
		t.Fatalf("doctor stdout=%q", stdout.String())
	}
}

// TestDoctorNamesAnExistingButUnwiredPrePushGate is the issue-6 regression:
// the repository shipped the hook but no diagnostic distinguished "armed"
// from "file exists and Git will never execute it".
func TestDoctorNamesAnExistingButUnwiredPrePushGate(t *testing.T) {
	savedProbe := doctor.PrePushGateProbeOverride
	doctor.PrePushGateProbeOverride = nil
	t.Cleanup(func() { doctor.PrePushGateProbeOverride = savedProbe })

	root := jailTest(t)
	repository := filepath.Join(root, "repository")
	if err := os.MkdirAll(filepath.Join(repository, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repository, ".githooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(repository, ".git", "config"),
		[]byte("[core]\n\trepositoryformatversion = 0\n\tbare = false\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(repository, ".git", "HEAD"),
		[]byte("ref: refs/heads/main\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{"objects", filepath.Join("refs", "heads")} {
		if err := os.MkdirAll(filepath.Join(repository, ".git", directory), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(
		filepath.Join(repository, ".githooks", "pre-push"),
		[]byte("#!/bin/sh\nexit 0\n"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Chdir(repository)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"doctor"}, &stdout, &stderr); code != 1 {
		t.Fatalf("doctor code=%d, want warning exit\nstdout=%s\nstderr=%s", code, stdout.String(), stderr.String())
	}
	want := "doctor: pre-push gate=UNWIRED expected=.githooks actual=(unset)"
	if !strings.Contains(stdout.String(), want) {
		t.Fatalf("doctor omitted %q:\n%s", want, stdout.String())
	}

	if err := os.WriteFile(
		filepath.Join(repository, ".git", "config"),
		[]byte("[core]\n\trepositoryformatversion = 0\n\tbare = false\n\thooksPath = .githooks\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"doctor"}, &stdout, &stderr); code != 0 {
		t.Fatalf("armed doctor code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if want := "doctor: pre-push gate=armed core.hooksPath=.githooks"; !strings.Contains(stdout.String(), want) {
		t.Fatalf("armed doctor omitted %q:\n%s", want, stdout.String())
	}

	if err := os.Chmod(filepath.Join(repository, ".githooks", "pre-push"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"doctor"}, &stdout, &stderr); code != 1 {
		t.Fatalf("broken-hook doctor code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
	if want := "doctor: pre-push gate=BROKEN core.hooksPath=.githooks"; !strings.Contains(stdout.String(), want) {
		t.Fatalf("broken-hook doctor omitted %q:\n%s", want, stdout.String())
	}
}

func TestUsageErrors(t *testing.T) {
	jailTest(t)
	for _, args := range [][]string{
		{"ls", "--plain", "--tsv"},
		{"ls", "--killed", "--all"},
		{"chat", "open"},
		{"index", "--unknown"},
		{"doctor", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("run(%q) code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
		if stderr.Len() == 0 {
			t.Fatalf("run(%q) emitted no usage/error", args)
		}
	}
}

// harnessPromptFixtureCaptured is the fixed "live" prompt every jailed
// doctor test observes through doctor.HarnessCaptureOverride (set in TestMain).
// Its content is arbitrary — the check only ever hashes it and compares
// against whatever baseline stageHarnessPromptBaseline pins alongside it.
const harnessPromptFixtureCaptured = "pfm jail fixture harness prompt\n"

// stageHarnessPromptBaseline writes the managed baseline pin a wired machine
// carries after `pfm install` — internal/installer/assets/prompts/harness-original.sha256,
// staged verbatim by stageAssets — so a hand-built "wired" doctor fixture can
// reach the same matches-baseline verdict a real install produces, without
// re-deriving or re-pinning the real embedded asset.
func stageHarnessPromptBaseline(t *testing.T, home string) {
	t.Helper()
	for _, model := range doctor.HarnessPromptModels {
		stageModelHarnessPromptBaseline(t, home, model, harnessPromptFixtureCaptured, "harness-prompt-fixture.md")
	}
}

func stageModelHarnessPromptBaseline(
	t *testing.T,
	home string,
	model doctor.HarnessPromptModel,
	captured, name string,
) {
	t.Helper()
	testjail.StageHarnessPromptBaseline(t, home, model.Alias, model.Stem, captured, name)
}

func jailTest(t *testing.T) string {
	t.Helper()
	root := testjail.InstalledHome(t)
	stageHarnessPromptBaseline(t, filepath.Join(root, "home"))
	return root
}

func jailPaths(t *testing.T) paths.Values {
	t.Helper()
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve jail paths: %v", err)
	}
	return resolved
}

func writeJailedCodexAuth(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(
		filepath.Join(root, "codex", "auth.json"),
		[]byte(`{"tokens":{"access_token":"fixture","account_id":"fixture"}}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
}

// holdClaudeOpen readies the jail for a `chat open` that creates its resume's
// server through the one chat-server creator BEFORE it prints the attach: the
// stock jail claude exits at once and would take that fresh server with it,
// so it becomes a pane that stays up, and the server ends with the test. It
// returns a reader for the created server's pane.
func holdClaudeOpen(t *testing.T, root, socket string) func(format string) string {
	t.Helper()
	managed := filepath.Join(root, "home", ".local", "share", "pfm", "install", "bin", "claude")
	if err := os.WriteFile(managed, []byte("#!/bin/sh\nexec sleep 120\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(root, "tmux", socket)
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", socketPath, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=")
		_ = kill.Run()
	})
	return func(format string) string {
		read := exec.Command("tmux", "-S", socketPath, "display-message", "-p", "-t", socket, format)
		read.Env = append(os.Environ(), "TMUX=")
		output, err := read.Output()
		if err != nil {
			t.Fatalf("read the opened server %s: %v", socket, err)
		}
		return strings.TrimSpace(string(output))
	}
}

// argsZeroStringLiterals walks body and collects every string literal a
// "==" or "!=" comparison holds against an args[0] index expression — the
// shape both run's top-level switch cases and runInternal's if-chain (plus
// its final "!= kill-exit" negation) use to name a subcommand. It is the
// structural half of issue #24 F1's reachability proof: reading the actual
// dispatch, never trusting a second hand-copied list to match it.
func argsZeroStringLiterals(body *ast.BlockStmt) map[string]bool {
	literals := map[string]bool{}
	isArgsZero := func(expr ast.Expr) bool {
		index, ok := expr.(*ast.IndexExpr)
		if !ok {
			return false
		}
		ident, ok := index.X.(*ast.Ident)
		return ok && ident.Name == "args"
	}
	ast.Inspect(body, func(n ast.Node) bool {
		binary, ok := n.(*ast.BinaryExpr)
		if !ok || (binary.Op != token.EQL && binary.Op != token.NEQ) {
			return true
		}
		var literal *ast.BasicLit
		switch {
		case isArgsZero(binary.X):
			literal, _ = binary.Y.(*ast.BasicLit)
		case isArgsZero(binary.Y):
			literal, _ = binary.X.(*ast.BasicLit)
		}
		if literal == nil || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil {
			literals[value] = true
		}
		return true
	})
	return literals
}

// switchCaseStringLiterals collects every string literal a top-level
// "switch args[0]" case clause names.
// switchCaseStringLiterals collects every case's plain string literal AND
// the printed source text of every non-literal case expression — run's own
// "codex" case matches on pfmengine.MustLookup(pfmengine.Codex).LongName,
// not a bare "codex" literal, so a caller that needs that one name checks
// the printed-text set with pfmengine's own known selector text instead.
func switchCaseStringLiterals(fset *token.FileSet, body *ast.BlockStmt) (literals, printedExprs map[string]bool) {
	literals = map[string]bool{}
	printedExprs = map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				if value, err := strconv.Unquote(literal.Value); err == nil {
					literals[value] = true
				}
				continue
			}
			var buf bytes.Buffer
			if err := printer.Fprint(&buf, fset, expr); err == nil {
				printedExprs[buf.String()] = true
			}
		}
		return true
	})
	return literals, printedExprs
}

// findFuncDecl parses main.go once and returns the *ast.FuncDecl body for
// name — "run" or "runInternal", the two functions topLevelSubcommands and
// internalSubcommands must stay in lockstep with.
func findFuncDecl(t *testing.T, name string) (*ast.BlockStmt, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn.Body, fset
		}
	}
	t.Fatalf("main.go declares no func %s", name)
	return nil, nil
}

// TestTopLevelSubcommandsReachTheirHandler pins issue #24 F1's top-level
// door: every name topLevelSubcommands lists (the same list
// installer.SetImplementedSubcommands teaches the installer at process
// start) must be a case run's own "switch args[0]" actually matches — a name
// listed but unmatched would silently fall to the default "unknown command"
// arm, and, worse, unknownPFMHookCommand would then treat an operator's own
// hook naming it as implemented when this binary's dispatch disagrees.
func TestTopLevelSubcommandsReachTheirHandler(t *testing.T) {
	body, fset := findFuncDecl(t, "run")
	literals, printedExprs := switchCaseStringLiterals(fset, body)
	// Engine cases match on registry LongName expressions, not bare literals;
	// topLevelSubcommands carries those same runtime values.
	selectors := map[string]string{
		pfmengine.MustLookup(pfmengine.Codex).LongName:    "pfmengine.MustLookup(pfmengine.Codex).LongName",
		pfmengine.MustLookup(pfmengine.OpenCode).LongName: "pfmengine.MustLookup(pfmengine.OpenCode).LongName",
	}
	for _, name := range topLevelSubcommands {
		if literals[name] {
			continue
		}
		if selector, ok := selectors[name]; ok && printedExprs[selector] {
			continue
		}
		t.Fatalf(
			"topLevelSubcommands names %q, but run's switch has no matching case — it falls through to the default \"unknown command\" arm",
			name,
		)
	}
}

// TestInternalSubcommandsReachTheirHandler is TestTopLevelSubcommandsReachTheirHandler's
// twin for runInternal's if-chain, including "kill-exit"'s
// "args[0] != \"kill-exit\"" negation — the one entry not shaped like the
// rest's "args[0] == name" branches.
func TestInternalSubcommandsReachTheirHandler(t *testing.T) {
	body, _ := findFuncDecl(t, "runInternal")
	comparisons := argsZeroStringLiterals(body)
	for _, name := range internalSubcommands {
		if !comparisons[name] {
			t.Fatalf(
				"internalSubcommands names %q, but runInternal's if-chain never compares args[0] against it — it falls through to \"pfm internal: unknown subcommand\"",
				name,
			)
		}
	}
}
