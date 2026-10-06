package testjail

import (
	"os"
	"os/exec"
	"testing"
)

const unsetXDGChild = "PFM_TEST_UNSET_XDG_CHILD"

func TestRunUnsetsXDGConfigHome(t *testing.T) {
	if os.Getenv(unsetXDGChild) == "1" {
		if value, set := os.LookupEnv("XDG_CONFIG_HOME"); set || value != "" {
			t.Fatalf("XDG_CONFIG_HOME=(%q, %t) after Run, want (\"\", false)", value, set)
		}
		home := os.Getenv("PFM_HOME")
		if home == "" {
			t.Fatal("PFM_HOME is unset — Run's own jail did not pin it")
		}
		// A child tool falls back to its home's .config: that home must be the jail's.
		if got, err := os.UserHomeDir(); err != nil || got != home {
			t.Fatalf("home dir=(%q, %v) after Run, want the jail home %q", got, err, home)
		}
		return
	}
	command := exec.Command(os.Args[0], "-test.run=^TestRunUnsetsXDGConfigHome$")
	command.Env = envWithout("XDG_CONFIG_HOME", "XDG_CONFIG_HOME=/tmp/ambient-xdg", unsetXDGChild+"=1")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child test: %v: %s", err, output)
	}
}
