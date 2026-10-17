package gather

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
)

func TestAccountGuardSerializesLaunchAndChecksProcessGeneration(t *testing.T) {
	for _, scenario := range []string{"live", "exited", "reused", "pending", "invalid", "unreadable", "proc error", "abort"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			guard, err := AcquireAccountGuard(dir, true)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := guard.Close(); err != nil {
					t.Error(err)
				}
			}()
			if competing, err := acquireAccountGuardWithin(dir, false, 0); !errors.Is(err, syscall.EWOULDBLOCK) {
				if competing != nil {
					if closeErr := competing.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				}
				t.Fatalf("concurrent guard = %v", err)
			}
			path := guard.claim
			proc := &fakeProcFS{processes: map[int]fakeProcess{42: {stat: ProcStat{StartTime: 101}}}}
			raw, encodeErr := json.Marshal(accountLaunchClaim{PID: 42, Start: 101})
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			if scenario == "exited" {
				delete(proc.processes, 42)
			}
			if scenario == "reused" {
				proc.processes[42] = fakeProcess{stat: ProcStat{StartTime: 102}}
			}
			if scenario == "pending" {
				raw = []byte(`{"pending":true}`)
			}
			if scenario == "invalid" {
				raw = []byte("{")
			}
			if scenario == "unreadable" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := atomicfile.Write(path, raw, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "abort" {
				if err := guard.Abort(); err != nil {
					t.Fatal(err)
				}
			}
			var source ProcFS = proc
			if scenario == "proc error" {
				source = RealProcFS{Root: filepath.Join(dir, "missing")}
				if err := atomicfile.Write(
					filepath.Join(dir, "missing", "42", "stat"),
					[]byte("broken"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			active, err := guard.Active(source)
			switch scenario {
			case "live":
				if err != nil || len(active) != 1 || active[0] != 42 {
					t.Fatalf("active=%v err=%v", active, err)
				}
			case "exited", "reused", "abort":
				if err != nil || len(active) != 0 {
					t.Fatalf("active=%v err=%v", active, err)
				}
				if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("expired claim=%v", err)
				}
			default:
				if err == nil {
					t.Fatalf("%s claim read as no active launch", scenario)
				}
			}
		})
	}
}

func TestAccountGuardRecordUsesNativeProcessBirthAndReleasesOnFailure(t *testing.T) {
	dir := t.TempDir()
	guard, err := AcquireAccountGuard(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Record(os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if err := guard.Close(); err != nil {
		t.Fatal(err)
	}
	install, err := AcquireAccountGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	live, err := install.Active(NewProcFS(""))
	if err != nil || len(live) != 1 || live[0] != os.Getpid() {
		t.Fatalf("recorded live launch=%v %v", live, err)
	}
	if err := install.Close(); err != nil {
		t.Fatal(err)
	}
	if err := guard.Abort(); err != nil {
		t.Fatal(err)
	}
	failed, err := AcquireAccountGuard(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.Record(0); err == nil {
		t.Fatal("invalid process record accepted")
	}
	if err := failed.Abort(); err != nil {
		t.Fatal(err)
	}
	if err := failed.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := AcquireAccountGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := again.Close(); err != nil {
			t.Error(err)
		}
	}()
	if live, err := again.Active(NewProcFS("")); err != nil || len(live) != 0 {
		t.Fatalf("failed launch cleanup=%v %v", live, err)
	}
}

// A closed guard whose open file description a forked child still holds keeps
// the flock; the next non-blocking acquire must outlast that window and not
// report the account busy.
func TestAcquireAccountGuardOutlastsAHeldFileDescription(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireAccountGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	held, err := syscall.Dup(int(first.file.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { _ = syscall.Close(held) }) }
	defer release()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	timer := time.AfterFunc(100*time.Millisecond, release)
	defer timer.Stop()
	second, err := AcquireAccountGuard(dir, false)
	if err != nil {
		t.Fatalf("acquire while a forked child held the description = %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

// An account a live owner holds is still refused, after the grace window and
// with the busy error install reports.
func TestAcquireAccountGuardRefusesAnAccountHeldPastTheGrace(t *testing.T) {
	dir := t.TempDir()
	owner, err := AcquireAccountGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := owner.Close(); err != nil {
			t.Error(err)
		}
	}()
	started := time.Now()
	competing, err := acquireAccountGuardWithin(dir, false, 60*time.Millisecond)
	if competing != nil {
		_ = competing.Close()
	}
	if !errors.Is(err, syscall.EWOULDBLOCK) {
		t.Fatalf("competing guard = %v, want EWOULDBLOCK", err)
	}
	if waited := time.Since(started); waited < 60*time.Millisecond {
		t.Fatalf("refused after %s, before the 60ms grace", waited)
	}
}

// Acquire-and-close loops running beside goroutines that start processes never
// see a busy account: a fork between open and exec must not fail the guard.
func TestAcquireAccountGuardSurvivesConcurrentProcessStarts(t *testing.T) {
	dir := t.TempDir()
	stop := make(chan struct{})
	var starters sync.WaitGroup
	for range 4 {
		starters.Add(1)
		go func() {
			defer starters.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = exec.Command("true").Run()
				}
			}
		}()
	}
	defer func() {
		close(stop)
		starters.Wait()
	}()
	for attempt := range 400 {
		guard, err := AcquireAccountGuard(dir, false)
		if err != nil {
			t.Fatalf("acquire %d beside process starts: %v", attempt, err)
		}
		if err := guard.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
