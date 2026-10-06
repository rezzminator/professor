package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/stale"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRRDirEntryUsesInjectedHome(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(root, "unmanaged")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "injected-home")
	var stdout, stderr bytes.Buffer
	if code := runRRDirEntry(bytes.NewReader(payload), &stdout, &stderr, &paths.MapEnv{HomeDir: home}); code != 0 {
		t.Fatalf("runRRDirEntry code = %d, want fail-open 0; stderr = %q", code, stderr.String())
	}
	want := filepath.Join(home, ".professor", ".professor", "RR")
	if got := stdout.String(); !strings.Contains(got, want) {
		t.Fatalf("runRRDirEntry stdout = %q, want injected fallback %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("runRRDirEntry stderr = %q, want empty", stderr.String())
	}
}

func TestRRDirEntryReportsHomeErrorAndContinues(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"cwd": root})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runRRDirEntry(
		bytes.NewReader(payload),
		&stdout,
		&stderr,
		&paths.MapEnv{HomeErr: errors.New("home unavailable")},
	); code != 0 {
		t.Fatalf("runRRDirEntry code = %d, want fail-open 0; stderr = %q", code, stderr.String())
	}
	if got := stderr.String(); !strings.Contains(
		got,
		"pfm internal rr-dir: resolve home directory: home unavailable\n",
	) {
		t.Fatalf("runRRDirEntry stderr = %q, want visible home error", got)
	}
	if got := stdout.String(); !strings.Contains(got, "RR-DIR-ERROR:") || !strings.Contains(got, "no home directory") {
		t.Fatalf("runRRDirEntry stdout = %q, want fail-open hook response for empty home", got)
	}
}

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
}

func TestChatShimWhoamiCompatibilityRoute(t *testing.T) {
	jailTest(t)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	var stdout, stderr bytes.Buffer
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	code := runChatWithRuntime([]string{"whoami"}, strings.NewReader(""), &stdout, &stderr,
		runtime, context.Background())
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

func TestChatUnknownVerbIsAUsageError(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "no-such-verb"}, &stdout, &stderr); code != 2 {
		t.Fatalf("chat unknown verb code=%d stdout=%q stderr=%q, want 2", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), `pfm chat: unknown command "no-such-verb"`) ||
		!strings.Contains(stderr.String(), "usage: pfm chat") {
		t.Fatalf("chat unknown verb stdout=%q stderr=%q", stdout.String(), stderr.String())
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
	testjail.PinClaudeAsk(t, filepath.Join(root, "home"))
	if code := run([]string{"doctor"}, &stdout, &stderr); code != 0 {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "doctor: clean") ||
		!strings.Contains(stdout.String(), "transcripts=1") ||
		!strings.Contains(stdout.String(), "host-check: ok (22 checks)") ||
		!strings.Contains(stdout.String(), "account-links: ok (1 accounts × 22 entries)") {
		t.Fatalf("doctor stdout=%q", stdout.String())
	}
}

func TestDoctorReportsDamagedDatabaseWithoutPanic(t *testing.T) {
	jailTest(t)
	dbPath := os.Getenv(paths.EnvCacheDB)
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

// An existing pre-push hook is inactive until Git is configured to use it.
func TestDoctorNamesAnExistingButUnwiredPrePushGate(t *testing.T) {
	savedProbe := doctor.PrePushGateProbeOverride
	doctor.PrePushGateProbeOverride = nil
	t.Cleanup(func() { doctor.PrePushGateProbeOverride = savedProbe })
	root := jailTest(t)
	testjail.PinClaudeAsk(t, filepath.Join(root, "home"))
	home := jailPaths(t).Home
	account := pfmconfig.DefaultAccountDir(home, 42)
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	testjail.StageGlobalAgents(t, home)
	dirs, files := storeLayout()
	testjail.StageAccountLinks(t, home, account, dirs, files)
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(account, "projects"))
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
	if err := testjail.WriteExecutable(
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

func jailTest(t *testing.T) string {
	t.Helper()
	root := testjail.InstalledHome(t)
	dirs, files := storeLayout()
	testjail.StageAccountLinks(t, filepath.Join(root, "home"), filepath.Join(root, "home", ".cc", "1"), dirs, files)
	stageStorePlugins(t, filepath.Join(root, "home"))
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

// Keep the fixture Claude pane alive until cleanup so chat open can attach.
func holdClaudeOpen(t *testing.T, root, socket string) func(format string) string {
	t.Helper()
	managed := filepath.Join(root, "home", ".local", "share", "pfm", "install", "bin", "claude")
	if err := testjail.WriteExecutable(managed, []byte("#!/bin/sh\nexec sleep 120\n"), 0o700); err != nil {
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

// Read args[0] comparisons from the dispatcher, including its != kill-exit case.
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

// Every advertised top-level subcommand must reach its dispatcher handler.
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

// Advertised internal subcommands must reach their dispatcher handlers.
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

// legacyConfigJail is a jailed installed home whose default config load finds
// no clone config while a legacy ~/.config/pfm/pfm.config.json waits.
func legacyConfigJail(t *testing.T) string {
	t.Helper()
	root := jailTest(t)
	t.Setenv(paths.EnvConfig, "")
	home := jailPaths(t).Home
	clone := filepath.Join(root, "clone") // a jailed clone with no pfm.config.json
	if err := os.MkdirAll(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(pfmconfig.LegacyConfigDir(paths.OSEnv{}, home), pfmconfig.FileName)
	if err := os.MkdirAll(filepath.Dir(legacy), 0o700); err != nil {
		t.Fatal(err)
	}
	content, err := pfmconfig.MarshalDefault(home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return legacy
}

func TestInstallRefusesWhileLegacyConfigWaits(t *testing.T) {
	legacy := legacyConfigJail(t)
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	var modes []installer.Mode
	runInstaller = func(_ context.Context, options installer.Options) (installer.Report, error) {
		modes = append(modes, options.Mode)
		return installer.Report{}, nil
	}
	for _, args := range [][]string{{"install", "--skip-harvest"}, {"install", "--yes", "--skip-harvest"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 4 {
			t.Fatalf("run(%q) code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
		want := "pfm install: BLOCK legacy-config " + legacy + " — legacy pfm config outside the clone\n"
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr=%q, want %q", stderr.String(), want)
		}
	}
	if len(modes) != 0 {
		t.Fatalf("installer modes=%v, want no installer run", modes)
	}
}

func TestDoctorReportsConfigNotMigrated(t *testing.T) {
	legacy := legacyConfigJail(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"doctor"}, &stdout, &stderr); code != 3 {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q, want failure", code, stdout.String(), stderr.String())
	}
	want := "doctor: config error=config not migrated: run pfm doctor for the fix"
	if !strings.Contains(stdout.String(), want) || !strings.Contains(stdout.String(), legacy) {
		t.Fatalf("doctor stdout=%q stderr=%q, want %q naming %s", stdout.String(), stderr.String(), want, legacy)
	}
}

// Version answers while a legacy config waits for the operator to apply doctor's fix.
func TestVersionAnswersWhileLegacyConfigWaits(t *testing.T) {
	legacyConfigJail(t)
	for _, arg := range []string{"--version", versionCommand} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s code=%d stdout=%q stderr=%q, want 0", arg, code, stdout.String(), stderr.String())
		}
		if !strings.HasPrefix(stdout.String(), "pfm ") {
			t.Fatalf("%s stdout=%q, want the pfm version line", arg, stdout.String())
		}
	}
}

// Stale needs no config during binary replacement while the legacy config waits.
func TestInternalStaleAnswersWhileLegacyConfigWaits(t *testing.T) {
	legacyConfigJail(t)
	bin := filepath.Join(jailPaths(t).Home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutable(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{"stale", "--binary", bin}
	var stdout, stderr bytes.Buffer
	code := run(append([]string{internalCommand}, args...), &stdout, &stderr)
	if strings.Contains(stderr.String(), "pfm: config:") {
		t.Fatalf("internal stale stderr=%q, want no config refusal", stderr.String())
	}
	var wantOut, wantErr bytes.Buffer
	wantCode := stale.Run(args[1:], &wantOut, &wantErr)
	if code != wantCode || stdout.String() != wantOut.String() || stderr.String() != wantErr.String() {
		t.Fatalf("internal stale code=%d stdout=%q stderr=%q, want stale.Run's code=%d stdout=%q stderr=%q",
			code, stdout.String(), stderr.String(), wantCode, wantOut.String(), wantErr.String())
	}
}

// Only stale answers config-free: every other internal command still refuses.
func TestInternalCommandsRefuseWhileLegacyConfigWaits(t *testing.T) {
	legacyConfigJail(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{internalCommand, "git-guard"}, &stdout, &stderr); code != 1 {
		t.Fatalf("internal git-guard code=%d stdout=%q stderr=%q, want 1", code, stdout.String(), stderr.String())
	}
	if want := "pfm: config: config not migrated: run pfm doctor for the fix"; !strings.HasPrefix(
		stderr.String(),
		want,
	) {
		t.Fatalf("internal git-guard stderr=%q, want %q", stderr.String(), want)
	}
}

// pfm config show keeps naming the pending legacy config; its exit stays 0.
func TestConfigShowNamesNotMigrated(t *testing.T) {
	legacyConfigJail(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{configCommand, "show"}, &stdout, &stderr); code != 0 {
		t.Fatalf("config show code=%d stdout=%q stderr=%q, want 0", code, stdout.String(), stderr.String())
	}
	if want := "pfm config show: configuration error: config not migrated"; !strings.Contains(stderr.String(), want) {
		t.Fatalf("config show stderr=%q, want %q", stderr.String(), want)
	}
}

func TestCommandsRefuseWhileLegacyConfigWaits(t *testing.T) {
	legacy := legacyConfigJail(t)
	for _, args := range [][]string{{"ls"}, {"chat", "ls"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 1 {
			t.Fatalf("ls code=%d stdout=%q stderr=%q, want 1", code, stdout.String(), stderr.String())
		}
		want := "pfm: config: config not migrated: run pfm doctor for the fix"
		if !strings.HasPrefix(stderr.String(), want) || !strings.Contains(stderr.String(), legacy) {
			t.Fatalf("ls stderr=%q, want %q naming %s", stderr.String(), want, legacy)
		}
	}
}

func TestInstallWithMissingNamedLegacyConfig(t *testing.T) {
	legacy := legacyConfigJail(t)
	if err := os.Remove(legacy); err != nil {
		t.Fatal(err)
	}
	account := pfmconfig.DefaultAccountDir(jailPaths(t).Home, 42)
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(account, "projects"))
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(context.Context, installer.Options) (installer.Report, error) { return installer.Report{}, nil }
	for _, scenario := range []struct {
		args   []string
		code   int
		prefix string
	}{
		{[]string{"install", "--skip-harvest"}, 0, "  skip    "},
		{[]string{"install", "--yes", "--skip-harvest"}, 1, "pfm install: "},
		{[]string{"install", "--check", "--skip-harvest"}, 1, "pfm install: "},
	} {
		var stdout, stderr bytes.Buffer
		args := append([]string{"--config", legacy}, scenario.args...)
		code := run(args, &stdout, &stderr)
		want := scenario.prefix + "--config " + legacy + " does not exist; refusing to converge host wiring on defaults (a missing explicit config would disable every MCP service it names)\n"
		output := stderr.String()
		if scenario.code == 0 {
			output = stdout.String()
		}
		if code != scenario.code || !strings.Contains(output, want) {
			t.Fatalf("code=%d want=%d output=%q expected=%q", code, scenario.code, output, want)
		}
	}
}

func storeLayout() ([]string, map[string]string) {
	var names []string
	files := make(map[string]string)
	for _, entry := range installer.StoreEntries {
		if entry.Dir {
			names = append(names, entry.Name)
		} else {
			files[entry.Name] = entry.Seed
		}
	}
	return names, files
}

func TestInstallerManagedCleanupOptions(t *testing.T) {
	var output bytes.Buffer
	home := t.TempDir()
	runtime := pfmconfig.Runtime{
		Paths:  paths.Values{Home: home, ManagedSettingsDir: filepath.Join(home, "managed")},
		Config: pfmconfig.Config{Claude: pfmconfig.Claude{CleanupPeriodDays: 123, RequireManagedCleanup: true}},
	}
	options := newInstallerOptions(installer.ModeDryRun, "", true, &output, &output, runtime)
	if options.ManagedSettingsDir != runtime.Paths.ManagedSettingsDir || options.CleanupPeriodDays != 123 ||
		!options.RequireManagedCleanup {
		t.Fatalf("cleanup options=%+v", options)
	}
}

func stageStorePlugins(t *testing.T, home string) {
	t.Helper()
	storeDir := installer.ClaudeStore(home)
	ids, err := installer.ClaudePluginsNotInstalled(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	testjail.StageClaudePlugins(t, storeDir, ids)
}
