package gather_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	fleetindex "github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/naming"
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
	t.Setenv(paths.EnvCacheDB, filepath.Join(root, "pfm-cache.db"))
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

	// A name a user chose (the session_index rename) leaves the automatic-name
	// option as it was; a first-prompt fallback sets it to the name it wrote,
	// so the Herdr reporter keeps a tab label the user set against it.
	const fallbackID = "22222222-2222-4222-8222-222222222222"
	_, byID := naming.CodexNameIndex([]naming.CodexThread{{
		ID: fallbackID, Path: filepath.Join(codexHome, "fallback.jsonl"), FirstPrompt: "FIRST_PROMPT",
	}}, names)
	for index, test := range []struct {
		name, threadID, wantName, wantAuto string
	}{
		{"a session_index name is the user's", threadID, "INDEX_TWIN", ""},
		{"a first-prompt fallback is automatic", fallbackID, "FIRST_PROMPT", "FIRST_PROMPT"},
	} {
		t.Run(test.name, func(t *testing.T) {
			chatName, auto := convergeCodexProbeWindow(t, root, index, test.threadID, func(id string) naming.CodexName {
				return byID[id]
			})
			if chatName != test.wantName || auto != test.wantAuto {
				t.Fatalf(
					"@pfm_chat_name=%q @pfm_chat_name_auto=%q, want %q and %q",
					chatName, auto, test.wantName, test.wantAuto,
				)
			}
		})
	}
}

// convergeCodexProbeWindow renames a fresh probe server's window after the
// Codex thread threadID resolves to and answers the server's chat name and
// automatic-name options.
func convergeCodexProbeWindow(
	t *testing.T,
	root string,
	index int,
	threadID string,
	resolveID gather.CodexIDNameResolver,
) (chatName, auto string) {
	t.Helper()
	socket := fmt.Sprintf("probe-pfm-codex-name-%d-%d", os.Getpid(), index)
	probeTmux := func(args ...string) string {
		t.Helper()
		command := exec.Command("tmux", append([]string{"-L", socket}, args...)...)
		command.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %q: %v: %s", args, err, output)
		}
		return strings.TrimSpace(string(output))
	}
	probeTmux("-f", "/dev/null", "new-session", "-d", "-s", socket, "-n", "before", "sleep 120")
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-L", socket, "kill-server")
		kill.Env = append(os.Environ(), "TMUX=", "TMUX_TMPDIR="+root)
		_ = kill.Run()
	})
	windowID := probeTmux("list-windows", "-F", "#{window_id}")
	want := resolveID(threadID).Name
	renames := gather.ComputeWindowRenames(
		[]gather.ProbePane{{
			Socket:      socket,
			SessionName: socket,
			WindowID:    windowID,
			WindowName:  "before",
			PaneID:      "%0",
		}},
		[]gather.LiveCodex{{Socket: socket, PaneID: "%0", ThreadID: threadID}},
		nil,
		nil,
		resolveID,
	)
	if len(renames) != 1 || renames[0].TargetName != want {
		t.Fatalf("renames=%#v, want one to %q", renames, want)
	}
	// N2: the Codex half states the thread's exact name for the Herdr reporter.
	if renames[0].ChatName != want {
		t.Fatalf("rename ChatName = %q, want the thread name %q", renames[0].ChatName, want)
	}
	if err := (gather.TmuxProbe{TmuxTmpDir: root}).RenameWindow(
		context.Background(), renames[0],
	); err != nil {
		t.Fatal(err)
	}
	if got := probeTmux("display-message", "-p", "#{window_name}"); got != want {
		t.Fatalf("window name=%q, want %q", got, want)
	}
	return probeTmux("show-options", "-qv", "-t", socket, "@pfm_chat_name"),
		probeTmux("show-options", "-qv", "-t", socket, "@pfm_chat_name_auto")
}
