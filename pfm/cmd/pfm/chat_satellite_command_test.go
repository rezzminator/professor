package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestChatSaveUsesConfiguredImplicitAccountRoot(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "account", "projects")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	slug := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	const id = "40404040-4040-4040-8040-404040404040"
	transcriptPath := filepath.Join(projects, slug, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		transcriptPath,
		[]byte(`{"type":"user","message":{"content":"configured transcript"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", id)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	target := filepath.Join(root, "saved.md")
	runtime := commandRuntime{
		Paths: paths.Values{
			Home:  filepath.Join(root, "home"),
			Roots: map[pfmengine.ID][]string{pfmengine.Claude: {projects}},
		},
	}
	var stdout, stderr bytes.Buffer
	if code := runChatSave([]string{target}, &stdout, &stderr, nil, runtime); code != 0 {
		t.Fatalf("save code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	saved, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "Source: "+transcriptPath) {
		t.Fatalf("saved transcript source=%q, want %q", string(saved), transcriptPath)
	}
}

func TestChatSaveDispatchUsesScopedCallerRepository(t *testing.T) {
	root := t.TempDir()
	transcriptPath := filepath.Join(root, "caller.jsonl")
	if err := os.WriteFile(
		transcriptPath,
		[]byte(`{"type":"user","message":{"content":"scoped save"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "saved.md")
	callerCWD := filepath.Join(root, "caller-repository")
	if err := os.MkdirAll(callerCWD, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := (deps.RealRunner{}).Run(context.Background(), []string{
		deps.Executable("git"), "init", "-b", "caller-snapshot", callerCWD,
	}, deps.RunOptions{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("initialize caller repository: result=%+v err=%v", result, err)
	}
	ctx := pfmchat.WithResolvedSelf(context.Background(), headless.Chat{CWD: callerCWD})
	var stdout, stderr bytes.Buffer
	if code := runChatWithRuntime(
		[]string{"save", target, transcriptPath},
		strings.NewReader(""),
		&stdout,
		&stderr,
		commandRuntime{},
		ctx,
	); code != 0 {
		t.Fatalf("save code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	saved, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(saved), "Branch: caller-snapshot\n") {
		t.Fatalf("saved snapshot = %q, want caller repository branch from %q", saved, callerCWD)
	}
}

func TestChatSaveContextKeepsExplicitTranscriptReadFailuresVisible(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "saved.md")
	missingTranscript := filepath.Join(root, "missing.jsonl")
	ctx := pfmchat.WithResolvedSelf(context.Background(), headless.Chat{CWD: root})
	var stdout, stderr bytes.Buffer

	code := runChatSaveContext(
		ctx,
		[]string{target, missingTranscript},
		&stdout,
		&stderr,
		nil,
		commandRuntime{},
	)
	if code != 1 || !strings.Contains(stderr.String(), missingTranscript) {
		t.Fatalf(
			"save code=%d stdout=%q stderr=%q, want named transcript read failure",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("target stat error = %v, want no output after transcript read failure", err)
	}
}

func TestCurrentClaudeModelUsesConfiguredAccountRoot(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "account", "projects")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	slug := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	const id = "50505050-5050-4050-8050-505050505050"
	transcriptPath := filepath.Join(projects, slug, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		transcriptPath,
		[]byte(`{"type":"assistant","message":{"model":"claude-opus-5"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	got := currentClaudeModel(id, nil, commandRuntime{Paths: paths.Values{
		Home:  filepath.Join(root, "home"),
		Roots: map[pfmengine.ID][]string{pfmengine.Claude: {projects}},
	}})
	if got != "opus[1m]" {
		t.Fatalf("currentClaudeModel()=%q, want opus[1m]", got)
	}
}

func TestChatLSUsesConfiguredAccountRoots(t *testing.T) {
	root := jailTest(t)
	accountRoot := filepath.Join(root, "configured-account")
	projects := filepath.Join(accountRoot, "projects")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	slug := strings.NewReplacer("/", "-", ".", "-").Replace(cwd)
	const id = "60606060-6060-4060-8060-606060606060"
	transcriptPath := filepath.Join(projects, slug, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","cwd":"` + cwd + `","message":{"content":"configured chat ls transcript"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := writeConfigFixture(t, root, `{"version":1,"accounts":[{"id":9,"configDir":"`+accountRoot+`"}]}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--config", configPath, "chat", "ls", "--all"}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat ls code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	database, err := store.Open(store.WithWarningWriter(&bytes.Buffer{}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	row, found, err := database.Transcript(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if !found || row.Path != transcriptPath {
		t.Fatalf(
			"indexed transcript found=%t path=%q, want found at %q; stdout=%q stderr=%q",
			found,
			row.Path,
			transcriptPath,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestChatHistoryUsesConfiguredRootsToResolveTheTranscript(t *testing.T) {
	root := t.TempDir()
	projects := filepath.Join(root, "account", "projects")
	const slug = "-fixture-project"
	const id = "70707070-7070-4070-8070-707070707070"
	transcriptPath := filepath.Join(projects, slug, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	// A leading line is always dropped as possibly-partial (history.sh's own
	// `tail -n 800 | sed 1d`), so the fixture carries one throwaway record
	// ahead of the message actually under test.
	lines := `{"type":"user","message":{"content":"dropped as the possibly-partial first line"}}` + "\n" +
		`{"type":"user","timestamp":"2026-01-02T03:04:05Z","message":{"content":"configured roots transcript"}}` + "\n"
	if err := os.WriteFile(transcriptPath, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	// A pool other than the configured one must never be consulted: only the
	// runtime's own Claude roots resolve the sid, exactly what runChatScript
	// used to marshal into PFM_HISTORY_ROOTS_JSON for the retired history.sh.
	t.Setenv("PFM_HOME", filepath.Join(root, "unused-home"))
	runtime := commandRuntime{Paths: paths.Values{Roots: map[pfmengine.ID][]string{pfmengine.Claude: {projects}}}}
	var stdout, stderr bytes.Buffer
	if code := runChatSatellite(
		"history",
		[]string{id, "5", slug},
		strings.NewReader(""),
		&stdout,
		&stderr,
		nil,
		nil,
		runtime,
	); code != 0 {
		t.Fatalf("history code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), transcriptPath) {
		t.Fatalf(
			"history did not resolve through the configured root: stdout=%q stderr=%q",
			stdout.String(),
			stderr.String(),
		)
	}
	if !strings.Contains(stdout.String(), "configured roots transcript") {
		t.Fatalf("history did not render the transcript message: %q", stdout.String())
	}
}

func TestChatFindSearchesEveryConfiguredTranscriptRegistry(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	registry := filepath.Join(root, "registry")
	project := filepath.Join(registry, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "30303030-3030-4030-8030-303030303030"
	line := `{"type":"user","timestamp":"2026-01-02T03:04:05Z","message":{"content":"a long distinctive sentence carried across the registry"}}` + "\n"
	if err := os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	excerpt := filepath.Join(root, "excerpt.txt")
	if err := os.WriteFile(
		excerpt,
		[]byte("a long distinctive sentence carried across the registry\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_CLAUDE_ROOTS", registry)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "find", excerpt}, &stdout, &stderr); code != 0 {
		t.Fatalf("find code=%d stderr=%q", code, stderr.String())
	}
	if got, want := stdout.String(), id+"\t"+filepath.Join(project, id+".jsonl")+"\n"; got != want {
		t.Fatalf("stdout=%q, want %q", got, want)
	}
	if !strings.Contains(stderr.String(), "(1/1 needles hit)") {
		t.Fatalf("stderr=%q, want the hit/needle proof", stderr.String())
	}
}

// TestChatReadExcerptWritesUnderSIDDirNotCWDTmp is 1-c's regression test:
// unfixed, `pfm chat read <excerpt-file>` writes to the cwd-relative
// "tmp/chat-loads" — this test's cwd is a fresh temp dir unrelated to
// PFM_SID_DIR, so a stray tmp/ appears there and the printed path is
// cwd-relative, not the absolute SIDDir-rooted one.
func TestChatReadExcerptWritesUnderSIDDirNotCWDTmp(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	registry := filepath.Join(root, "registry")
	project := filepath.Join(registry, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "50505050-5050-4050-8050-505050505050"
	line := `{"type":"user","timestamp":"2026-01-02T03:04:05Z","message":{"content":"a long distinctive sentence carried for chat read"}}` + "\n"
	if err := os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	excerpt := filepath.Join(root, "excerpt.txt")
	if err := os.WriteFile(
		excerpt,
		[]byte("a long distinctive sentence carried for chat read\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	sidDir := filepath.Join(root, "sid")
	t.Setenv("PFM_HOME", home)
	t.Setenv("PFM_CLAUDE_ROOTS", registry)
	t.Setenv("PFM_SID_DIR", sidDir)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")

	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "read", excerpt}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat read code=%d stderr=%q", code, stderr.String())
	}
	wantOut := filepath.Join(sidDir, "chat-loads", id+".md")
	if !strings.Contains(stdout.String(), "Extracted -> "+wantOut+" (") {
		t.Fatalf("stdout=%q, want the absolute SIDDir-rooted path %q", stdout.String(), wantOut)
	}
	content, err := os.ReadFile(wantOut)
	if err != nil {
		t.Fatalf("excerpt not written under SIDDir/chat-loads: %v", err)
	}
	if !strings.Contains(string(content), "# Loaded chat — session "+id) {
		t.Fatalf("excerpt content=%q, missing header", content)
	}
	if _, err := os.Stat(filepath.Join(cwd, "tmp")); !os.IsNotExist(err) {
		t.Fatalf("chat read left a cwd-relative tmp/: err=%v", err)
	}
}

// TestChatReadExcerptResolveFailureNeverFallsBackToCWD pins the named-error
// path: an empty runtime.Paths.SIDDir sends chat read to paths.Resolve() —
// when that resolution itself fails (here, the test-jail refusal to touch a
// real operator HOME), chat read names the failure and writes nothing,
// rather than silently falling back to a cwd-relative tmp/.
func TestChatReadExcerptResolveFailureNeverFallsBackToCWD(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	registry := filepath.Join(root, "registry")
	project := filepath.Join(registry, "project")
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	const id = "60606060-6060-4060-8060-606060606060"
	line := `{"type":"user","timestamp":"2026-01-02T03:04:05Z","message":{"content":"a distinctive sentence for a resolve failure"}}` + "\n"
	if err := os.WriteFile(filepath.Join(project, id+".jsonl"), []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	excerpt := filepath.Join(root, "excerpt.txt")
	if err := os.WriteFile(
		excerpt,
		[]byte("a distinctive sentence for a resolve failure\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	// The package jail already pins PFM_HOME for the whole test binary
	// (internal/testjail.jailHome); override it back to empty here so
	// paths.Resolve() (reached because the runtime below carries no SIDDir)
	// hits the test-jail refusal to touch a real operator HOME
	// (internal/paths.HomeFrom) and returns an error.
	t.Setenv("PFM_HOME", "")
	runtime := commandRuntime{
		Paths: paths.Values{
			Home:  home,
			Roots: map[pfmengine.ID][]string{pfmengine.Claude: {registry}},
		},
	}

	cwd := t.TempDir()
	t.Chdir(cwd)

	var stdout, stderr bytes.Buffer
	code := runChatReadExcerpt([]string{excerpt}, &stdout, &stderr, runtime)
	if code != 1 {
		t.Fatalf(
			"code=%d, want 1 on an unresolvable scratch root; stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if !strings.Contains(stderr.String(), "pfm chat read: resolve output directory:") {
		t.Fatalf("stderr=%q, want the named resolve-output-directory error", stderr.String())
	}
	if _, err := os.Stat(filepath.Join(cwd, "tmp")); !os.IsNotExist(err) {
		t.Fatalf("chat read left a cwd-relative tmp/ after a resolve failure: err=%v", err)
	}
}

// TestChatLoadVerbIsRetired pins that `pfm chat load` is gone: the verb must
// fail as an unknown command, never enumerate files.
func TestChatLoadVerbIsRetired(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.txt"), []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "load", root}, &stdout, &stderr); code != 2 {
		t.Fatalf("chat load code=%d stdout=%q stderr=%q, want 2", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), `pfm chat: unknown command "load"`) {
		t.Fatalf("stderr=%q, want the unknown-command refusal", stderr.String())
	}
	if strings.Contains(stdout.String(), "one.txt") {
		t.Fatalf("stdout=%q, a retired verb must not enumerate files", stdout.String())
	}
}

// TestChatLSHeaderNamesBothViewVariants pins the header text runChatLS
// prints for each view: the in-repo view names name/session/state/activity,
// and --all additionally names dir — both now lead with "name" ahead of
// "session", matching the row format below.
func TestChatLSHeaderNamesBothViewVariants(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "ls"}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat ls code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "live chats in this repo (name · session · state · last activity):") {
		t.Fatalf("chat ls header = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "(none)") {
		t.Fatalf("chat ls with no live chats = %q, want (none)", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"chat", "ls", "--all"}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat ls --all code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "live chats everywhere (name · session · state · dir · last activity):") {
		t.Fatalf("chat ls --all header = %q", stdout.String())
	}
}

// TestChatLSPrintsNameBeforeSessionAtAndOverTruncationBoundary is the
// red-first proof for the NAME-primary row format: two real live tmux
// panes get distinct pane-title-derived names, one exactly at the 28-char
// truncation boundary and one well over it. For both rows the printed line
// must carry the (possibly truncated) NAME first, at a fixed column, with
// the tmux session id still present immediately after it — never lost,
// never reordered, and never pushed out of alignment by an oversized name.
func TestChatLSPrintsNameBeforeSessionAtAndOverTruncationBoundary(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	jail := newKillCLIJail(t)
	sidDir := filepath.Join(jail.root, "sid")

	epoch := strconv.FormatInt(time.Now().Unix(), 10)
	pid := strconv.Itoa(os.Getpid())
	exactSocket := "cc-" + epoch + "-" + pid + "-1"
	overSocket := "cc-" + epoch + "-" + pid + "-2"
	exactName := strings.Repeat("A", 28) // exactly at the truncation boundary
	overName := strings.Repeat("B", 40)  // well over it

	seat := func(socket, title string) {
		startBootingPane(t, socket)
		paneID := strings.TrimSpace(runTmuxOutput(t, socket, "list-panes", "-F", "#{pane_id}"))
		if output, err := exec.Command(
			"tmux", "-L", socket, "select-pane", "-t", paneID, "-T", title,
		).CombinedOutput(); err != nil {
			t.Fatalf("set pane title for %q: %v: %s", socket, err, output)
		}
		crumb := filepath.Join(sidDir, socket+"."+paneID)
		if err := os.WriteFile(crumb, []byte("/nonexistent/"+socket+".jsonl\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	seat(exactSocket, exactName)
	seat(overSocket, overName)

	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "ls", "--all"}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat ls --all code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}

	findRow := func(socket string) string {
		for _, line := range strings.Split(stdout.String(), "\n") {
			if strings.Contains(line, socket) {
				return line
			}
		}
		t.Fatalf("no row for socket %q in stdout=%q", socket, stdout.String())
		return ""
	}

	check := func(socket, fullName, wantTruncated string) {
		line := findRow(socket)
		const nameStart, nameWidth = 2, 28
		if len(line) < nameStart+nameWidth+1 {
			t.Fatalf("row for %q too short to hold the name column: %q", socket, line)
		}
		if got := line[nameStart : nameStart+nameWidth]; got != wantTruncated {
			t.Fatalf("row for %q name column = %q, want %q (line=%q)", socket, got, wantTruncated, line)
		}
		if line[nameStart+nameWidth] != ' ' {
			t.Fatalf("row for %q has no delimiter after the name column: %q", socket, line)
		}
		sessionStart := nameStart + nameWidth + 1
		if got := line[sessionStart : sessionStart+len(socket)]; got != socket {
			t.Fatalf(
				"row for %q session id at column %d = %q, want %q — name column pushed it out of alignment: %q",
				socket,
				sessionStart,
				got,
				socket,
				line,
			)
		}
		nameIdx := strings.Index(line, wantTruncated)
		sessionIdx := strings.Index(line, socket)
		if nameIdx < 0 || sessionIdx < 0 || nameIdx >= sessionIdx {
			t.Fatalf(
				"row for %q: name must appear before session id — name@%d session@%d line=%q",
				socket,
				nameIdx,
				sessionIdx,
				line,
			)
		}
		if len(fullName) > nameWidth && strings.Contains(stdout.String(), fullName) {
			t.Fatalf("full untruncated name %q leaked into stdout — truncation did not run", fullName)
		}
	}
	check(exactSocket, exactName, exactName)
	check(overSocket, overName, overName[:28])
}
