//go:build linux

package restart

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

// nativeDaemonFixture restricts enumeration to this fixture's real process;
// every identity, executable and environment read still comes from Linux.
type nativeDaemonFixture struct {
	gather.ProcFS
	pid              int
	missingWhileLive int
}

func (fixture *nativeDaemonFixture) PIDs() ([]int, error) { return []int{fixture.pid}, nil }
func (fixture *nativeDaemonFixture) ProcessIdentity(pid int) (gather.ProcessIdentity, error) {
	return fixture.ProcFS.(gather.ProcIdentity).ProcessIdentity(pid)
}

func (fixture *nativeDaemonFixture) observeMissing(pid int, err error) {
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ESRCH) {
		who, proofErr := fixture.ProcessIdentity(pid)
		if proofErr == nil && !who.Zombie {
			fixture.missingWhileLive++
		}
	}
}

func (fixture *nativeDaemonFixture) Executable(pid int) (string, error) {
	path, err := fixture.ProcFS.(gather.ProcExecutable).Executable(pid)
	fixture.observeMissing(pid, err)
	return path, err
}

func (fixture *nativeDaemonFixture) Environ(pid int) (map[string]string, error) {
	env, err := fixture.ProcFS.Environ(pid)
	fixture.observeMissing(pid, err)
	return env, err
}

func TestConvergeMCPDaemonRealLinuxProcessExit(t *testing.T) {
	runner := deps.RealRunner{}
	binary, err := runner.LookPath("yes")
	if err != nil {
		t.Fatal(err)
	}
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(t.TempDir(), "pfm.config.json")
	if err := os.WriteFile(configPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	observed := 0
	for attempt := 0; attempt < 4; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), termGrace+killGrace)
		process, err := runner.Start(ctx, []string{binary, "mcp", "serve"}, deps.StartOptions{
			Env: deps.EnvironmentWith("PFM_CONFIG", configPath), Stdout: io.Discard, Stderr: io.Discard,
		})
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		fixture := &nativeDaemonFixture{ProcFS: gather.NewProcFS("/proc"), pid: process.Pid()}
		// Start's exec handshake can precede a readable /proc cmdline. The fixture
		// must prove it is running the intended argv before asking to stop it.
		readyBy := clock.Real.Now().Add(termGrace)
		for {
			argv, err := fixture.Cmdline(process.Pid())
			image, imageErr := fixture.Executable(process.Pid())
			env, envErr := fixture.Environ(process.Pid())
			stat, statErr := fixture.Stat(process.Pid())
			who, identityErr := fixture.ProcessIdentity(process.Pid())
			if err == nil && imageErr == nil && envErr == nil && statErr == nil && identityErr == nil &&
				slices.Equal(argv, []string{binary, "mcp", "serve"}) && image == binary &&
				env["PFM_CONFIG"] == configPath && stat.StartTime != 0 && !who.Zombie && who.EffectiveUID == uint32(os.Geteuid()) {
				break
			}
			if !clock.Real.Now().Before(readyBy) {
				_ = process.Kill()
				_ = process.Wait()
				cancel()
				t.Fatalf("fixture argv readiness: %v/%v", argv, err)
			}
			if err := clock.Real.Sleep(ctx, pollEvery); err != nil {
				_ = process.Kill()
				_ = process.Wait()
				cancel()
				t.Fatal(err)
			}
		}

		detail, handoffErr := ConvergeMCPDaemon(ctx, Deps{
			Procs: fixture, Signal: syscall.Kill, Clock: clock.Real, EffectiveUID: uint32(os.Geteuid()),
			Executable: binary, ConfigPath: configPath,
		}, false)
		if handoffErr != nil || strings.Contains(detail, "no daemon") {
			argv, argvErr := fixture.Cmdline(process.Pid())
			image, imageErr := fixture.Executable(process.Pid())
			who, identityErr := fixture.ProcessIdentity(process.Pid())
			env, envErr := fixture.Environ(process.Pid())
			_ = process.Kill()
			_ = process.Wait()
			cancel()
			t.Fatalf(
				"handoff=%s error=%v argv=%v/%v executable=%q/%v identity=%+v/%v config=%q/%v wanted exe=%q config=%q",
				detail,
				handoffErr,
				argv,
				argvErr,
				image,
				imageErr,
				who,
				identityErr,
				env["PFM_CONFIG"],
				envErr,
				binary,
				configPath,
			)
		}
		waitErr := process.Wait()
		cancel()
		if handoffErr != nil {
			t.Fatal(handoffErr)
		}
		var exited *exec.ExitError
		if !errors.As(waitErr, &exited) || exited.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
			t.Fatalf("fixture exit=%v; expected the owned process to terminate on SIGTERM", waitErr)
		}
		observed += fixture.missingWhileLive
	}
	t.Logf(
		"real Linux teardown: 4 owned processes terminated; missing-exe/env with live status sampled=%d (kernel window is not forced)",
		observed,
	)
}
