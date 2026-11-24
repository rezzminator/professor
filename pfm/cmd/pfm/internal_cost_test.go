package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// fakeClaude records its pid, environment and open descriptors, then lives until the test closes
// its stdin, the way a chat lives while its sink serves it: it reads to end of file and exits.
const fakeClaude = `#!/bin/sh
out="$1"
echo $$ > "$out/pid"
env > "$out/env"
for f in /proc/$$/fd/*; do readlink "$f"; done > "$out/fds"
: > "$out/ready"
while read -r _; do :; done
`

type costLaunch struct {
	home, out string
	cmd       *exec.Cmd
	stderr    *bytes.Buffer
	// release is the write end of fakeClaude's stdin: closing it ends the chat.
	release *os.File
	// exited closes once the chat is reaped, exitErr then holding its Wait error.
	exited  chan struct{}
	exitErr error
}

func (l *costLaunch) stop(t *testing.T) {
	t.Helper()
	if err := l.release.Close(); err != nil {
		t.Fatal(err)
	}
	testwait.Receive(t, "the launched chat to exit once its stdin closed", l.exited)
	if l.exitErr != nil {
		t.Fatalf("launched chat exit: %v\n%s", l.exitErr, l.stderr.String())
	}
}

func (l *costLaunch) read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(l.out, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func (l *costLaunch) sinkLog() string {
	data, _ := os.ReadFile(filepath.Join(l.home, ".local", "state", "pfm", "log", "cost-sink.log"))
	return string(data)
}
