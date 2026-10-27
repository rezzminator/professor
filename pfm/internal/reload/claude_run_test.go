package reload

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/action"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestClaudeRunCarriesTheChatLabel(t *testing.T) {
	home := t.TempDir()
	const id = "11111111-1111-4111-8111-111111111111"
	for _, scenario := range []struct {
		name  string
		fresh bool
		label string
	}{
		{"fresh launch with a label", true, "Fix login"},
		{"same-pane resume without a label", false, ""},
		{"fresh launch without a label", true, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			run, err := claudeRun(Request{
				Account:   2,
				Home:      home,
				Machine:   reloadTestMachine(pfmconfig.SystemPromptLean, home),
				SessionID: id,
				Name:      scenario.label,
				fresh:     scenario.fresh,
			})
			if err != nil {
				t.Fatal(err)
			}
			parsed := parsedReloadShell(t, run)
			if parsed.Name != scenario.label {
				t.Fatalf("--name = %q, want %q", parsed.Name, scenario.label)
			}
			if scenario.label == "" && strings.Contains(run, "'--name'") {
				t.Fatal("an unlabelled reload carries --name")
			}
			if scenario.fresh && parsed.SessionID != id || !scenario.fresh && parsed.Resume != id {
				t.Fatalf("reload lost its session identity: session %q resume %q", parsed.SessionID, parsed.Resume)
			}
		})
	}
}

type claimedReloadTmux struct {
	fakeReloadTmux
	t          *testing.T
	account    string
	crossed    bool
	failure    string
	cleanupErr bool
}

func (tmux *claimedReloadTmux) Respawn(ctx context.Context, socket, pane, cwd, command string) error {
	// One non-blocking flock of the account directory, the lock the guard
	// takes: AcquireAccountGuard would retry a held lock for accountBusyGrace.
	probe, err := os.Open(tmux.account)
	if err != nil {
		tmux.t.Fatal(err)
	}
	err = syscall.Flock(int(probe.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err == nil {
		tmux.crossed = true
	} else if !errors.Is(err, syscall.EWOULDBLOCK) {
		tmux.t.Fatal(err)
	}
	if err := probe.Close(); err != nil {
		tmux.t.Fatal(err)
	}
	if command == "exit 1" {
		if tmux.cleanupErr {
			return errors.New("cleanup refused")
		}
		return nil
	}
	if tmux.failure == "create" {
		return errors.New("respawn client failed")
	}
	return tmux.fakeReloadTmux.Respawn(ctx, socket, pane, cwd, command)
}

func (tmux *claimedReloadTmux) ListPanes(ctx context.Context, socket string) ([]Pane, error) {
	if tmux.failure == "query" {
		return nil, errors.New("pane query failed")
	}
	panes, err := tmux.fakeReloadTmux.ListPanes(ctx, socket)
	if tmux.respawn != "" {
		panes[0].PID = os.Getpid()
		if tmux.failure == "record" {
			panes[0].PID = 0
		}
	}
	return panes, err
}

func TestReloadClaimsAccountBeforeRespawnAndSessionMarkers(t *testing.T) {
	account := t.TempDir()
	tmux := &claimedReloadTmux{t: t, account: account}
	_, err := Run(context.Background(), Request{
		Engine:     pfmengine.Claude,
		SocketPath: "/tmp/reload-claim",
		Pane:       "%7",
		SessionID:  "11111111-1111-4111-8111-111111111111",
		Account:    1,
		AccountIDs: []int{1},
		Machine:    pfmconfig.Config{Accounts: []pfmconfig.Account{{ID: 1, ConfigDir: account}}},
	}, Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2}, tmux, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if tmux.crossed {
		t.Fatal("install crossed the account ownership boundary during reload respawn")
	}
	guard, err := gather.AcquireAccountGuard(account, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := guard.Close(); err != nil {
			t.Error(err)
		}
	}()
	live, err := guard.Active(gather.NewProcFS(""))
	if err != nil || len(live) != 1 || live[0] != os.Getpid() {
		t.Fatalf("reload lacks pre-marker live claim: %v %v", live, err)
	}
}

func TestReloadFailedClaimCleanupPreservesUntrackedChildren(t *testing.T) {
	for _, failure := range []string{"create", "query", "record"} {
		for _, cleanupErr := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/cleanup_failure_%t", failure, cleanupErr), func(t *testing.T) {
				account := t.TempDir()
				tmux := &claimedReloadTmux{t: t, account: account, failure: failure, cleanupErr: cleanupErr}
				err := respawnClaimedPane(
					context.Background(),
					tmux,
					Request{SocketPath: "/tmp/reload-claim", Pane: "%7"},
					"env CLAUDE_CONFIG_DIR="+account+" claude",
				)
				if err == nil {
					t.Fatal("failed reload reported success")
				}
				guard, err := gather.AcquireAccountGuard(account, false)
				if err != nil {
					t.Fatalf("failed reload held account lock: %v", err)
				}
				defer func() {
					if err := guard.Close(); err != nil {
						t.Error(err)
					}
				}()
				live, claimErr := guard.Active(gather.NewProcFS(""))
				if cleanupErr {
					if claimErr == nil {
						t.Fatal("untracked child lost protection")
					}
				} else if claimErr != nil || len(live) != 0 {
					t.Fatalf("terminated child claim remained: %v %v", live, claimErr)
				}
			})
		}
	}
}

func TestRunTellsTheStarterWhenTheReloadedChatLosesTheFleetPrompt(t *testing.T) {
	staged := func(t *testing.T, home string) {
		t.Helper()
		if err := paths.WriteSourceRepoMarker(home, t.TempDir()); err != nil {
			t.Fatal(err)
		}
		promptPath, err := action.ProfessorPromptPath(home)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(promptPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(promptPath, []byte("professor prompt\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name    string
		policy  string
		stage   bool
		warning bool
		// terminal: stdin and stderr are a terminal; wait: held for Enter before the respawn.
		terminal, wait bool
	}{
		{"professor policy with no source clone", pfmconfig.SystemPromptProfessor, false, true, true, true},
		{"professor policy with no source clone off a terminal", pfmconfig.SystemPromptProfessor, false, true, false, false},
		{"professor policy with the composed prompt staged", pfmconfig.SystemPromptProfessor, true, false, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv(paths.EnvHome, t.TempDir())
			t.Setenv(paths.EnvStateDB, filepath.Join(t.TempDir(), "pfm.db"))
			home := t.TempDir()
			if test.stage {
				staged(t, home)
			}
			tmux := &fakeReloadTmux{}
			var stderr bytes.Buffer
			enter := &enterProbe{tmux: tmux, data: strings.NewReader("\n")}
			t.Cleanup(launchnotice.Replace(enter, test.terminal))
			_, err := Run(
				context.Background(),
				Request{
					Engine: pfmengine.Claude, SocketPath: "/tmp/tmux-1000/probe-reload", Pane: "%7",
					SessionID: "11111111-1111-4111-8111-111111111111", CWD: "/jail/project",
					Account: 2, AccountIDs: []int{2}, Home: home, Machine: reloadTestMachine(test.policy, home),
				},
				Options{SIDDir: t.TempDir(), Delay: -1, Poll: -1, ExitTries: 2},
				tmux, nil, &stderr,
			)
			if err != nil {
				t.Fatal(err)
			}
			if enter.read != test.wait || enter.respawnedFirst {
				t.Fatalf(
					"read Enter = %t (after the respawn: %t), want %t before it",
					enter.read,
					enter.respawnedFirst,
					test.wait,
				)
			}
			const want = "Claude started WITHOUT its system prompt file"
			if got := strings.Contains(stderr.String(), want); got != test.warning {
				t.Fatalf("stderr warns=%t, want %t: %q", got, test.warning, stderr.String())
			}
			shown := false
			for _, display := range tmux.displays {
				shown = shown || strings.Contains(display, want)
			}
			if shown != test.warning {
				t.Fatalf("pane display warns=%t, want %t: %q", shown, test.warning, tmux.displays)
			}
		})
	}
	// A Codex chat's prompt rides its own config, never the Claude prompt file:
	// reloading one under the professor policy with no clone recorded is not a
	// Claude launch that lost its prompt.
	t.Run("codex reload under professor policy with no source clone", func(t *testing.T) {
		home := t.TempDir()
		var stderr bytes.Buffer
		if warning := warnPromptMissing(Request{
			Engine: pfmengine.Codex, SocketPath: "/tmp/tmux-1000/probe-reload", Pane: "%7",
			SessionID: "11111111-1111-4111-8111-111111111111", Account: 2, Home: home,
			Machine: reloadTestMachine(pfmconfig.SystemPromptProfessor, home),
		}, &stderr); warning != "" {
			t.Fatalf("codex reload warned %q", warning)
		}
		if stderr.Len() != 0 {
			t.Fatalf("codex reload told stderr=%q, want nothing", stderr.String())
		}
	})
}

// enterProbe is the stdin a reload reads its acknowledgement from; it notes
// whether the pane had already been respawned when it was read.
type enterProbe struct {
	tmux           *fakeReloadTmux
	data           *strings.Reader
	read           bool
	respawnedFirst bool
}

func (probe *enterProbe) Read(p []byte) (int, error) {
	probe.read, probe.respawnedFirst = true, probe.tmux.respawn != ""
	return probe.data.Read(p)
}
