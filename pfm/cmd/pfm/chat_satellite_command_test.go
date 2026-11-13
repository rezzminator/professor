package main

import (
	"bytes"
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pfmchat "github.com/rezzminator/professor/pfm/internal/chat"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
)

func TestHistoryPoolsUsesOnlySharedClaudeRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv(paths.EnvHome, home)
	account := filepath.Join(home, ".cc", "2", "projects")
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(pfmengine.MustLookup(pfmengine.Codex).RootEnv, "")
	got, codex, err := historyPools()
	if err != nil {
		t.Fatal(err)
	}
	want, wantCodex := filepath.Join(home, ".claude", "projects"), filepath.Join(home, ".codex")
	if len(got) != 1 || got[0] != want || len(codex) != 1 || codex[0] != wantCodex {
		t.Fatalf("history roots = %q and Codex homes %q, want [%q] and [%q]", got, codex, want, wantCodex)
	}
}

func TestChatSaveWithoutConfigDirUsesTheConfiguredRoot(t *testing.T) {
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
	projects := jailPaths(t).Roots[pfmengine.Claude][0]
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

// TestChatHistoryRendersACodexRollout: a rollout path is read as Codex, its
// harness instructions and tool calls left out, never as an empty Claude file.
func TestChatHistoryRendersACodexRollout(t *testing.T) {
	codexHome := t.TempDir()
	path := filepath.Join(codexHome, "sessions", "2026", "01", "02",
		"rollout-2026-01-02T03-04-05-71717171-7171-4171-8171-717171717171.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join([]string{
		`{"timestamp":"2026-01-02T03:04:05Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions\n\n<INSTRUCTIONS>\nbe brief\n</INSTRUCTIONS>"}]}}`,
		`{"timestamp":"2026-01-02T03:04:06Z","type":"event_msg","payload":{"type":"user_message","message":"reply with OK"}}`,
		`{"timestamp":"2026-01-02T03:04:07Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{}"}}`,
		`{"timestamp":"2026-01-02T03:04:08Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"OK"}]}}`,
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	want := "== " + path + " · last 5 messages ==\n" +
		"\n───── 2026-01-02T03:04:06Z · user ─────\nreply with OK\n" +
		"\n───── 2026-01-02T03:04:08Z · assistant ─────\nOK\n"
	runtime := commandRuntime{Paths: paths.Values{Roots: map[pfmengine.ID][]string{
		pfmengine.Claude: {t.TempDir()}, pfmengine.Codex: {codexHome},
	}}}
	// By its path, and by the Codex thread id alone: no Claude pool holds it, the Codex homes do.
	for _, sid := range []string{path, "71717171-7171-4171-8171-717171717171"} {
		var stdout, stderr bytes.Buffer
		code := runChatSatellite("history", []string{sid, "5", "-fixture-project"}, strings.NewReader(""), &stdout,
			&stderr, nil, nil, runtime)
		if code != 0 || stdout.String() != want {
			t.Fatalf("history %s code=%d stdout=%q, want 0 and %q; stderr=%q", sid, code, stdout.String(), want,
				stderr.String())
		}
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
	if !strings.Contains(stdout.String(), "live chats in this repo (name · session · state · herdr · last activity):") {
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
	if !strings.Contains(
		stdout.String(),
		"live chats everywhere (name · session · state · herdr · dir · last activity):",
	) {
		t.Fatalf("chat ls --all header = %q", stdout.String())
	}
}

// steppingClock advances its fake time on every Sleep, so a poll loop runs to
// its deadline without real sleeping; onSleep sees the time after each step.
type steppingClock struct {
	*clock.Fake
	onSleep func(now time.Time)
}

func (c *steppingClock) Sleep(_ context.Context, d time.Duration) error {
	c.Advance(d)
	if c.onSleep != nil {
		c.onSleep(c.Now())
	}
	return nil
}

func TestWaitForBranchSessionIDGivesUpAtThirtySecondsNamingNoLaunchRecord(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	clk := &steppingClock{Fake: clock.NewFake(start)}
	id, err := waitForBranchSessionID(
		context.Background(), clk, t.TempDir(), "pfm-fork", "parent", branchForkSessionIDTimeout,
	)
	if id != "" {
		t.Fatalf("id = %q, want empty on timeout", id)
	}
	if elapsed := clk.Now().Sub(start); elapsed < 30*time.Second || elapsed > 31*time.Second {
		t.Fatalf("gave up after %s of fake time, want 30s", elapsed)
	}
	if err == nil || !strings.Contains(err.Error(), "no launch record written") ||
		!strings.Contains(err.Error(), "pfm-fork") || !strings.Contains(err.Error(), "30s") {
		t.Fatalf("err = %v, want a named 30s timeout carrying the socket and the missing launch record", err)
	}
}

func TestWaitForBranchSessionIDReturnsAnIDThatAppearsAtTenSeconds(t *testing.T) {
	start := time.Unix(1_700_000_000, 0)
	sidDir := t.TempDir()
	const forkID = "11111111-2222-3333-4444-555555555555"
	clk := &steppingClock{Fake: clock.NewFake(start)}
	clk.onSleep = func(now time.Time) {
		if now.Sub(start) >= 10*time.Second {
			crumb := filepath.Join(sidDir, "pfm-fork")
			if err := os.WriteFile(crumb, []byte("/transcripts/"+forkID+".jsonl\n"), 0o600); err != nil {
				t.Errorf("write crumb: %v", err)
			}
		}
	}
	id, err := waitForBranchSessionID(
		context.Background(), clk, sidDir, "pfm-fork", "parent", branchForkSessionIDTimeout,
	)
	if err != nil || id != forkID {
		t.Fatalf("id, err = %q, %v; want %q, nil", id, err, forkID)
	}
	if elapsed := clk.Now().Sub(start); elapsed < 10*time.Second || elapsed > 11*time.Second {
		t.Fatalf("resolved after %s of fake time, want about 10s", elapsed)
	}
}

func TestChatLSNativeUnreachableIsVisibleAndNonzero(t *testing.T) {
	jailTest(t)
	values := jailPaths(t)
	state := fleetdb.OpenSharedState(context.Background(), values)
	defer func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	}()
	// A socket file nothing listens on: Herdr cannot be read, never proved gone.
	dead := filepath.Join(t.TempDir(), "dead.sock")
	listener, err := net.Listen("unix", dead)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	if err := state.PutSeat(
		context.Background(),
		fleetdb.Seat{
			SeatID:         "native-offline",
			SessionID:      "native-offline-session",
			Engine:         "cc",
			Backend:        "herdr",
			State:          "live",
			Name:           "OFFLINE",
			LostNote:       "your last message was not answered — re-send it",
			HerdrSocket:    dead,
			HerdrPane:      "w6:p3",
			HerdrWorkspace: "w6",
		},
	); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "ls", "--all"}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stdout.String(), "OFFLINE") ||
		!strings.Contains(
			stderr.String(),
			"HERDR UNREACHABLE",
		) || !strings.Contains(stdout.String(), "loss note: your last message was not answered — re-send it") {
		t.Fatalf("unreachable list code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
