package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/hookentry"
)

func TestCodexLaunchCompatibilityForAlreadyLoadedShells(t *testing.T) {
	jailTest(t)
	binary, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	previous := hookentry.LaunchExec
	t.Cleanup(func() { hookentry.LaunchExec = previous })
	sentinel := errors.New("exec intercepted")
	called := false
	hookentry.LaunchExec = func(path string, args, env []string) error {
		called = true
		if path != binary ||
			!reflect.DeepEqual(args, []string{binary, "-c", "printf untouched", "--", "literal prompt"}) ||
			!reflect.DeepEqual(env, os.Environ()) {
			t.Fatalf("legacy launch changed argv/environment: %q %q", path, args)
		}
		return sentinel
	}
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"internal", "codex-launch", binary, "-c", "printf untouched", "--", "literal prompt"},
		&stdout,
		&stderr,
	)
	if !called || code != 1 {
		t.Fatalf("legacy entrypoint missing: called=%v code=%d stderr=%s", called, code, stderr.String())
	}
}
