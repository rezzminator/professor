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

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
	"hostops/pfm/internal/store"
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
	if code := runChatSave([]string{target}, &stdout, &stderr, runtime); code != 0 {
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
	got := currentClaudeModel(id, commandRuntime{Paths: paths.Values{
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
