//go:build jail

package kill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/index"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

type killTmuxJail struct {
	root       string
	tmuxDir    string
	sidDir     string
	home       string
	claudeRoot string
	codexHome  string
	sockets    []string
}

func newKillTmuxJail(t *testing.T) *killTmuxJail {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}
	root, err := os.MkdirTemp("/tmp", "cch")
	if err != nil {
		t.Fatal(err)
	}
	jail := &killTmuxJail{
		root:       root,
		tmuxDir:    filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())),
		sidDir:     filepath.Join(root, "sid"),
		home:       filepath.Join(root, "home"),
		claudeRoot: filepath.Join(root, "claude"),
		codexHome:  filepath.Join(root, "codex"),
	}
	for _, directory := range []string{
		jail.tmuxDir,
		jail.sidDir,
		jail.home,
		jail.claudeRoot,
		jail.codexHome,
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", jail.home)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", jail.root)
	t.Setenv("PFM_HOME", jail.home)
	t.Setenv("PFM_CACHE_DB", filepath.Join(root, "pfm-cache.db"))
	t.Setenv("PFM_SID_DIR", jail.sidDir)
	t.Setenv("PFM_CLAUDE_ROOTS", jail.claudeRoot)
	t.Setenv("PFM_CODEX_ROOT", jail.codexHome)
	t.Setenv("PFM_TMUX_DIR", jail.tmuxDir)
	t.Cleanup(func() {
		for _, socket := range jail.sockets {
			_ = jail.command("-L", socket, "kill-server").Run()
		}
		if err := os.RemoveAll(jail.root); err != nil {
			t.Errorf("remove kill tmux jail: %v", err)
		}
	})
	return jail
}

func (jail *killTmuxJail) command(arguments ...string) *exec.Cmd {
	command := exec.Command("tmux", arguments...)
	command.Env = append(
		os.Environ(),
		"HOME="+jail.home,
		"TMUX=",
		"TMUX_TMPDIR="+jail.root,
	)
	return command
}

func (jail *killTmuxJail) startReader(
	t *testing.T,
	socket, session, transcriptPath string,
	appendPrompt bool,
) string {
	t.Helper()
	scriptPath := filepath.Join(jail.root, socket+".sh")
	content := "#!/bin/sh\nprintf '❯ \\n────\\n'\nIFS= read -r line\n"
	if appendPrompt {
		content += "printf '%s\\n' " +
			shellQuote(`{"type":"user","cwd":"/work/exit","message":{"content":"goodbye flush"}}`) +
			" >> " + shellQuote(transcriptPath) + "\n"
	}
	content += "exit 0\n"
	if err := testjail.WriteExecutable(scriptPath, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	command := jail.command(
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		session,
		scriptPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start reader %s: %v: %s", socket, err, output)
	}
	jail.sockets = append(jail.sockets, socket)
	output, err := jail.command(
		"-L",
		socket,
		"list-panes",
		"-F",
		"#{pane_id}",
	).Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func TestJailedKillExitFlushesAndSweeps(t *testing.T) {
	testjail.TmuxServer(t)
	jail := newKillTmuxJail(t)
	ctx := context.Background()
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()

	id := "88888888-8888-4888-8888-888888888888"
	projectDir := filepath.Join(jail.claudeRoot, "project")
	transcriptPath := filepath.Join(projectDir, id+".jsonl")
	writeTestFile(
		t,
		transcriptPath,
		`{"type":"user","cwd":"/work/exit","message":{"content":"before"}}`+"\n",
	)
	indexer, err := index.New(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := indexer.Run(ctx, index.Options{}); err != nil {
		t.Fatal(err)
	}

	socketName := "cc-800-1-1"
	paneID := jail.startReader(
		t,
		socketName,
		"exit-session",
		transcriptPath,
		true,
	)
	socketPath := filepath.Join(jail.tmuxDir, socketName)
	writeTestFile(t, filepath.Join(jail.sidDir, socketName), transcriptPath)
	writeTestFile(
		t,
		filepath.Join(jail.sidDir, socketName+"."+paneID),
		transcriptPath,
	)
	detachedPath := filepath.Join(
		jail.home,
		".claude",
		".cc-new-children",
		id,
	)
	paneChildrenPath := filepath.Join(
		jail.home,
		".claude",
		".cc-pane-children",
		id,
	)
	writeTestFile(t, detachedPath, "cc-does-not-exist\n")
	writeTestFile(t, paneChildrenPath, "cc-does-not-exist\t%77\n")

	spawner := &captureSpawner{}
	manager, err := New(database, Dependencies{
		ProcFS:  &fakeProc{},
		Tmux:    TmuxKiller{},
		Spawner: spawner,
		Now:     func() time.Time { return time.Unix(500, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	manager.identifier = killTestIdentity{
		identity: resolve.Identity{
			Backend:    "tmux",
			ID:         id,
			Engine:     string(pfmengine.Claude),
			SocketPath: socketPath,
			SocketName: filepath.Base(socketPath),
			Pane:       paneID,
		},
	}
	if _, err := manager.Kill(ctx, Request{Self: true, Exit: true}); err != nil {
		t.Fatal(err)
	}
	if len(spawner.args) != 1 {
		t.Fatalf("spawn args = %#v", spawner.args)
	}

	waits := newTmuxExitWaits(t)
	finisher, err := NewFinisher(database, Dependencies{
		Tmux:   TmuxKiller{},
		Screen: waits,
		Clock:  waits,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := finisher.Run(ctx, spawner.args[0]); err != nil {
		t.Fatal(err)
	}
	// The chat exited on the typed /exit, flushing its last prompt, never by
	// the fallback kill once the grace window ran out.
	body, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatalf("read transcript %s: %v", transcriptPath, err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	if want := `{"type":"user","cwd":"/work/exit","message":{"content":"goodbye flush"}}`; lines[len(lines)-1] != want {
		t.Fatalf("transcript %s last line = %q, want %q", transcriptPath, lines[len(lines)-1], want)
	}
	if exists, err := (TmuxKiller{}).PaneExists(ctx, socketPath, paneID); err != nil {
		t.Fatalf("probe pane after kill-exit: %v", err)
	} else if exists {
		t.Fatal("target pane survived kill-exit")
	}
	for _, path := range []string{
		filepath.Join(jail.sidDir, socketName),
		filepath.Join(jail.sidDir, socketName+"."+paneID),
		detachedPath,
		paneChildrenPath,
	} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s remains: %v", path, err)
		}
	}
	assertKilled(t, database, id, pfmengine.Claude, 500)
}

func TestStressTenSimultaneousKillExits(t *testing.T) {
	testjail.TmuxServer(t)
	jail := newKillTmuxJail(t)
	ctx := testwait.Context(t)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	}()

	const count = 10
	args := make([]ExitArgs, 0, count)
	for position := 0; position < count; position++ {
		id := fmt.Sprintf("kill-exit-%02d", position)
		path := filepath.Join(jail.claudeRoot, id+".jsonl")
		if err := database.UpsertTranscript(ctx, store.Transcript{
			UUID:        id,
			Path:        path,
			PromptCount: 1,
		}); err != nil {
			t.Fatal(err)
		}
		if err := database.Kill(ctx, store.Killed{
			ID:       id,
			Engine:   pfmengine.Claude,
			KilledAt: int64(position + 1),
		}); err != nil {
			t.Fatal(err)
		}
		socketName := fmt.Sprintf("cc-%d-1-1", 900+position)
		paneID := jail.startReader(
			t,
			socketName,
			fmt.Sprintf("kill-%02d", position),
			path,
			false,
		)
		writeTestFile(t, filepath.Join(jail.sidDir, socketName), path)
		writeTestFile(
			t,
			filepath.Join(jail.sidDir, socketName+"."+paneID),
			path,
		)
		args = append(args, ExitArgs{
			Engine:     pfmengine.Claude,
			ID:         id,
			DataPath:   path,
			SocketPath: filepath.Join(jail.tmuxDir, socketName),
			SocketName: socketName,
			PaneID:     paneID,
		})
	}
	waits := newTmuxExitWaits(t)
	finisher, err := NewFinisher(database, Dependencies{
		Tmux:      TmuxKiller{},
		Refresher: refreshFunc(func(context.Context) error { return nil }),
		Screen:    waits,
		Clock:     waits,
	})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errorsByIndex := make([]error, len(args))
	for position := range args {
		position := position
		group.Add(1)
		go func() {
			defer group.Done()
			errorsByIndex[position] = finisher.Run(ctx, args[position])
		}()
	}
	group.Wait()
	for position, runErr := range errorsByIndex {
		if runErr != nil {
			t.Fatalf("finisher %d: %v", position, runErr)
		}
		if exists, err := (TmuxKiller{}).PaneExists(
			context.Background(),
			args[position].SocketPath,
			args[position].PaneID,
		); err != nil {
			t.Fatalf("probe pane %d after kill-exit: %v", position, err)
		} else if exists {
			t.Fatalf("pane %d survived", position)
		}
		for _, crumb := range []string{
			filepath.Join(jail.sidDir, args[position].SocketName),
			filepath.Join(
				jail.sidDir,
				args[position].SocketName+"."+args[position].PaneID,
			),
		} {
			if _, statErr := os.Stat(crumb); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("crumb %s remains: %v", crumb, statErr)
			}
		}
	}
	t.Logf(
		"STRESS kill-exit panes=%d dead=%d crumbs_remaining=0",
		count,
		count,
	)
}

// tmuxExitWaits is the finisher's screen and clock against real tmux panes.
// The screen is the real pane door, recording each pane Enter was pressed in.
// A wait with no such pane still open steps at once, as a fake pane would; a
// wait while one is open holds until every such pane has closed, so the poll
// after it reads the pane's real end, never a count of real polls.
type tmuxExitWaits struct {
	pfmcall.Screen
	*finisherStepClock
	mu      sync.Mutex
	entered []pfmcall.Target
}

func newTmuxExitWaits(t *testing.T) *tmuxExitWaits {
	t.Helper()
	screen, err := pfmcall.New(pfmcall.Dependencies{})
	if err != nil {
		t.Fatalf("pane door: %v", err)
	}
	return &tmuxExitWaits{Screen: screen, finisherStepClock: newFinisherStepClock()}
}

func (waits *tmuxExitWaits) SendKey(ctx context.Context, target pfmcall.Target, key string) error {
	err := waits.Screen.SendKey(ctx, target, key)
	if err == nil && key == "Enter" {
		waits.mu.Lock()
		waits.entered = append(waits.entered, target)
		waits.mu.Unlock()
	}
	return err
}

func (waits *tmuxExitWaits) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	waits.mu.Lock()
	entered := slices.Clone(waits.entered)
	waits.mu.Unlock()
	err := testwait.UntilErr("every pane Enter was pressed in to close", func() (bool, string) {
		for index := range entered {
			target := &entered[index]
			exists, probeErr := (TmuxKiller{}).PaneExists(ctx, target.SocketPath, target.Pane)
			if probeErr != nil {
				return false, fmt.Sprintf("probe pane %s: %v", target.Pane, probeErr)
			}
			if exists {
				return false, "pane " + target.Pane + " still open"
			}
		}
		return true, fmt.Sprintf("%d entered panes closed", len(entered))
	})
	if err != nil {
		return err
	}
	return waits.finisherStepClock.Sleep(ctx, d)
}
