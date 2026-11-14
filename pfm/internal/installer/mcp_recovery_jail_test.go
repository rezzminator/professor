//go:build jail

package installer

// A FIFO registry must be rejected, not blocked on: the test re-runs its own binary as a real child under a deadline.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A hostile registry must not leave an installer renewing its native lock
// forever. Subprocesses bound the blocked-open regression without leaking it.
func TestMCPReadsRejectFIFO(t *testing.T) {
	if source := os.Getenv("PFM_TEST_FIFO_SOURCE"); source != "" {
		var err error
		switch os.Getenv("PFM_TEST_FIFO_READ") {
		case "backup":
			err = copyBackup(source, source+".backup")
		case "save":
			inst := &engine{stamp: "fixture"}
			err = withClaudeMCPLocks([]string{source}, func(check func() error) error {
				if err := os.Remove(source); err != nil {
					return err
				}
				if err := syscall.Mkfifo(source, 0o600); err != nil {
					return err
				}
				return inst.writeMCPFileIf(source, []byte("original"), []byte("pfm"), true, check)
			})
			if _, lockErr := os.Lstat(source + ".lock"); !errors.Is(lockErr, os.ErrNotExist) {
				t.Fatalf("hostile registry left native lock held: %v", lockErr)
			}
		default:
			_, _, err = readMCPFile(source)
		}
		if err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Fatalf("want non-regular rejection for %s, got %v", source, err)
		}
		return
	}
	for _, reader := range []string{"registry", "backup", "save"} {
		t.Run(reader, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "registry.json")
			if reader == "save" {
				writeFixture(t, path, "original")
			} else if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, executable, "-test.run=^TestMCPReadsRejectFIFO$")
			command.Env = append(os.Environ(), "PFM_TEST_FIFO_SOURCE="+path, "PFM_TEST_FIFO_READ="+reader)
			output, err := command.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("%s blocked on a FIFO: %v", reader, ctx.Err())
			}
			if err != nil {
				t.Fatalf("%s: %v\n%s", reader, err, output)
			}
		})
	}
}
