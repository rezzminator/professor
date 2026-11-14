package installer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/retired"
)

// bootstrapFailureRunner models launchd's real shape: `print` answers for a
// loaded label and stops answering once the job has been booted out, and
// `bootstrap` fails the way it does when it cannot take the label back.
type bootstrapFailureRunner struct {
	calls     []string
	bootedOut bool
}

func (r *bootstrapFailureRunner) Run(_ context.Context, name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "bootout":
		r.bootedOut = true
		return nil
	case "print":
		if r.bootedOut {
			return errors.New("could not find service")
		}
		return nil
	case "bootstrap":
		return errors.New("bootstrap exit status 5")
	}
	return nil
}

// A bootstrap that fails AFTER the running job was stopped has left the host
// with the service DOWN. That must not read like a plain "not loaded": it is
// the one outcome where the installer made things worse, and the operator
// needs the command that puts it back.
func TestLaunchAgentBootstrapFailureAfterStopSaysTheServiceIsDown(t *testing.T) {
	t.Parallel()
	runner := &bootstrapFailureRunner{}
	installer := engine{
		options: Options{Runner: runner, Stdout: io.Discard, Sleep: func(time.Duration) {}},
		apply:   true,
	}
	err := installer.reloadLaunchAgentWithLabel(context.Background(), "/fixture/agent.plist", mcpLaunchdLabel, true)
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	want := "launchctl bootstrap com.professor.pfm.mcp failed AFTER its running job was stopped to load the new plist;" +
		" the agent file is installed and the service is now DOWN — restart it with `launchctl bootstrap " + domain +
		" /fixture/agent.plist`: bootstrap exit status 5"
	if err == nil || err.Error() != want {
		t.Fatalf("bootstrap failure = %v, want %q", err, want)
	}
	wantCalls := []string{
		"launchctl print " + domain + "/com.professor.pfm.mcp",
		"launchctl bootout " + domain + "/com.professor.pfm.mcp",
		"launchctl bootstrap " + domain + " /fixture/agent.plist",
	}
	if !slices.Equal(runner.calls, wantCalls) {
		t.Fatalf("calls = %q, want %q", runner.calls, wantCalls)
	}
}

func TestRetirementReachesTheStore(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	primary := filepath.Join(home, ".claude")
	codexHome := filepath.Join(home, ".codex")
	managed := filepath.Join(home, "install")
	status := filepath.Join(primary, "statusline-command.sh")
	if err := os.MkdirAll(primary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(primary, "gone-statusline.sh"), status); err != nil {
		t.Fatal(err)
	}
	installer := engine{
		options: Options{
			Home:       home,
			ConfigDir:  primary,
			CodexHomes: []string{codexHome},
			Stdout:     io.Discard,
			retired: &retired.Lists{
				ConfigFiles: []retired.ConfigFile{{Path: "statusline-command.sh", Reason: "native pfm statusline"}},
			},
		},
		managedRoot: managed,
		apply:       true,
	}
	if err := installer.retirePredecessors(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(status); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("retired artifact %s survives: %v", status, err)
	}
}
