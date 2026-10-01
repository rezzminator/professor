package reload

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func parsedReloadShell(t *testing.T, run string) claudelaunch.Parsed {
	t.Helper()
	output, err := exec.Command("sh", "-c", "set -- "+run+"; printf '%s\\000' \"$@\"").Output()
	if err != nil {
		t.Fatal(err)
	}
	words := strings.Split(strings.TrimSuffix(string(output), "\x00"), "\x00")
	parsed, err := claudelaunch.Parse(append([]string{"claude"}, words...))
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

type fakeReloadTmux struct {
	dead       bool
	literal    string
	respawn    string
	respawnCWD string
	displays   []string
	selector   bool
	displayErr error
}

func (tmux *fakeReloadTmux) ListPanes(context.Context, string) ([]Pane, error) {
	return []Pane{{ID: "%7", Dead: tmux.dead, PID: 700}}, nil
}
func (*fakeReloadTmux) SetRemain(context.Context, string, string, bool) error { return nil }
func (*fakeReloadTmux) PaneInMode(context.Context, string, string) (bool, error) {
	return false, nil
}
func (*fakeReloadTmux) CancelMode(context.Context, string, string) error { return nil }
func (tmux *fakeReloadTmux) Capture(context.Context, string, string) (string, error) {
	if tmux.selector {
		return "Claude\n❯ \n❯ 1. choose", nil
	}
	if tmux.literal == "/exit" {
		return "Claude\n❯ /exit", nil
	}
	return "Claude\n❯ ", nil
}

func (tmux *fakeReloadTmux) CaptureStyled(ctx context.Context, socket, pane string) (string, error) {
	return tmux.Capture(ctx, socket, pane)
}

func (tmux *fakeReloadTmux) SendKey(_ context.Context, _, _, key string) error {
	if key == "Enter" && tmux.literal == "/exit" {
		tmux.dead = true
	}
	return nil
}

func (tmux *fakeReloadTmux) SendLiteral(_ context.Context, _, _, value string) error {
	tmux.literal = value
	return nil
}

func (tmux *fakeReloadTmux) Respawn(_ context.Context, _, _, cwd, command string) error {
	tmux.respawn = command
	tmux.respawnCWD = cwd
	tmux.dead = false
	return nil
}

func (tmux *fakeReloadTmux) Display(_ context.Context, _, _, message string) error {
	tmux.displays = append(tmux.displays, message)
	return tmux.displayErr
}

type fakeReloadProc struct {
	pids   []int
	argv   map[int][]string
	cmdErr map[int]error
	stat   map[int]gather.ProcStat
}

func (proc fakeReloadProc) PIDs() ([]int, error) { return proc.pids, nil }
func (proc fakeReloadProc) Cmdline(pid int) ([]string, error) {
	if err := proc.cmdErr[pid]; err != nil {
		return nil, err
	}
	return proc.argv[pid], nil
}
func (fakeReloadProc) Environ(int) (map[string]string, error)     { return nil, nil }
func (proc fakeReloadProc) Stat(pid int) (gather.ProcStat, error) { return proc.stat[pid], nil }

type delayedThenTmux struct {
	fakeReloadTmux
	ready     bool
	submitted bool
	marker    string
}

type delayedExitRenderTmux struct {
	fakeReloadTmux
	exitRendered     bool
	earlySubmitCount int
}

type neverExitRenderTmux struct {
	fakeReloadTmux
	enterCount int
}

func (tmux *neverExitRenderTmux) Capture(context.Context, string, string) (string, error) {
	return "Codex\n› ", nil
}

func (tmux *neverExitRenderTmux) SendKey(_ context.Context, _, _, key string) error {
	if key == "Enter" && tmux.literal == "/exit" {
		tmux.enterCount++
	}
	return nil
}

func (tmux *delayedExitRenderTmux) Capture(context.Context, string, string) (string, error) {
	if tmux.literal == "/exit" {
		tmux.exitRendered = true
		return "Codex\n› /exit", nil
	}
	return "Codex\n› ", nil
}

func (tmux *delayedExitRenderTmux) SendKey(_ context.Context, _, _, key string) error {
	if key != "Enter" || tmux.literal != "/exit" {
		return nil
	}
	if !tmux.exitRendered {
		tmux.earlySubmitCount++
		return nil
	}
	tmux.dead = true
	return nil
}

func (tmux *delayedThenTmux) Capture(context.Context, string, string) (string, error) {
	marker := tmux.marker
	if marker == "" {
		marker = "❯"
	}
	if tmux.respawn == "" {
		if tmux.literal == "/exit" {
			return "Chat\n" + marker + " /exit", nil
		}
		return "Chat\n" + marker + " ", nil
	}
	tmux.ready = true
	draft := tmux.literal
	if draft == "" || draft == "/exit" {
		return "Chat\n" + marker + " ", nil
	}
	if tmux.submitted {
		// Submitted text remains in scrollback while the active composer is
		// empty. Submit proof must inspect the composer, not the whole pane.
		return marker + " " + draft + "\nWorking\n" + wrapComposer(marker, "", 60), nil
	}
	return "Chat\n" + wrapComposer(marker, draft, 60), nil
}

// wrapComposer renders a multiline TUI draft with its marker only on row one.
func wrapComposer(marker, text string, width int) string {
	rule := strings.Repeat("─", width)
	runes := []rune(text)
	rows := []string{}
	for start := 0; start < len(runes); start += width {
		end := start + width
		if end > len(runes) {
			end = len(runes)
		}
		rows = append(rows, string(runes[start:end]))
	}
	if len(rows) == 0 {
		rows = []string{""}
	}
	block := []string{rule, marker + " " + rows[0]}
	for _, row := range rows[1:] {
		block = append(block, "  "+row)
	}
	return strings.Join(append(block, rule), "\n")
}

func (tmux *delayedThenTmux) SendKey(_ context.Context, _, _, key string) error {
	if key != "Enter" {
		return nil
	}
	switch {
	case tmux.literal == "/exit":
		tmux.dead = true
	case tmux.literal != "":
		tmux.submitted = true
	}
	return nil
}

type promptReadyProc struct{ tmux *delayedThenTmux }

func (proc promptReadyProc) PIDs() ([]int, error) {
	if !proc.tmux.ready {
		return nil, errors.New("Claude has not reached its prompt")
	}
	return []int{801}, nil
}

type respawnPIDTmux struct {
	delayedThenTmux
	oldPID int
	newPID int
}

func (tmux *respawnPIDTmux) ListPanes(context.Context, string) ([]Pane, error) {
	pid := tmux.oldPID
	if tmux.respawn != "" {
		pid = tmux.newPID
	}
	return []Pane{{ID: "%7", Dead: tmux.dead, PID: pid}}, nil
}

type respawnPromptProc struct{ tmux *respawnPIDTmux }

func (proc respawnPromptProc) PIDs() ([]int, error) {
	if !proc.tmux.ready {
		return nil, errors.New("Claude has not reached its prompt")
	}
	return []int{801}, nil
}
func (respawnPromptProc) Cmdline(int) ([]string, error) { return []string{"claude"}, nil }
func (respawnPromptProc) Environ(int) (map[string]string, error) {
	return map[string]string{}, nil
}

func (proc respawnPromptProc) Stat(pid int) (gather.ProcStat, error) {
	if pid == 801 {
		return gather.ProcStat{ParentPID: proc.tmux.newPID}, nil
	}
	return gather.ProcStat{ParentPID: 1}, nil
}
func (promptReadyProc) Cmdline(int) ([]string, error) { return []string{"claude"}, nil }
func (promptReadyProc) Environ(int) (map[string]string, error) {
	return map[string]string{}, nil
}

func (promptReadyProc) Stat(pid int) (gather.ProcStat, error) {
	if pid == 801 {
		return gather.ProcStat{ParentPID: 700}, nil
	}
	return gather.ProcStat{ParentPID: 1}, nil
}

func TestSessionFromCrumbUsesPaneSpecificIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "probe-1.%7"), []byte("/transcripts/pane.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "probe-1"), []byte("/transcripts/socket.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, path, err := SessionFromCrumb(dir, "probe-1", "%7")
	if err != nil {
		t.Fatal(err)
	}
	if id != "pane" || path != "/transcripts/pane.jsonl" {
		t.Fatalf("identity = %q/%q", id, path)
	}
}

func TestSessionFromPaneCrumbRequiresExactValidatedBinding(t *testing.T) {
	dir := t.TempDir()
	const socket = "cc-1-2-3"
	if err := os.WriteFile(filepath.Join(dir, socket+".%7"), []byte("/transcripts/pane.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, socket), []byte("/transcripts/socket.jsonl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, path, err := SessionFromPaneCrumb(dir, socket, "%7")
	if err != nil || id != "pane" || path != "/transcripts/pane.jsonl" {
		t.Fatalf("exact pane identity = %q/%q err=%v", id, path, err)
	}
	id, path, err = SessionFromPaneCrumb(dir, socket, "%8")
	if err != nil || id != "" || path != "" {
		t.Fatalf("missing pane borrowed socket fallback = %q/%q err=%v", id, path, err)
	}
	if _, _, err := SessionFromPaneCrumb(dir, socket, "bad"); !errors.Is(err, ErrInvalidPaneCrumb) {
		t.Fatalf("invalid pane error = %v, want ErrInvalidPaneCrumb", err)
	}
	if _, _, err := SessionFromPaneCrumb("relative", socket, "%7"); err == nil ||
		!strings.Contains(err.Error(), "not absolute") {
		t.Fatalf("relative breadcrumb directory error = %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, socket+".%9"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := SessionFromPaneCrumb(dir, socket, "%9"); err == nil ||
		!strings.Contains(err.Error(), "read reload breadcrumb") {
		t.Fatalf("pane breadcrumb read error = %v", err)
	}
}

func TestTranscriptCWDReadsARecordBeforeTheSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.jsonl")
	content := `{"type":"summary","message":"not a cwd"}` + "\n" + `{"cwd":"/jail/project","message":"start"}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := TranscriptCWD(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "/jail/project" {
		t.Fatalf("cwd = %q", got)
	}
}

func TestRunRefusesAnOverlappingPaneReload(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, ".probe-1.%7.reloadlock")
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	_, err = Run(
		context.Background(),
		Request{
			Engine:     pfmengine.Claude,
			SocketPath: "/tmp/probe-1",
			Pane:       "%7",
			Account:    2,
			AccountIDs: []int{2},
		},
		Options{SIDDir: dir, Delay: -1, Poll: -1, IdleTries: 3},
		nil,
		nil,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "already in flight") {
		t.Fatalf("overlap error = %v", err)
	}
}

type releasePaneLockClock struct {
	clock.Clock
	lock         *os.File
	polls        int
	beforeUnlock func() error
}

func (clk *releasePaneLockClock) Sleep(ctx context.Context, _ time.Duration) error {
	clk.polls++
	if clk.polls == 2 {
		if clk.beforeUnlock != nil {
			if err := clk.beforeUnlock(); err != nil {
				return err
			}
		}
		if err := syscall.Flock(int(clk.lock.Fd()), syscall.LOCK_UN); err != nil {
			return err
		}
	}
	return clk.Clock.Sleep(ctx, 0)
}

type queuedThenTmux struct {
	delayedThenTmux
	submissions int
}

func (tmux *queuedThenTmux) SendKey(ctx context.Context, socket, pane, key string) error {
	if key == "Enter" && tmux.literal == "S" && !tmux.submitted {
		tmux.submissions++
	}
	return tmux.delayedThenTmux.SendKey(ctx, socket, pane, key)
}

func TestRunQueuesBehindAnOverlappingPaneReload(t *testing.T) {
	dir := t.TempDir()
	root, cwd := t.TempDir(), t.TempDir()
	t.Setenv(paths.EnvHome, t.TempDir())
	t.Setenv(paths.EnvClaudeRoots, root)
	id := "22222222-2222-4222-8222-222222222222"
	if err := os.WriteFile(filepath.Join(root, id+".jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(LockPath(dir, "probe-1", "%7"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	clk := &releasePaneLockClock{Clock: clock.Real, lock: lock}
	clk.beforeUnlock = func() error {
		return writeHandoff(lock, handoffRecord{
			Engine: pfmengine.Claude, SessionID: id, Account: 2, Cache1H: true,
			CWD: cwd, WrittenAt: clock.Real.Now(),
		})
	}
	tmux := &queuedThenTmux{}
	var stderr strings.Builder
	result, err := Run(
		context.Background(),
		Request{
			Engine: pfmengine.Claude, SocketPath: "/tmp/probe-1", Pane: "%7",
			PanePID: 700, SessionID: "11111111-1111-4111-8111-111111111111",
			CWD: "/jail/project", Account: 1, AccountIDs: []int{1}, Then: "S",
			Machine: reloadTestMachine("", dir),
		},
		Options{SIDDir: dir, Delay: -1, Poll: -1, IdleTries: 5, ExitTries: 2, ThenTries: 2, Clock: clk},
		tmux, promptReadyProc{tmux: &tmux.delayedThenTmux}, &stderr,
	)
	if err != nil {
		t.Fatal(err)
	}
	if clk.polls < 2 || tmux.respawn == "" || tmux.submissions != 1 {
		t.Fatalf("polls=%d respawned=%t steer submissions=%d", clk.polls, tmux.respawn != "", tmux.submissions)
	}
	if parsedReloadShell(t, tmux.respawn).Resume != id || result.Account != 2 || !result.Cache1H ||
		result.LeftBehind != id || tmux.respawnCWD != cwd || !strings.Contains(stderr.String(), "continuing from session "+id+" on account 2") {
		t.Fatalf("queued result=%+v cwd=%q stderr=%q", result, tmux.respawnCWD, stderr.String())
	}
}

func TestRunMarksSelectorRefusalOnlyWhenPaneWasTold(t *testing.T) {
	for _, displayErr := range []error{nil, errors.New("display failed")} {
		tmux := &fakeReloadTmux{selector: true, displayErr: displayErr}
		_, err := Run(context.Background(), Request{
			Engine: pfmengine.Claude, SocketPath: "/tmp/probe-1", Pane: "%7",
			SessionID: "11111111-1111-4111-8111-111111111111", Account: 1,
			AccountIDs: []int{1}, Machine: reloadTestMachine("", t.TempDir()),
		}, Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, nil)
		if err == nil || PaneTold(err) != (displayErr == nil) || len(tmux.displays) != 1 || tmux.literal != "" {
			t.Fatalf("display=%v error=%v told=%t", displayErr, err, PaneTold(err))
		}
	}
}

func TestClaudeRunUnsetsInheritedIdentity(t *testing.T) {
	run, err := claudeRun(Request{
		Account:   2,
		Machine:   reloadTestMachine("", ""),
		SessionID: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{"CLAUDE_CODE_SESSION_ID", "CLAUDE_CONFIG_DIR", "CLAUDE_CODE_PROMPT_CACHE_TTL"} {
		if !strings.Contains(run, variable) {
			t.Fatalf("run %q does not mention %s", run, variable)
		}
	}
	if parsedReloadShell(t, run).Resume != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("run %q has no resume", run)
	}
}

func reloadTestMachine(systemPrompt, home string) pfmconfig.Config {
	return pfmconfig.Config{
		Claude: pfmconfig.ClaudePrefs{PermissionMode: pfmconfig.PermissionBypass, SystemPrompt: systemPrompt},
		Accounts: []pfmconfig.Account{
			{ID: 1, ConfigDir: filepath.Join(home, ".claude"), Implicit: true},
			{ID: 2, ConfigDir: filepath.Join(home, ".cc", "2")},
		},
	}
}

func TestClaudeRunCarriesTheConfiguredSystemPrompt(t *testing.T) {
	home := t.TempDir()
	if err := paths.WriteSourceRepoMarker(home, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	promptPath, err := action.ProfessorPromptPath(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
		t.Fatalf("stage professor prompt dir: %v", err)
	}
	if err := os.WriteFile(promptPath, []byte("professor prompt\n"), 0o644); err != nil {
		t.Fatalf("stage professor prompt file: %v", err)
	}
	professor, err := claudeRun(Request{
		Account:   2,
		Home:      home,
		Machine:   reloadTestMachine(pfmconfig.SystemPromptProfessor, home),
		SessionID: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := parsedReloadShell(t, professor).PromptFile; got != promptPath {
		t.Fatalf("reloaded chat prompt = %q", got)
	}
	if !strings.Contains(professor, "--dangerously-skip-permissions") {
		t.Fatalf("reloaded chat lost the configured autonomy posture: %q", professor)
	}
	rolePrompt := filepath.Join(home, "sid", "role-prompt-cc-reviewer.md")
	if err := os.MkdirAll(filepath.Dir(rolePrompt), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rolePrompt, []byte("role"), 0o600); err != nil {
		t.Fatal(err)
	}
	roleRun, err := claudeRun(Request{
		Account: 2, Home: home, Machine: reloadTestMachine(pfmconfig.SystemPromptProfessor, home),
		SessionID: "11111111-1111-4111-8111-111111111111", PromptChannel: rolePrompt,
	})
	if err != nil || parsedReloadShell(t, roleRun).PromptFile != rolePrompt ||
		strings.Contains(roleRun, action.Quote(promptPath)) {
		t.Fatalf("Claude role reload did not replace the ordinary prompt file: run=%q error=%v", roleRun, err)
	}

	lean, err := claudeRun(Request{
		Account:   2,
		Home:      home,
		Machine:   reloadTestMachine(pfmconfig.SystemPromptLean, home),
		SessionID: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsedReloadShell(t, lean).SettingsEnv["CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"] != "1" {
		t.Fatalf("reloaded chat lost the lean prompt arm: %q", lean)
	}

	production, err := claudeRun(Request{
		Account:   2,
		Home:      home,
		Machine:   reloadTestMachine(pfmconfig.SystemPromptProduction, home),
		SessionID: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed := parsedReloadShell(t, production); parsed.PromptFile != "" ||
		parsed.SettingsEnv["CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT"] != "" {
		t.Fatalf("production mode invented prompt material: %q", production)
	}
	if !strings.Contains(production, " -u CLAUDE_CODE_SIMPLE_SYSTEM_PROMPT ") {
		t.Fatalf("reload no longer strips an inherited lean arm: %q", production)
	}
}

func TestCodexRunUsesTheSelectedHomeAndRosterPolicy(t *testing.T) {
	prompt := strings.Repeat("0123456789abcdef", 820) + ` a triple quote """ and slash \\ survive`
	run, err := engineRun(Request{
		Engine:        "cx",
		Account:       9,
		CodexHome:     "/jail/codex/9",
		CodexBinary:   "/opt/codex safe",
		CodexYolo:     false,
		SessionID:     "019ff700-0000-7000-8000-000000000001",
		PromptChannel: prompt,
	})
	if err != nil {
		t.Fatal(err)
	}
	escaped := strings.ReplaceAll(prompt, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"""`, `\"\"\"`)
	developerInstructions := action.Quote("developer_instructions=\"\"\"\n" + escaped + "\"\"\"")
	if !strings.Contains(run, " -c "+developerInstructions+" resume ") {
		t.Fatalf("Codex role reload does not carry one complete developer-instructions value before resume: %q", run)
	}
	for _, want := range []string{
		"CODEX_HOME='/jail/codex/9'",
		"'/opt/codex safe' --sandbox workspace-write",
		"resume '019ff700-0000-7000-8000-000000000001'",
	} {
		if !strings.Contains(run, want) {
			t.Fatalf("run %q lacks %q", run, want)
		}
	}
	if strings.Contains(run, "CLAUDE_CONFIG_DIR=") {
		t.Fatalf("Codex reload inherited a Claude config assignment: %q", run)
	}
	for _, name := range claudelaunch.Hygiene() {
		if !strings.Contains(run, "-u "+name) {
			t.Fatalf("Codex reload did not strip %s: %q", name, run)
		}
	}
	roleless, err := codexRun(Request{SessionID: "019ff700-0000-7000-8000-000000000001"})
	if err != nil || strings.Contains(roleless, "developer_instructions") {
		t.Fatalf("roleless Codex reload changed: run=%q error=%v", roleless, err)
	}
}

func TestOpenCodeRunIsNotMisroutedToClaude(t *testing.T) {
	run, err := engineRun(Request{Engine: pfmengine.OpenCode})
	if err != nil {
		t.Fatal(err)
	}
	if run != "" {
		t.Fatalf("engineRun(OpenCode)=%q, want an explicit unsupported result", run)
	}
}

func TestRunRefusesOpenCodeBeforeExitingThePane(t *testing.T) {
	tmux := &fakeReloadTmux{}
	_, err := Run(
		context.Background(),
		Request{
			Engine: pfmengine.OpenCode, SocketPath: "/tmp/ox-session", Pane: "%7",
			PanePID: 700, Account: 1, AccountIDs: []int{1}, CWD: "/work",
		},
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 1},
		tmux, nil, nil,
	)
	if err == nil || !strings.Contains(err.Error(), "OpenCode") || tmux.literal != "" {
		t.Fatalf("OpenCode reload error=%v literal=%q, want refusal before /exit", err, tmux.literal)
	}
}

func TestRunGracefullyExitsThenRespawnsTheSamePane(t *testing.T) {
	for _, configKey := range []bool{false, true} {
		name := "environment"
		if configKey {
			name = "config key"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv(paths.EnvHome, t.TempDir())
			statePath := filepath.Join(t.TempDir(), "pfm.db")
			t.Setenv(paths.EnvStateDB, statePath)
			if configKey {
				configPath := filepath.Join(t.TempDir(), "pfm.config.json")
				content := []byte(`{"version":2,"state":{"db":"` + statePath + `"}}`)
				if err := os.WriteFile(configPath, content, 0o600); err != nil {
					t.Fatal(err)
				}
				t.Setenv(paths.EnvConfig, configPath)
				t.Setenv(paths.EnvStateDB, "")
				t.Setenv(paths.EnvCacheDB, "")
			}
			tmux := &fakeReloadTmux{}
			result, err := Run(
				context.Background(),
				Request{
					Engine:     pfmengine.Claude,
					SocketPath: "/tmp/tmux-1000/probe-reload",
					Pane:       "%7",
					PanePID:    700,
					SessionID:  "11111111-1111-4111-8111-111111111111",
					CWD:        "/jail/project",
					Account:    2,
					AccountIDs: []int{2},
					Machine:    reloadTestMachine("", "/jail/home"),
					Cache1H:    false,
				},
				Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2},
				tmux,
				nil,
				nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			if result.New || tmux.literal != "/exit" {
				t.Fatalf("result=%+v literal=%q", result, tmux.literal)
			}
			for _, want := range []string{
				"CLAUDE_CONFIG_DIR=",
			} {
				if !strings.Contains(tmux.respawn, want) {
					t.Fatalf("respawn %q lacks %q", tmux.respawn, want)
				}
			}
			parsed := parsedReloadShell(t, tmux.respawn)
			if parsed.Resume != "11111111-1111-4111-8111-111111111111" ||
				parsed.SettingsEnv["CACHE_LIVE_CONTROL_MAIN_TTL"] != "5m" {
				t.Fatalf("respawn resume=%q settings=%#v", parsed.Resume, parsed.SettingsEnv)
			}
			values, err := pfmconfig.ResolvePaths()
			if err != nil {
				t.Fatal(err)
			}
			launches, err := fleetdb.OpenLaunches(context.Background(), values)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := launches.Close(); err != nil {
					t.Error(err)
				}
			}()
			record, err := launches.LaunchFor(context.Background(), "11111111-1111-4111-8111-111111111111")
			if err != nil {
				t.Fatal(err)
			}
			if record.Account != 2 || record.Cache1H || record.Engine != pfmengine.Claude {
				t.Fatalf("reload record=%+v", record)
			}
		})
	}
}

func TestRunWaitsForExitTextToRenderBeforeSubmitting(t *testing.T) {
	tmux := &delayedExitRenderTmux{}
	_, err := Run(
		context.Background(),
		Request{
			Engine: pfmengine.Codex, SocketPath: "/tmp/tmux-1000/probe-reload-render", Pane: "%7",
			PanePID: 700, SessionID: "019ff700-0000-7000-8000-000000000001", CWD: "/jail/project",
			Account: 1, AccountIDs: []int{1}, CodexHome: "/jail/codex/1",
		},
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2},
		tmux,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if tmux.earlySubmitCount != 0 || !tmux.exitRendered || tmux.respawn == "" {
		t.Fatalf(
			"early submits=%d rendered=%t respawn=%q",
			tmux.earlySubmitCount, tmux.exitRendered, tmux.respawn,
		)
	}
}

func TestRunRefusesBlindExitWhenTextNeverRenders(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	tmux := &neverExitRenderTmux{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := Run(
		ctx,
		Request{
			Engine: pfmengine.Codex, SocketPath: filepath.Join(t.TempDir(), "fake-codex-socket"), Pane: "%7",
			PanePID: 700, SessionID: "019ff700-0000-7000-8000-000000000001", CWD: "/jail/project",
			Account: 1, AccountIDs: []int{1}, CodexHome: "/jail/codex/1",
		},
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 1},
		tmux,
		nil,
		nil,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("never-rendered /exit error=%v, want context deadline while waiting for visible text", err)
	}
	if tmux.enterCount != 0 || tmux.respawn != "" {
		t.Fatalf("blind exit mutation: enters=%d respawn=%q", tmux.enterCount, tmux.respawn)
	}
}

func TestRunWaitsForTheRebornPromptBeforeCheckingClaudeAndSubmittingThen(t *testing.T) {
	tmux := &delayedThenTmux{}
	_, err := Run(
		context.Background(),
		Request{
			Engine:     pfmengine.Claude,
			SocketPath: "/tmp/tmux-1000/probe-reload-then",
			Pane:       "%7",
			PanePID:    700,
			SessionID:  "11111111-1111-4111-8111-111111111111",
			CWD:        "/jail/project",
			Account:    1,
			AccountIDs: []int{1},
			Cache1H:    true,
			Then:       "continue the task",
		},
		Options{
			SIDDir:    t.TempDir(),
			Delay:     -1,
			Poll:      -1,
			ExitTries: 2,
			ThenTries: 2,
		},
		tmux,
		promptReadyProc{tmux: tmux},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !tmux.ready || !tmux.submitted {
		t.Fatalf("ready=%t submitted=%t", tmux.ready, tmux.submitted)
	}
	if len(tmux.displays) != 0 {
		t.Fatalf("successful --then displayed a failure: %q", tmux.displays)
	}
}

// The worker is spawned from inside the chat's own turn. A /exit typed while
// that turn is still rendering lands in the composer as a draft the harness
// never submits, and the pane then holds every queued prompt behind it — the
// six-hour stall. So /exit is typed only once the pane has been idle twice.
func TestRunWaitsForTheCallerTurnToEndBeforeTypingExit(t *testing.T) {
	tmux := &busyThenIdleTmux{busyCaptures: 3, typedAfter: -1}
	_, err := Run(
		context.Background(),
		reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-idle"),
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2, IdleTries: 10},
		tmux,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if tmux.typedAfter <= tmux.busyCaptures || tmux.respawn == "" {
		t.Fatalf(
			"/exit typed after %d captures with the first %d busy (want typed only once idle); respawn=%q",
			tmux.typedAfter, tmux.busyCaptures, tmux.respawn,
		)
	}
}

// A mid-turn /reload used to hold /exit with the wait logged only to the
// worker's own stderr; the pane itself must announce the wait, then the reboot.
func TestRunAnnouncesTheHoldAndTheRebootOnThePane(t *testing.T) {
	tmux := &busyThenIdleTmux{busyCaptures: 3, typedAfter: -1}
	options := Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2, IdleTries: 10}
	_, err := Run(
		context.Background(), reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-announce"), options, tmux, nil, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	wantWaiting := "pfm reload: waiting for this turn to end, then rebooting this chat"
	wantRebooting := "pfm reload: rebooting now"
	if len(tmux.displays) != 2 || tmux.displays[0] != wantWaiting || tmux.displays[1] != wantRebooting {
		t.Fatalf("pane displays=%#v, want [%q, %q]", tmux.displays, wantWaiting, wantRebooting)
	}
}

func TestRunRefusesToTypeExitIntoAChatThatStaysBusy(t *testing.T) {
	tmux := &busyThenIdleTmux{busyCaptures: 1 << 30, typedAfter: -1}
	_, err := Run(
		context.Background(),
		reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-busy"),
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2, IdleTries: 3},
		tmux,
		nil,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "still busy") {
		t.Fatalf("always-busy chat error=%v, want a 'still busy' refusal", err)
	}
	if tmux.literal != "" || tmux.typedAfter != -1 || tmux.respawn != "" {
		t.Fatalf(
			"a busy chat was touched: literal=%q typedAfter=%d respawn=%q",
			tmux.literal,
			tmux.typedAfter,
			tmux.respawn,
		)
	}
}

// A refused /exit must not leave its own text behind: the composer is
// cleared with exactly the backspaces the typed text needs, and the error
// says so — "chat left running" alone would hide a poisoned input box.
func TestRunClearsTheTypedExitWhenTheChatDoesNotDie(t *testing.T) {
	tmux := &stuckExitTmux{}
	_, err := Run(
		context.Background(),
		reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-stuck"),
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2},
		tmux,
		nil,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "did not complete") ||
		!strings.Contains(err.Error(), "cleared the typed /exit") {
		t.Fatalf("stuck /exit error=%v, want 'did not complete' plus 'cleared the typed /exit'", err)
	}
	backspaces := 0
	for _, key := range tmux.keys {
		if key == "BSpace" {
			backspaces++
		}
	}
	if backspaces != len("/exit") || tmux.literal != "" || tmux.respawn != "" {
		t.Fatalf("composer cleanup: backspaces=%d residue=%q respawn=%q", backspaces, tmux.literal, tmux.respawn)
	}
}

const exitDialog = "   Background work is running\n" +
	"   The following will stop when you exit:\n" +
	"   shell · sleep 25 && echo done\n" +
	"   ❯ 1. Exit and stop tasks\n" +
	"     2. Stay\n" +
	"   Enter to confirm · Esc to cancel\n"

// exitDialogTmux is the incident pane: Enter on the typed /exit does not exit,
// it opens Claude Code's background-work confirmation with "Exit and stop
// tasks" preselected, and only a second Enter — on the dialog — kills the
// chat. With confirms=false the dialog swallows every Enter, the way it does
// once Escape is the only key it still takes.
type exitDialogTmux struct {
	fakeReloadTmux
	confirms bool
	dialog   bool
	keys     []string
}

func (tmux *exitDialogTmux) Capture(context.Context, string, string) (string, error) {
	if tmux.dialog {
		return "Claude\n" + exitDialog, nil
	}
	return "Claude\n❯ " + tmux.literal, nil
}

func (tmux *exitDialogTmux) SendKey(_ context.Context, _, _, key string) error {
	tmux.keys = append(tmux.keys, key)
	switch {
	case key == "Enter" && tmux.dialog && tmux.confirms:
		tmux.dead = true
	case key == "Enter" && tmux.literal == "/exit" && !tmux.dialog:
		tmux.dialog = true
		tmux.literal = ""
	case key == "Escape" && tmux.dialog:
		tmux.dialog = false
	}
	return nil
}

func countKey(keys []string, want string) int {
	count := 0
	for _, key := range keys {
		if key == want {
			count++
		}
	}
	return count
}

func TestRunConfirmsTheBackgroundWorkExitDialog(t *testing.T) {
	tmux := &exitDialogTmux{confirms: true}
	_, err := Run(
		context.Background(),
		reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-dialog"),
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 3},
		tmux,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("exit dialog was not confirmed: %v (keys %v)", err, tmux.keys)
	}
	if countKey(tmux.keys, "Enter") != 2 || tmux.respawn == "" {
		t.Fatalf(
			"want exactly one submit Enter plus one confirm Enter, got keys %v respawn=%q",
			tmux.keys,
			tmux.respawn,
		)
	}
}

func TestRunDismissesAnExitDialogThatWillNotConfirm(t *testing.T) {
	tmux := &exitDialogTmux{confirms: false}
	_, err := Run(
		context.Background(),
		reloadIdleWaitRequest("/tmp/tmux-1000/probe-reload-dialog-stuck"),
		Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2},
		tmux,
		nil,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "did not complete") ||
		!strings.Contains(err.Error(), "dismissed it") {
		t.Fatalf("stuck dialog error=%v, want 'did not complete' plus 'dismissed it'", err)
	}
	if countKey(tmux.keys, "Escape") != 1 || tmux.dialog || tmux.respawn != "" {
		t.Fatalf("dialog cleanup: keys=%v dialogStillOpen=%t respawn=%q", tmux.keys, tmux.dialog, tmux.respawn)
	}
}
