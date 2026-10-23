package restart

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

type indicatorKernel struct {
	*fakeKernel
	images     map[int]gather.FileID
	envs       map[int]map[string]string
	imageErr   error
	envErr     error
	imageReads int
	reexec     bool
	recycle    bool
}

func (k *indicatorKernel) Image(pid int) (gather.FileID, error) {
	k.imageReads++
	image := k.images[pid]
	if k.reexec && k.imageReads > 1 {
		image.Inode++
	}
	return image, k.imageErr
}

func (k *indicatorKernel) Environ(pid int) (map[string]string, error) {
	if k.recycle {
		p := k.procs[pid]
		p.Start++
		k.procs[pid] = p
	}
	return k.envs[pid], k.envErr
}

func TestConvergeHerdrIndicators(t *testing.T) {
	for _, name := range []string{
		"stale", "current", "foreign", "absent", "duplicate socket", "two sockets",
		"list error", "unreadable", "image error", "environment error", "missing socket", "zero start",
		"reexec", "recycled pid", "start error", "stop error", "extra args", "gone image", "missing binary",
	} {
		t.Run(name, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "pfm")
			if err := os.WriteFile(binary, []byte("installed"), 0o600); err != nil {
				t.Fatal(err)
			}
			installed, err := gather.FileIDOf(binary)
			if err != nil {
				t.Fatal(err)
			}
			stale := installed
			stale.Inode++
			proc := process{PID: 40, Start: 5, Argv: []string{"pfm", "internal", "herdr-indicators"}}
			k := &indicatorKernel{
				fakeKernel: newKernel(proc),
				images:     map[int]gather.FileID{40: stale},
				envs: map[int]map[string]string{
					40: {"HERDR_SOCKET_PATH": "/fixture/one.sock", "HOME": "/fixture/home"},
				},
			}
			wantStarts, wantStops, wantErr := 1, 1, ""
			switch name {
			case "current":
				k.images[40] = installed
				wantStarts, wantStops = 0, 0
			case "foreign":
				k.foreign[40] = true
				wantStarts, wantStops = 0, 0
			case "absent":
				delete(k.procs, 40)
				wantStarts, wantStops = 0, 0
			case "duplicate socket", "two sockets":
				second := proc
				second.PID = 41
				k.procs[41] = second
				k.images[41] = stale
				k.envs[41] = map[string]string{"HERDR_SOCKET_PATH": "/fixture/one.sock"}
				wantStops = 2
				if name == "two sockets" {
					k.envs[41]["HERDR_SOCKET_PATH"] = "/fixture/two.sock"
					wantStarts = 2
				}
			case "list error":
				k.listErr = fs.ErrPermission
				wantErr = "list processes"
			case "unreadable":
				k.unreadable[40] = true
				wantErr = "could not be read"
			case "image error":
				k.imageErr = fs.ErrPermission
				wantErr = "executable identity"
			case "environment error":
				k.envErr = fs.ErrPermission
				wantErr = "environment"
			case "missing socket":
				delete(k.envs[40], "HERDR_SOCKET_PATH")
				wantErr = "HERDR_SOCKET_PATH"
			case "zero start":
				proc.Start = 0
				k.procs[40] = proc
				wantErr = "start identity unavailable"
			case "reexec":
				k.reexec = true
				wantErr = "executable changed before signal"
			case "recycled pid":
				k.recycle = true
				wantErr = "identity changed while reading environment"
			case "start error":
				wantErr = "start Herdr indicator"
			case "stop error":
				k.unkillable[40], k.stubborn[40] = true, true
				wantErr = "still runs"
			case "extra args":
				proc.Argv = append(proc.Argv, "extra")
				k.procs[40] = proc
				wantStarts, wantStops = 0, 0
			case "gone image":
				k.imageErr = fs.ErrNotExist
				wantErr = "executable identity"
			case "missing binary":
				binary += ".absent"
				wantErr = "installed binary"
			}
			runner := &deps.FakeRunner{}
			var startErr error
			if name == "start error" {
				startErr = errors.New("spawn refused")
			}
			runner.ScriptStart([]string{binary}, 99, nil, startErr)
			dependencies := Deps{Procs: k, Signal: k.signal, Clock: newTickClock(), Runner: runner, Executable: binary}
			detail, err := ConvergeHerdrIndicators(context.Background(), dependencies)
			if wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), wantErr) {
					t.Fatalf("error = %v, want %s", err, wantErr)
				}
				if name != "start error" && len(runner.Starts()) != 0 {
					t.Fatalf("started after error: %v", runner.Starts())
				}
				if name != "start error" && name != "stop error" && len(k.sent()) != 0 {
					t.Fatalf("signalled before preflight: %v", k.sent())
				}
				return
			}
			if err != nil {
				t.Fatalf("converge: %v", err)
			}
			if len(k.sent()) != wantStops || len(runner.Starts()) != wantStarts {
				t.Fatalf("signals=%v starts=%v detail=%s", k.sent(), runner.Starts(), detail)
			}
			for _, start := range runner.Starts() {
				if !slices.Equal(start.Argv, []string{binary, "internal", "herdr-indicators"}) || !start.Opts.Detach ||
					start.Opts.Stdin != nil || start.Opts.Stdout != nil || start.Opts.Stderr != nil {
					t.Fatalf("start = %+v", start)
				}
				if len(start.Opts.Env) == 0 {
					t.Fatal("missing saved environment")
				}
			}
		})
	}
}
