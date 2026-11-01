package gather

import (
	"os"
	"os/exec"
	"testing"
)

// TestProcessAlive: this process is alive, a reaped child is gone, and a pid
// that names no single process is an error, never "dead".
func TestProcessAlive(t *testing.T) {
	if alive, err := ProcessAlive(os.Getpid()); err != nil || !alive {
		t.Fatalf("ProcessAlive(self) = %v, %v; want alive", alive, err)
	}
	child := exec.Command("true")
	if err := child.Run(); err != nil {
		t.Fatal(err)
	}
	if alive, err := ProcessAlive(child.Process.Pid); err != nil || alive {
		t.Fatalf("ProcessAlive(reaped child) = %v, %v; want gone", alive, err)
	}
	for _, pid := range []int{0, -1} {
		if alive, err := ProcessAlive(pid); err == nil || alive {
			t.Fatalf("ProcessAlive(%d) = %v, %v; want an error", pid, alive, err)
		}
	}
}
