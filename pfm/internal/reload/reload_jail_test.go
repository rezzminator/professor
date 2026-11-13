//go:build jail

package reload

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/cmdparse"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

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
			SessionID: "11111111-1111-4111-8111-111111111111",
			CWD:       "/jail/project", Account: 1, AccountIDs: []int{1}, Then: "S",
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

func TestClaudeRunUnsetsInheritedIdentity(t *testing.T) {
	run, err := claudeRun(Request{
		Account:   2,
		Machine:   reloadTestMachine("", t.TempDir()),
		SessionID: "11111111-1111-4111-8111-111111111111",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{"CLAUDE_CODE_SESSION_ID", "CLAUDE_CONFIG_DIR", "CACHE_LIVE_CONTROL_MAIN_TTL"} {
		if !strings.Contains(run, variable) {
			t.Fatalf("run %q does not mention %s", run, variable)
		}
	}
	if parsedReloadShell(t, run).Resume != "11111111-1111-4111-8111-111111111111" {
		t.Fatalf("run %q has no resume", run)
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
		strings.Contains(roleRun, cmdparse.Quote(promptPath)) {
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
					SessionID:  "11111111-1111-4111-8111-111111111111",
					CWD:        "/jail/project",
					Account:    2,
					AccountIDs: []int{2},
					Machine:    reloadTestMachine("", t.TempDir()),
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
				respawnEnv(t, tmux.respawn)["CACHE_LIVE_CONTROL_MAIN_TTL"] != "5m" {
				t.Fatalf("respawn resume=%q env=%#v", parsed.Resume, respawnEnv(t, tmux.respawn))
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
