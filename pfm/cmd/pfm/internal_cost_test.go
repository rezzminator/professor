package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// fakeClaude records its pid, environment and open descriptors, then waits for a stop file, the
// way a chat lives while its sink serves it.
const fakeClaude = `#!/bin/sh
out="$1"
echo $$ > "$out/pid"
env > "$out/env"
for f in /proc/$$/fd/*; do readlink "$f"; done > "$out/fds"
: > "$out/ready"
while [ ! -e "$out/stop" ]; do sleep 0.05; done
`

type costLaunch struct {
	home, out string
	cmd       *exec.Cmd
	stderr    *bytes.Buffer
}

func (l *costLaunch) stop(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(l.out, "stop"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := l.cmd.Wait(); err != nil {
		t.Fatalf("launched chat exit: %v\n%s", err, l.stderr.String())
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
