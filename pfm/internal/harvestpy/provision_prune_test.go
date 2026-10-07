package harvestpy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

var (
	pruneCurrent    = strings.Repeat("a", 64)
	pruneSuperseded = strings.Repeat("b", 64)
	pruneLive       = strings.Repeat("c", 64)
	pruneRepair     = pruneCurrent + ".repair-17"
	pruneStaging    = ".current-99"
)

// writePruneEnvironment makes one environment dir holding a 7-byte python.
func writePruneEnvironment(t *testing.T, dir string) string {
	t.Helper()
	python := filepath.Join(dir, "python", "bin", "python3.11")
	if err := os.MkdirAll(filepath.Dir(python), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(python, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	return python
}

// fakeProcess makes procRoot/{pid}/exe a link to exe, as Linux shows it.
func fakeProcess(t *testing.T, procRoot, pid, exe string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(procRoot, pid), 0o700); err != nil {
		t.Fatal(err)
	}
	if exe != "" {
		if err := os.Symlink(exe, filepath.Join(procRoot, pid, "exe")); err != nil {
			t.Fatal(err)
		}
	}
}

// pruneFixture is a platform environment root: current names one env, one
// env is superseded, one is superseded but a live process runs from it, a
// repair quarantine and a pointer staging link are left from a crash, plus
// the lock and an unrelated file.
func pruneFixture(t *testing.T) (envRoot, procRoot string) {
	t.Helper()
	base := t.TempDir()
	envRoot, procRoot = filepath.Join(base, "env", "linux-amd64"), filepath.Join(base, "proc")
	for _, name := range []string{pruneCurrent, pruneSuperseded, pruneRepair} {
		writePruneEnvironment(t, filepath.Join(envRoot, name))
	}
	livePython := writePruneEnvironment(t, filepath.Join(envRoot, pruneLive))
	for _, link := range []string{"current", pruneStaging} {
		if err := os.Symlink(pruneCurrent, filepath.Join(envRoot, link)); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{provisionLockName, "notes.txt"} {
		if err := os.WriteFile(filepath.Join(envRoot, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	fakeProcess(t, procRoot, "4242", livePython)
	fakeProcess(t, procRoot, "1", "")
	fakeProcess(t, procRoot, "self", filepath.Join(envRoot, pruneSuperseded, "python", "bin", "python3.11"))
	return envRoot, procRoot
}

// TestPruneEnvironments pins the prune's keep and reap sides: current's
// target and every env a live process runs from stay, with the reason named;
// where liveness cannot be read every superseded env stays and says so; a
// failed removal is reported and leaves the env.
func TestPruneEnvironments(t *testing.T) {
	cases := []struct {
		name        string
		goos        string
		missingProc bool
		noCurrent   bool
		failRemove  string
		removed     []string
		kept        map[string]string
		failed      []string
	}{
		{
			name: "linux reaps superseded and crash leftovers", goos: "linux",
			removed: []string{pruneSuperseded, pruneRepair, pruneStaging},
			kept:    map[string]string{pruneLive: "pid 4242 runs from it"},
		},
		{
			name: "darwin cannot read liveness", goos: "darwin",
			removed: []string{pruneStaging},
			kept: map[string]string{
				pruneSuperseded: "liveness unreadable",
				pruneLive:       "liveness unreadable",
				pruneRepair:     "liveness unreadable",
			},
		},
		{
			name: "unreadable proc keeps every superseded env", goos: "linux", missingProc: true,
			removed: []string{pruneStaging},
			kept: map[string]string{
				pruneSuperseded: "liveness unreadable",
				pruneLive:       "liveness unreadable",
				pruneRepair:     "liveness unreadable",
			},
		},
		{
			name: "failed removal is reported and kept", goos: "linux", failRemove: pruneSuperseded,
			removed: []string{pruneRepair, pruneStaging},
			kept:    map[string]string{pruneLive: "pid 4242 runs from it"},
			failed:  []string{pruneSuperseded},
		},
		{name: "no current pointer supersedes nothing", goos: "linux", noCurrent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			envRoot, procRoot := pruneFixture(t)
			if tc.missingProc {
				procRoot = filepath.Join(procRoot, "missing")
			}
			if tc.noCurrent {
				if err := os.Remove(filepath.Join(envRoot, "current")); err != nil {
					t.Fatal(err)
				}
			}
			if tc.failRemove != "" {
				restore := pruneRemoveAll
				pruneRemoveAll = func(path string) error {
					if filepath.Base(path) == tc.failRemove {
						return errors.New("permission denied")
					}
					return os.RemoveAll(path)
				}
				t.Cleanup(func() { pruneRemoveAll = restore })
			}
			report := pruneEnvironmentRoot(envRoot, procRoot, tc.goos)
			var removed []string
			for _, env := range report.Removed {
				removed = append(removed, filepath.Base(env.Path))
				want := int64(7)
				if filepath.Base(env.Path) == pruneStaging {
					want = 0
				}
				if env.Bytes != want || filepath.Dir(env.Path) != envRoot {
					t.Errorf("removed %+v, want %d bytes under %s", env, want, envRoot)
				}
				if _, err := os.Lstat(env.Path); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("reported removed %s remains: %v", env.Path, err)
				}
			}
			slices.Sort(removed)
			wantRemoved := slices.Clone(tc.removed)
			slices.Sort(wantRemoved)
			if !slices.Equal(removed, wantRemoved) {
				t.Errorf("removed %v, want %v", removed, wantRemoved)
			}
			if len(report.Kept) != len(tc.kept) {
				t.Errorf("kept %+v, want %v", report.Kept, tc.kept)
			}
			for _, kept := range report.Kept {
				reason, ok := tc.kept[filepath.Base(kept.Path)]
				if !ok || !strings.Contains(kept.Reason, reason) {
					t.Errorf("kept %+v, want reason %q", kept, reason)
				}
			}
			var failed []string
			for _, failure := range report.Failed {
				failed = append(failed, filepath.Base(failure.Path))
				if failure.Err == nil {
					t.Errorf("failure without its error: %+v", failure)
				}
			}
			if !slices.Equal(failed, tc.failed) {
				t.Errorf("failed %v, want %v", failed, tc.failed)
			}
			survivors := []string{pruneCurrent, provisionLockName, "notes.txt"}
			for name := range tc.kept {
				survivors = append(survivors, name)
			}
			survivors = append(survivors, tc.failed...)
			if tc.noCurrent {
				survivors = append(survivors, pruneSuperseded, pruneLive, pruneRepair, pruneStaging)
			}
			for _, name := range survivors {
				if _, err := os.Lstat(filepath.Join(envRoot, name)); err != nil {
					t.Errorf("kept %s is gone: %v", name, err)
				}
			}
		})
	}
}

// TestPruneEnvironmentsLocksAndNeverCreatesTheRoot pins the exported entry:
// a host with no environment root is left without one, and an existing root
// is pruned under the provisioning lock.
func TestPruneEnvironmentsLocksAndNeverCreatesTheRoot(t *testing.T) {
	restore := pruneHostGOOS
	pruneHostGOOS = "linux"
	t.Cleanup(func() { pruneHostGOOS = restore })
	platform := Platform{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	empty := t.TempDir()
	if report := PruneEnvironments(context.Background(), empty, platform, t.TempDir()); len(report.Removed) != 0 ||
		len(report.Failed) != 0 {
		t.Fatalf("absent root report = %+v", report)
	}
	if _, err := os.Lstat(filepath.Join(empty, "env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prune created the environment root: %v", err)
	}
	envRoot, procRoot := pruneFixture(t)
	root := filepath.Dir(filepath.Dir(envRoot))
	if moved := filepath.Join(root, "env", platform.String()); moved != envRoot {
		if err := os.Rename(envRoot, moved); err != nil {
			t.Fatal(err)
		}
	}
	// A provision in flight holds the lock while it builds an env current
	// does not name yet: a prune waits for it and, out of time, removes nothing.
	release, err := lockProvisionRoot(context.Background(), filepath.Join(root, "env", platform.String()))
	if err != nil {
		t.Fatal(err)
	}
	waiting, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	held := PruneEnvironments(waiting, root, platform, procRoot)
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if len(held.Removed) != 0 || len(held.Failed) != 1 {
		t.Fatalf("prune under a held provisioning lock = %+v, want nothing removed and one failure", held)
	}
	report := PruneEnvironments(context.Background(), root, platform, procRoot)
	if len(report.Removed) == 0 {
		t.Fatalf("existing root pruned nothing: %+v", report)
	}
}

// TestProvisionPrunesTheEnvironmentItSupersedes runs a full provision over a
// host whose current env is an older digest: the flip publishes the new env
// and the old one is removed and reported.
func TestProvisionPrunesTheEnvironmentItSupersedes(t *testing.T) {
	restore := pruneHostGOOS
	pruneHostGOOS = "linux"
	t.Cleanup(func() { pruneHostGOOS = restore })
	root := t.TempDir()
	platform := Platform{GOOS: "linux", GOARCH: "amd64"}
	targets, cache := fakeProvisionInputs(t, root, platform)
	envRoot := filepath.Join(root, "env", platform.String())
	old := filepath.Join(envRoot, pruneSuperseded)
	writePruneEnvironment(t, old)
	if err := os.Symlink(pruneSuperseded, filepath.Join(envRoot, "current")); err != nil {
		t.Fatal(err)
	}
	result, err := provisionWithTargets(context.Background(), ProvisionOptions{
		Root: root, Cache: cache, Platform: platform, ProcRoot: t.TempDir(),
		Run: fakeProvisionRun(t, false),
		Smoke: func(context.Context, Runtime) (map[string]any, error) {
			return map[string]any{"imports": map[string]any{}, "conversion": map[string]any{"ok": true}}, nil
		},
	}, targets)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if want := []PrunedEnvironment{{Path: old, Bytes: 7}}; !slices.Equal(result.Pruned.Removed, want) {
		t.Fatalf("pruned %+v, want %+v", result.Pruned, want)
	}
	if _, err := os.Lstat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("superseded env remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(envRoot, result.Digest)); err != nil {
		t.Fatalf("new env missing: %v", err)
	}
}
