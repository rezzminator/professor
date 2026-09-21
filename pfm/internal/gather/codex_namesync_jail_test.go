package gather

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	fleetindex "github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestSessionIndexRenameConvergesAProbeWindow(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root := testjail.ShortRoot(t)
	home := filepath.Join(root, "home")
	codexHome := filepath.Join(root, "codex")
	for _, directory := range []string{home, codexHome, filepath.Join(root, "claude")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv(paths.EnvHome, home)
	t.Setenv(paths.EnvDB, filepath.Join(root, "pfm.db"))
	t.Setenv(paths.EnvSIDDir, filepath.Join(root, "sid"))
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(root, "claude"))
	t.Setenv(paths.EnvCodexHome, codexHome)
	t.Setenv("TMUX_TMPDIR", root)
	t.Setenv(paths.EnvTmuxDir, filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())))
	t.Setenv(paths.EnvTmuxConf, "/dev/null")

	const threadID = "11111111-1111-4111-8111-111111111111"
	indexLine := `{"id":"` + threadID + `","thread_name":"INDEX_TWIN","updated_at":"2026-08-16T12:00:00Z"}` + "\n"
	if err := os.WriteFile(
		filepath.Join(codexHome, "session_index.jsonl"),
		[]byte(indexLine),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()
	indexer, err := fleetindex.New(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexer.Run(context.Background(), fleetindex.Options{}); err != nil {
		t.Fatal(err)
	}
	names, err := database.CxNames(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if names[threadID] != "INDEX_TWIN" {
		t.Fatalf("indexed name=%q", names[threadID])
	}

	socket := fmt.Sprintf("probe-pfm-codex-name-%d", os.Getpid())
	start := exec.Command(
		"tmux", "-L", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", socket, "-n", "before", "sleep 120",
	)
	start.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	if output, err := start.CombinedOutput(); err != nil {
		t.Fatalf("start probe server: %v: %s", err, output)
	}
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", socket, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
		_ = kill.Run()
	})
	windowIDCommand := exec.Command(
		"tmux", "-L", socket, "list-windows", "-F", "#{window_id}",
	)
	windowIDCommand.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	windowIDOutput, err := windowIDCommand.Output()
	if err != nil {
		t.Fatal(err)
	}
	windowID := strings.TrimSpace(string(windowIDOutput))
	renames := computeWindowRenames(
		[]ProbePane{{
			Socket:      socket,
			SessionName: socket,
			WindowID:    windowID,
			WindowName:  "before",
			PaneID:      "%0",
		}},
		[]LiveCodex{{Socket: socket, PaneID: "%0", ThreadID: threadID}},
		nil,
		nil,
		func(id string) string { return names[id] },
	)
	if len(renames) != 1 || renames[0].TargetName != "INDEX_TWIN" {
		t.Fatalf("renames=%#v", renames)
	}
	if err := (TmuxProbe{TmuxTmpDir: root}).RenameWindow(
		context.Background(), renames[0],
	); err != nil {
		t.Fatal(err)
	}
	readName := exec.Command(
		"tmux", "-L", socket, "display-message", "-p", "#{window_name}",
	)
	readName.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
	output, err := readName.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(output)); got != "INDEX_TWIN" {
		t.Fatalf("window name=%q, want INDEX_TWIN", got)
	}
}
