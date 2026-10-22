package installer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type gatewayServiceRunner struct {
	listener   net.Listener
	state      string
	failAction string
	linger     bool
	stateErr   bool
}

func (runner *gatewayServiceRunner) Run(_ context.Context, name string, args ...string) error {
	if name != "systemctl" || len(args) < 2 {
		return fmt.Errorf("unexpected service command: %s %v", name, args)
	}
	action := args[1]
	if action == "show-environment" {
		return nil
	}
	if action == runner.failAction {
		return errors.New("service action failed")
	}
	if action != "restart" && action != "stop" {
		return fmt.Errorf("unexpected service action: %s", action)
	}
	if runner.linger {
		return nil
	}
	if err := runner.listener.Close(); err != nil {
		return fmt.Errorf("release old gateway: %w", err)
	}
	runner.state = "inactive"
	if action == "restart" {
		runner.state = "active"
	}
	return nil
}

func (runner *gatewayServiceRunner) Output(_ context.Context, _ string, _ ...string) ([]byte, error) {
	if runner.stateErr {
		return nil, errors.New("service state unreadable")
	}
	return []byte(runner.state + "\n"), nil
}

type gatewaySetupRunner struct {
	goRunner
	address string
	setups  int
}

func (runner *gatewaySetupRunner) Run(
	ctx context.Context,
	argv []string,
	options deps.RunOptions,
) (deps.RunResult, error) {
	if len(argv) > 1 && argv[1] == "setup" {
		runner.setups++
		listener, err := net.Listen("tcp", runner.address)
		if err != nil {
			return deps.RunResult{ExitCode: 1, Stderr: []byte("old gateway still occupied")}, nil
		}
		if err := listener.Close(); err != nil {
			return deps.RunResult{}, err
		}
		return deps.RunResult{}, nil
	}
	return runner.goRunner.Run(ctx, argv, options)
}

func TestInstallHarvesterConvergesDaemonBeforeGatewaySetup(t *testing.T) {
	for _, test := range []struct {
		name       string
		chat       bool
		failAction string
		linger     bool
		stateErr   bool
		wantErr    string
	}{
		{name: "chat restart", chat: true},
		{name: "chat disabled stop"},
		{name: "restart failed", chat: true, failAction: "restart", wantErr: "service action failed"},
		{name: "stop failed", failAction: "stop", wantErr: "service action failed"},
		{name: "stop still active", linger: true, wantErr: "active"},
		{name: "state unreadable", stateErr: true, wantErr: "service state unreadable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = listener.Close() })
			home, root := t.TempDir(), t.TempDir()
			checkout := filepath.Join(root, "harvester", "plugins", "harvester")
			writeFixture(
				t,
				filepath.Join(checkout, ".claude-plugin", "plugin.json"),
				`{"name":"harvester","version":"0.1.0-dev.1"}`,
			)
			writeFixture(t, filepath.Join(checkout, "libexec", "harvester"), "#!/bin/sh\n")
			process := &gatewaySetupRunner{goRunner: goRunner{output: "plugin"}, address: listener.Addr().String()}
			service := &gatewayServiceRunner{
				listener:   listener,
				state:      "active",
				failAction: test.failAction,
				linger:     test.linger,
				stateErr:   test.stateErr,
			}
			inst := &engine{options: Options{
				Home: home, Env: &paths.MapEnv{}, Stdout: io.Discard, ProcessRunner: process,
				Runner: service, Sleep: func(time.Duration) {}, Version: "1.0.0-alpha", ClaudePluginCheckoutRoot: root,
				ClaudePlugins: bareList("harvester"), MCPEnabled: map[string]bool{"chat": test.chat},
			}, apply: true}
			if err := inst.ensureGoPluginBinary(
				context.Background(),
				&ClaudePluginTarget{Name: "harvester", Checkout: checkout},
			); err != nil {
				t.Fatal(err)
			}
			err = inst.installHarvester(context.Background())
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) || process.setups != 0 {
					t.Fatalf("error=%v setups=%d", err, process.setups)
				}
			} else if err != nil || process.setups != 1 {
				t.Fatalf("error=%v setups=%d", err, process.setups)
			}
		})
	}
}

func TestMCPDaemonHandoffWithoutManagerRequiresProvenAbsence(t *testing.T) {
	for _, unreadable := range []bool{false, true} {
		t.Run(fmt.Sprint(unreadable), func(t *testing.T) {
			root := t.TempDir()
			if unreadable {
				pid := filepath.Join(root, fmt.Sprint(os.Getppid()))
				writeFixture(t, filepath.Join(pid, "stat"), "invalid process stat")
				if err := os.Symlink("cmdline", filepath.Join(pid, "cmdline")); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			inst := &engine{
				options: Options{Home: t.TempDir(), ProcRoot: root, Runner: &fakeRunner{}, Stdout: &output},
				apply:   true,
			}
			err := inst.convergeMCPDaemon(context.Background())
			if unreadable {
				if err == nil || !strings.Contains(err.Error(), "process check") {
					t.Fatalf("unreadable process check=%v", err)
				}
			} else if err != nil || !strings.Contains(output.String(), "process enumeration confirms no daemon") {
				t.Fatalf("error=%v output=%s", err, output.String())
			}
		})
	}
}

func TestMCPDaemonLaunchdHandoff(t *testing.T) {
	for _, test := range []struct {
		name    string
		chat    bool
		runner  CommandRunner
		wantErr string
	}{
		{name: "restart", chat: true, runner: &loadedRunner{}},
		{name: "stopped", runner: &printRunner{err: commandExitError{name: "launchctl", code: 113}}},
		{name: "still loaded", runner: &printRunner{}, wantErr: "remains loaded"},
		{name: "unreadable", runner: &printRunner{err: errors.New("unreadable state")}, wantErr: "unreadable state"},
		{name: "coded permission error", runner: &printRunner{err: commandExitError{name: "launchctl", code: 5}}, wantErr: "exited 5"},
		{name: "cannot probe", runner: &loadedRunner{}, wantErr: "cannot read output"},
	} {
		t.Run(test.name, func(t *testing.T) {
			inst := &engine{
				options: Options{
					Home:       t.TempDir(),
					Runner:     test.runner,
					Stdout:     io.Discard,
					Sleep:      func(time.Duration) {},
					MCPEnabled: map[string]bool{"chat": test.chat},
				},
				apply: true,
			}
			err := inst.convergeMCPLaunchd(context.Background())
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
