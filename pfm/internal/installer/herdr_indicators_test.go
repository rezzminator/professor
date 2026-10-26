package installer

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/restart"
)

type indicatorInstallKernel struct {
	gather.ProcFS
	alive   bool
	image   gather.FileID
	listErr error
	signals []syscall.Signal
}

func (k *indicatorInstallKernel) PIDs() ([]int, error) { return []int{4242}, k.listErr }
func (k *indicatorInstallKernel) Cmdline(int) ([]string, error) {
	if !k.alive {
		return nil, fs.ErrNotExist
	}
	return []string{"pfm", "internal", "herdr-indicators"}, nil
}

func (k *indicatorInstallKernel) Stat(int) (gather.ProcStat, error) {
	if !k.alive {
		return gather.ProcStat{}, fs.ErrNotExist
	}
	return gather.ProcStat{StartTime: 7}, nil
}

func (k *indicatorInstallKernel) ProcessIdentity(int) (gather.ProcessIdentity, error) {
	if !k.alive {
		return gather.ProcessIdentity{}, fs.ErrNotExist
	}
	return gather.ProcessIdentity{EffectiveUID: uint32(os.Geteuid())}, nil
}
func (k *indicatorInstallKernel) Image(int) (gather.FileID, error) { return k.image, nil }
func (k *indicatorInstallKernel) Environ(int) (map[string]string, error) {
	return map[string]string{"HERDR_SOCKET_PATH": "/fixture/herdr.sock", "HOME": "/fixture/home"}, nil
}

func (k *indicatorInstallKernel) signal(_ int, sig syscall.Signal) error {
	if sig != 0 {
		k.signals = append(k.signals, sig)
		k.alive = false
	}
	if !k.alive && sig == 0 {
		return syscall.ESRCH
	}
	return nil
}

func TestInstallRefreshesStaleHerdrIndicators(t *testing.T) {
	home := t.TempDir()
	binary := filepath.Join(home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("installed image"), 0o600); err != nil {
		t.Fatal(err)
	}
	kernel := &indicatorInstallKernel{alive: true, image: gather.FileID{Device: 9, Inode: 7}}
	runner := &deps.FakeRunner{}
	runner.ScriptStart([]string{binary, "internal", "herdr-indicators"}, 5000, nil, nil)
	dependencies := restart.Deps{
		Procs: kernel, Signal: kernel.signal, Runner: runner,
		Clock: clock.Real, EffectiveUID: uint32(os.Geteuid()), Executable: binary,
	}
	var output bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home,
		MCPConfigPath: testConfigPath(t), Runner: &fakeRunner{}, Stdout: &output,
		herdrIndicatorDeps: &dependencies,
	})
	if err != nil {
		t.Fatalf("install: %v\n%s", err, output.String())
	}
	if !slices.Equal(kernel.signals, []syscall.Signal{syscall.SIGTERM}) {
		t.Fatalf("install painter signals = %v, want [terminated]", kernel.signals)
	}
	starts := runner.Starts()
	if len(starts) != 1 {
		t.Fatalf("painter starts = %v, want one", starts)
	}
	start := starts[0]
	if !slices.Equal(start.Argv, []string{binary, "internal", "herdr-indicators"}) ||
		!start.Opts.Detach || start.Opts.Stdin != nil || start.Opts.Stdout != nil || start.Opts.Stderr != nil ||
		!slices.Contains(start.Opts.Env, "HERDR_SOCKET_PATH=/fixture/herdr.sock") {
		t.Fatalf("painter start = %+v", start)
	}
	if !strings.Contains(output.String(), "  change  replaced 1 stale Herdr indicator") {
		t.Fatalf("refresh not reported as a change: %s", output.String())
	}
}

func TestInstallHerdrIndicatorConvergenceNoopsAndErrors(t *testing.T) {
	for _, name := range []string{"preview", "current", "absent", "unreadable"} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			binary := filepath.Join(home, ".local", "bin", "pfm")
			if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(binary, []byte("installed image"), 0o600); err != nil {
				t.Fatal(err)
			}
			image, err := gather.FileIDOf(binary)
			if err != nil {
				t.Fatal(err)
			}
			kernel := &indicatorInstallKernel{alive: true, image: image}
			mode := ModeApply
			switch name {
			case "preview":
				mode = ModeDryRun
				kernel.image.Inode++
			case "absent":
				kernel.alive = false
			case "unreadable":
				kernel.listErr = fs.ErrPermission
			}
			runner := &deps.FakeRunner{}
			dependencies := restart.Deps{
				Procs: kernel, Signal: kernel.signal, Runner: runner,
				Clock: clock.Real, EffectiveUID: uint32(os.Geteuid()), Executable: binary,
			}
			var output bytes.Buffer
			_, err = Run(context.Background(), Options{
				Mode: mode, Home: home,
				MCPConfigPath: testConfigPath(t), Runner: &fakeRunner{}, Stdout: &output,
				herdrIndicatorDeps: &dependencies,
			})
			if name == "unreadable" {
				if err == nil || !strings.Contains(err.Error(), "list processes") ||
					!strings.Contains(output.String(), "FAIL    refresh Herdr indicators") {
					t.Fatalf("install error=%v output=%s", err, output.String())
				}
			} else if err != nil {
				t.Fatalf("install: %v\n%s", err, output.String())
			}
			if len(kernel.signals) != 0 || len(runner.Starts()) != 0 {
				t.Fatalf("signals=%v starts=%v", kernel.signals, runner.Starts())
			}
		})
	}
}

// A fake Env without PFM_PROC_ROOT must resolve to the package's process jail:
// "/proc" here is the fence's live table, where another suite's painter runs.
func TestInstallOptionsWithFakeEnvStayInProcessJail(t *testing.T) {
	options, err := normalizeInstallerOptions(
		Options{Home: t.TempDir(), Env: &paths.MapEnv{}, StateDB: filepath.Join(t.TempDir(), "pfm.db")},
	)
	if err != nil {
		t.Fatal(err)
	}
	if options.ProcRoot == "/proc" || options.ProcRoot != os.Getenv(paths.EnvProcRoot) {
		t.Fatalf("ProcRoot = %q, want the package jail %q", options.ProcRoot, os.Getenv(paths.EnvProcRoot))
	}
}
