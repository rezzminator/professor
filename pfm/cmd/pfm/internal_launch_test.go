package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

type synchronizedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (buffer *synchronizedBuffer) Write(content []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.Write(content)
}

func (buffer *synchronizedBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.b.String()
}

func TestDoctorReportsClaudeLauncherMissingDisplacedAndOK(t *testing.T) {
	root := jailTest(t)
	home := filepath.Join(root, "home")
	testjail.PinClaudeAsk(t, home)
	canonical := filepath.Join(home, ".local", "bin", "claude")
	managed := filepath.Join(home, ".local", "share", "pfm", "install", "bin", "claude")
	native := filepath.Join(home, ".local", "share", "claude", "versions", "fixture")
	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(managed); err != nil {
		t.Fatal(err)
	}

	doctor := func() (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := run([]string{"doctor"}, &stdout, &stderr)
		if stderr.Len() != 0 {
			t.Fatalf("doctor stderr=%q", stderr.String())
		}
		return code, stdout.String()
	}
	code, output := doctor()
	if code != 3 || !strings.Contains(output, "doctor: launcher: missing — run pfm install") {
		t.Fatalf("missing launcher doctor code=%d:\n%s", code, output)
	}

	stubExecutable(t, managed)
	if err := os.Symlink(managed, canonical); err != nil {
		t.Fatal(err)
	}
	code, output = doctor()
	if code != 0 || !strings.Contains(output, "doctor: launcher: ok") {
		t.Fatalf("healthy launcher doctor code=%d:\n%s", code, output)
	}

	if err := os.Remove(canonical); err != nil {
		t.Fatal(err)
	}
	stubExecutable(t, native)
	if err := os.Symlink(native, canonical); err != nil {
		t.Fatal(err)
	}
	code, output = doctor()
	if code != 3 || !strings.Contains(output, "doctor: launcher: DISPLACED by "+native+" — run pfm install") {
		t.Fatalf("displaced launcher doctor code=%d:\n%s", code, output)
	}
}

// stubExecutable writes an executable file the code under test only inspects;
// nothing in the test starts it.
func stubExecutable(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutableNeverRun(path, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}
