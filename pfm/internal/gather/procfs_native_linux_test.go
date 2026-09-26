//go:build linux

package gather

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLinuxProcFSProcessIdentity(t *testing.T) {
	root := t.TempDir()
	statusPath := filepath.Join(root, "42", "status")
	if err := os.MkdirAll(filepath.Dir(statusPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statusPath, []byte("Name:\tpfm\nUid:\t1000\t1200\t1300\t1400\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := (linuxProcFS{RealProcFS: RealProcFS{Root: root}}).ProcessIdentity(42)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Command != "pfm" || identity.EffectiveUID != 1200 {
		t.Fatalf("ProcessIdentity(42) = %#v, want command pfm and effective uid 1200", identity)
	}
}

func TestLinuxProcFSProcessIdentityErrorsNameTheirField(t *testing.T) {
	tests := []struct {
		name    string
		status  string
		wantErr string
	}{
		{name: "missing name", status: "Uid:\t1000\t1200\t1300\t1400\n", wantErr: "missing Name field"},
		{name: "missing uid", status: "Name:\tpfm\n", wantErr: "missing Uid field"},
		{name: "short uid", status: "Name:\tpfm\nUid:\t1000\n", wantErr: "malformed Uid field"},
		{name: "invalid uid", status: "Name:\tpfm\nUid:\t1000\tnope\n", wantErr: "parse effective uid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			statusPath := filepath.Join(root, "43", "status")
			if err := os.MkdirAll(filepath.Dir(statusPath), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(statusPath, []byte(test.status), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := (linuxProcFS{RealProcFS: RealProcFS{Root: root}}).ProcessIdentity(43)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ProcessIdentity(43) error = %v, want %q", err, test.wantErr)
			}
			if !strings.Contains(err.Error(), statusPath) {
				t.Fatalf("ProcessIdentity(43) error does not name status path: %v", err)
			}
		})
	}
}

func TestLinuxProcFSProcessIdentityReportsStatusReadFailure(t *testing.T) {
	root := t.TempDir()
	statusPath := filepath.Join(root, "44", "status")
	_, err := (linuxProcFS{RealProcFS: RealProcFS{Root: root}}).ProcessIdentity(44)
	if err == nil || !strings.Contains(err.Error(), "read process identity for pid 44 from "+statusPath) {
		t.Fatalf("ProcessIdentity(44) error = %v, want contextual status read failure", err)
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
}
