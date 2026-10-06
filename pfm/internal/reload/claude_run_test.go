package reload

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

func TestClaudeRunCarriesTheChatLabel(t *testing.T) {
	home := t.TempDir()
	const id = "11111111-1111-4111-8111-111111111111"
	for _, scenario := range []struct {
		name  string
		fresh bool
		label string
	}{
		{"same-pane resume with a label", false, "Fix login"},
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
	guard, err := gather.AcquireAccountGuard(tmux.account, false)
	if err == nil {
		tmux.crossed = true
		if err := guard.Close(); err != nil {
			tmux.t.Fatal(err)
		}
	} else if !errors.Is(err, syscall.EWOULDBLOCK) {
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
