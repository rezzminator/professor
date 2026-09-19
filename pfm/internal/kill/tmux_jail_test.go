package kill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/index"
	"hostops/pfm/internal/store"
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
	t.Setenv("PFM_DB", filepath.Join(root, "fleet.db"))
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
	content := "#!/bin/sh\nIFS= read -r line\n"
	if appendPrompt {
		content += "printf '%s\\n' " +
			shellQuote(`{"type":"user","cwd":"/work/exit","message":{"content":"goodbye flush"}}`) +
			" >> " + shellQuote(transcriptPath) + "\n"
	}
	content += "exit 0\n"
	if err := os.WriteFile(scriptPath, []byte(content), 0o700); err != nil {
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
	if _, err := manager.Kill(ctx, Request{
		Self: true,
		Exit: true,
		Environment: SelfEnvironment{
			TMUX:            socketPath + ",1,0",
			TMUXPane:        paneID,
			ClaudeSessionID: id,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if len(spawner.args) != 1 {
		t.Fatalf("spawn args = %#v", spawner.args)
	}

	finisher, err := NewFinisher(database, Dependencies{
		Tmux:         TmuxKiller{},
		Delay:        10 * time.Millisecond,
		PollEvery:    10 * time.Millisecond,
		PollAttempts: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := finisher.Run(ctx, spawner.args[0]); err != nil {
		t.Fatal(err)
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
	jail := newKillTmuxJail(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
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
	finisher, err := NewFinisher(database, Dependencies{
		Tmux:         TmuxKiller{},
		Refresher:    refreshFunc(func(context.Context) error { return nil }),
		Delay:        5 * time.Millisecond,
		PollEvery:    5 * time.Millisecond,
		PollAttempts: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
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
	elapsed := time.Since(started)
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
		"STRESS kill-exit panes=%d dead=%d crumbs_remaining=0 elapsed=%s",
		count,
		count,
		elapsed,
	)
}
