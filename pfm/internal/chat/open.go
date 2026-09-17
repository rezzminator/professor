package chat

import (
	"context"
	"fmt"
	"io"
	"os"

	"hostops/pfm/internal/action"
	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/compose"
	"hostops/pfm/internal/config"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/fleet"
	"hostops/pfm/internal/heal"
	"hostops/pfm/internal/spawn"
	"hostops/pfm/internal/store"
)

// OpenID resolves an indexed chat ID through a read-only fleet scan and opens it.
func OpenID(
	ctx context.Context,
	id string,
	stdout, stderr io.Writer,
	runtime *config.Runtime,
) (exitCode int) {
	effective, err := config.RuntimeOrDefault(runtime)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	database, err := store.Open(store.WithWarningWriter(stderr))
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	defer func() { cli.CloseResource(database, "pfm chat open: close database", stderr, &exitCode) }()
	scan, err := fleet.Scan(ctx, database, fleet.Request{
		View: compose.AllView, ReadOnly: true, Runtime: &effective,
	}, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	for index := range scan.Output.Rows {
		row := &scan.Output.Rows[index]
		if row.ID == id {
			primary := fleet.PrimaryAccount(scan.Env.Paths, effective.Config)
			return OpenRow(
				ctx,
				*row,
				effective.Config.PrimaryAccountFor(compose.EngineForKind(row.Kind), primary),
				effective.Config.InitialCache1H(primary),
				"",
				stdout,
				stderr,
				&effective,
			)
		}
	}
	fmt.Fprintf(stderr, "pfm chat open: chat %q is not indexed\n", id)
	return 1
}

// OpenRow builds and dispatches the action for one composed row.
func OpenRow(
	ctx context.Context,
	row compose.Row,
	primary int,
	cache1H bool,
	prompt string,
	stdout, stderr io.Writer,
	runtime *config.Runtime,
) int {
	effective, err := config.RuntimeOrDefault(runtime)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	resolved := effective.Paths
	if row.Kind != compose.LiveClaude && row.Kind != compose.LiveCodex && row.Kind != compose.LiveSplit {
		if info, statErr := os.Stat(row.CWD); statErr != nil || !info.IsDir() {
			if currentDir, cwdErr := os.Getwd(); cwdErr == nil {
				row.CWD = currentDir
			}
		}
	}
	healCodexRoot := resolved.FirstRoot(pfmengine.Codex)
	if account, found := effective.Config.CodexAccountByID(primary); found {
		healCodexRoot = account.Home
	}
	executor, err := action.New(action.Dependencies{
		Stderr: stderr,
		Heal: func(ctx context.Context, threadID string) string {
			return heal.Thread(ctx, healCodexRoot, threadID)
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	fresh, err := socketForKind(row.Kind)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	line, err := executor.Open(ctx, action.Request{
		Row:            row,
		Prompt:         prompt,
		PrimaryAccount: primary,
		Cache1H:        cache1H,
		Bunker:         fleet.CurrentSocket() == "vsct",
		Home:           resolved.Home,
		FreshSocket:    fresh,
		CurrentTMUX:    os.Getenv("TMUX"),
		Config:         effective.Config,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	if line != "" {
		if err := action.Dispatch(stdout, line); err != nil {
			fmt.Fprintf(stderr, "pfm chat open: execute action: %v\n", err)
			return 1
		}
	}
	return 0
}

func socketForKind(kind compose.Kind) (string, error) {
	id, err := compose.EngineForKindChecked(kind)
	if err != nil {
		return "", err
	}
	return spawn.FreshSocket(id), nil
}
