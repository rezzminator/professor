package gather

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"

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
			if competing, err := AcquireAccountGuard(dir, false); !errors.Is(err, syscall.EWOULDBLOCK) {
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
