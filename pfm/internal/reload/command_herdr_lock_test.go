package reload

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLockNativeReloadSafety(t *testing.T) {
	for _, kind := range []string{"new-directory", "existing-lock", "leaf-symlink", "dangling-symlink", "directory-symlink", "foreign-directory", "foreign-lock", "writable-directory"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			options := Options{SIDDir: filepath.Join(root, "sid"), IdleTries: 1, Poll: -1}
			options.defaults()
			request := Request{SocketPath: filepath.Join(root, "herdr.sock"), Pane: "w6:p3"}
			key := fmt.Sprintf("herdr-%x", sha256.Sum256([]byte(request.SocketPath+"\x00"+request.Pane)))
			path := LockPath(options.SIDDir, key, "seat")
			target := filepath.Join(root, "target")
			success := kind == "new-directory" || kind == "existing-lock"
			if kind != "new-directory" {
				if err := os.MkdirAll(options.SIDDir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "existing-lock", "foreign-lock":
				if err := os.WriteFile(path, []byte("lock-sentinel"), 0o600); err != nil {
					t.Fatal(err)
				}
				if kind == "foreign-lock" {
					if err := os.Chown(path, os.Geteuid()+1, -1); errors.Is(err, syscall.EPERM) {
						t.Skipf("needs root: only root hands a file to another uid (%v)", err)
					} else if err != nil {
						t.Fatalf("fenced foreign-owner fixture: %v", err)
					}
				}
			case "leaf-symlink", "dangling-symlink":
				if kind == "leaf-symlink" {
					if err := os.WriteFile(target, []byte("target-sentinel"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				if err := os.Remove(options.SIDDir); err != nil {
					t.Fatal(err)
				}
				if err := os.MkdirAll(target, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, options.SIDDir); err != nil {
					t.Fatal(err)
				}
			case "foreign-directory":
				if err := os.Chown(options.SIDDir, os.Geteuid()+1, -1); errors.Is(err, syscall.EPERM) {
					// Without root nothing is handed to another uid; the file
					// system root already belongs to another user.
					var rootDir syscall.Stat_t
					if err := syscall.Stat("/", &rootDir); err != nil || rootDir.Uid == uint32(os.Geteuid()) {
						t.Fatalf("no foreign-owned directory for a non-root run: / uid=%d err=%v", rootDir.Uid, err)
					}
					options.SIDDir = "/"
				} else if err != nil {
					t.Fatalf("fenced foreign-owner fixture: %v", err)
				}
			case "writable-directory":
				if err := os.Chmod(options.SIDDir, 0o777); err != nil {
					t.Fatal(err)
				}
			}
			lock, err := lockNativeReload(context.Background(), request, options)
			if lock != nil {
				if closeErr := lock.Close(); closeErr != nil {
					t.Fatal(closeErr)
				}
			}
			if success {
				if err != nil {
					t.Fatalf("safe lock refused: %v", err)
				}
				info, statErr := os.Stat(path)
				if statErr != nil || !info.Mode().IsRegular() {
					t.Fatalf("lock info=%v err=%v", info, statErr)
				}
				return
			}
			if err == nil {
				t.Fatalf("%s accepted an unsafe native reload lock", kind)
			}
			if !strings.Contains(err.Error(), "native reload") {
				t.Fatalf("lock error lacks context: %v", err)
			}
			if kind == "leaf-symlink" {
				got, readErr := os.ReadFile(target)
				if readErr != nil || string(got) != "target-sentinel" {
					t.Fatalf("symlink target=%q err=%v", got, readErr)
				}
			}
		})
	}
}
