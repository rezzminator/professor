package installer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstallerLockSerializesInstallsAcrossRootRetirement(t *testing.T) {
	root := filepath.Join(t.TempDir(), "managed")
	unlock, err := lockInstaller(root)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := lockInstaller(root); err == nil {
		_ = second()
		t.Fatal("concurrent installer acquired the lock")
	}
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if second, err := lockInstaller(root); err == nil {
		_ = second()
		t.Fatal("root retirement replaced the lock inode")
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	release, err := lockInstaller(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}
