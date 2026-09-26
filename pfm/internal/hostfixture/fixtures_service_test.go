package hostfixture

import (
	"errors"
	"os/exec"
	"testing"
)

func TestNoServiceManagerAnswersENOENTForBothManagers(t *testing.T) {
	base := NoServiceManager(t)
	for _, name := range []string{"systemctl", "launchctl"} {
		_, err := base.Runner.LookPath(name)
		if err == nil {
			t.Fatalf("LookPath(%s) succeeded after NoServiceManager, want ENOENT", name)
		}
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("LookPath(%s) error = %v, want exec.ErrNotFound (ENOENT)", name, err)
		}
	}
}
