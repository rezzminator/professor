//go:build linux

package gather

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxProcFSProcessIdentity(t *testing.T) {
	for state, zombie := range map[string]bool{
		"": false, "Z (zombie)": true, "X (dead)": true, "S (sleeping)": false, "? (unknown)": false,
	} {
		t.Run(state, func(t *testing.T) {
			root := t.TempDir()
			statusPath := filepath.Join(root, "42", "status")
			if err := os.MkdirAll(filepath.Dir(statusPath), 0o700); err != nil {
				t.Fatal(err)
			}
			status := "Name:\tpfm\nUid:\t1000\t1200\t1300\t1400\n"
			if state != "" {
				status += "State:\t" + state + "\n"
			}
			if err := os.WriteFile(statusPath, []byte(status), 0o600); err != nil {
				t.Fatal(err)
			}
			identity, err := (linuxProcFS{RealProcFS: RealProcFS{Root: root}}).ProcessIdentity(42)
			if err != nil {
				t.Fatal(err)
			}
			if identity.Command != "pfm" || identity.EffectiveUID != 1200 || identity.Zombie != zombie {
				t.Fatalf("ProcessIdentity(42)=%#v for state %q, want zombie %v", identity, state, zombie)
			}
		})
	}
}

func TestLinuxProcFSProcessIdentityErrorsNameTheirField(t *testing.T) {
	for _, test := range []struct{ name, status, want string }{
		{"missing name", "Uid:\t1000\t1200\t1300\t1400\n", "read process identity for pid 43 from {status}: missing Name field"},
		{"missing uid", "Name:\tpfm\n", "read process identity for pid 43 from {status}: missing Uid field"},
		{"short uid", "Name:\tpfm\nUid:\t1000\n", "parse effective uid for pid 43 from {status}: malformed Uid field"},
		{"invalid uid", "Name:\tpfm\nUid:\t1000\tnope\n", "parse effective uid for pid 43 from {status}: strconv.ParseUint: parsing \"nope\": invalid syntax"},
		{"unreadable status", "", "read process identity for pid 43 from {status}: open {status}: no such file or directory"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			statusPath := filepath.Join(root, "43", "status")
			if test.status != "" {
				if err := os.MkdirAll(filepath.Dir(statusPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(statusPath, []byte(test.status), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			_, err := (linuxProcFS{RealProcFS: RealProcFS{Root: root}}).ProcessIdentity(43)
			if got := strings.ReplaceAll(fmt.Sprint(err), statusPath, "{status}"); got != test.want {
				t.Fatalf("ProcessIdentity(43) error = %q, want %q", got, test.want)
			}
		})
	}
}

func TestNewProcFSGivesIdentityOnlyToNativeLinuxRoot(t *testing.T) {
	if _, ok := NewProcFS("").(ProcIdentity); !ok {
		t.Fatal("NewProcFS with native default does not expose process identity")
	}
	if _, ok := NewProcFS("/proc").(ProcIdentity); !ok {
		t.Fatal("NewProcFS(/proc) does not expose native process identity")
	}
	fixtureRoot := t.TempDir()
	if _, ok := NewProcFS(fixtureRoot).(ProcIdentity); ok {
		t.Fatal("fixture proc root unexpectedly requires native process identity metadata")
	}
	if _, ok := NewProcFS(fixtureRoot).(ProcStart); ok {
		t.Fatal("fixture proc root exposes a start clock its ticks do not count from")
	}
}
