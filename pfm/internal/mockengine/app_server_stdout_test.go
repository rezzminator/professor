package mockengine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/gather"
)

func TestAppServerDescendantRetainsStdoutInPrivateGroup(t *testing.T) {
	fix := newFixture(t)
	t.Chdir(fix.work)
	gate := filepath.Join(fix.root, "stdout-release")
	setScenarioField(t, fix, "rate_limits", map[string]any{"planType": "plus"})
	setScenarioField(t, fix, "app_server_stdout_hold", gate)
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	defer func() { _ = writer.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	output := &screen{}
	child, err := (deps.RealRunner{}).Start(
		ctx,
		[]string{filepath.Join(fix.bin, engineCodex), "app-server"},
		deps.StartOptions{
			Dir: fix.work, Env: os.Environ(), Stdin: reader, Stdout: output, Stderr: output, ProcessGroup: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = child.KillGroup() }()
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	if _, err := writer.WriteString("{\"jsonrpc\":\"2.0\",\"id\":0,\"method\":\"initialize\"}\n"); err != nil {
		t.Fatal(err)
	}
	birthPath := filepath.Join(fix.recordDir, "app-server-stdout-child.json")
	birth := waitStartupBirth(t, birthPath)
	if birth.ParentPID != child.Pid() || birth.Group != child.Pid() || birth.Start == 0 {
		t.Fatalf("%s: descendant=%+v want parent/pgid=%d", birthPath, birth, child.Pid())
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, err := gather.NewProcFS("").Stat(child.Pid())
		if os.IsNotExist(err) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("app-server parent %d did not exit: %v (stat: %v)", child.Pid(), ctx.Err(), err)
		case <-ticker.C:
		}
	}
	select {
	case err := <-done:
		t.Fatalf("stdout wait completed before descendant release: %v", err)
	default:
	}
	if got := strings.TrimSpace(output.all()); got != `{"id":0,"jsonrpc":"2.0","result":{}}` {
		t.Fatalf("app-server reply=%q want native initialize result", got)
	}
	if err := os.WriteFile(gate, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatalf("descendant release did not finish stdout: %v", ctx.Err())
	}
}
