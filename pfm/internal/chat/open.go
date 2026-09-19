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
	"hostops/pfm/internal/paths"
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
			primary, primaryErr := fleet.PrimaryAccount(scan.Env.Paths, effective.Config)
			if primaryErr != nil {
				fmt.Fprintf(stderr, "pfm chat open: read primary account: %v\n", primaryErr)
				return 1
			}
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

// OpenDetachedID opens an indexed chat for a caller that has no terminal to be
// replaced by an attach — the MCP daemon. It is OpenID's own resolution and
// preparation over action.Executor.OpenDetached: no K1 eval line, no attach,
// and the result reports whether the chat was already live or has just been
// started. The caller resolves its target first (Target), so an id that is not
// indexed here is a resolution and a scan disagreeing, not a caller's typo.
func OpenDetachedID(
	ctx context.Context,
	id string,
	stderr io.Writer,
	runtime *config.Runtime,
) (action.OpenResult, error) {
	effective, err := config.RuntimeOrDefault(runtime)
	if err != nil {
		return action.OpenResult{}, err
	}
	rows, err := Rows(ctx, stderr, &effective)
	if err != nil {
		return action.OpenResult{}, err
	}
	for index := range rows {
		row := rows[index]
		if row.ID != id {
			continue
		}
		fleetPrimary, err := fleet.PrimaryAccount(effective.Paths, effective.Config)
		if err != nil {
			return action.OpenResult{}, fmt.Errorf("read primary account: %w", err)
		}
		primary := effective.Config.PrimaryAccountFor(compose.EngineForKind(row.Kind), fleetPrimary)
		return openDetachedRow(ctx, row, primary, stderr, effective)
	}
	return action.OpenResult{}, fmt.Errorf("chat %q is not indexed", id)
}

// openDetachedRow is the detached door over one composed row.
func openDetachedRow(
	ctx context.Context,
	row compose.Row,
	primary int,
	stderr io.Writer,
	effective config.Runtime,
) (action.OpenResult, error) {
	// OpenRow's os.Getwd() substitution has no counterpart here: a daemon
	// stands in no directory that means anything to the chat it is opening,
	// and a resumable row whose recorded directory has disappeared would
	// otherwise be born in whatever directory the server was started from.
	// The fleet home is the one defensible answer, and the result says so.
	relocated := ""
	if !row.Kind.IsLiveSeat() {
		if info, err := os.Stat(row.CWD); err != nil || !info.IsDir() {
			relocated = fmt.Sprintf(
				"opened in %s: its own directory %s is gone", effective.Paths.Home, row.CWD,
			)
			row.CWD = effective.Paths.Home
		}
	}
	executor, request, err := prepareOpen(row, primary, effective.Config.InitialCache1H(primary), "", stderr, effective)
	if err != nil {
		return action.OpenResult{}, err
	}
	result, err := executor.OpenDetached(ctx, request)
	if err != nil {
		return action.OpenResult{}, err
	}
	result.Detail = relocated
	return result, nil
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
	if !row.Kind.IsLiveSeat() {
		if info, statErr := os.Stat(row.CWD); statErr != nil || !info.IsDir() {
			if currentDir, cwdErr := os.Getwd(); cwdErr == nil {
				row.CWD = currentDir
			}
		}
	}
	executor, request, err := prepareOpen(row, primary, cache1H, prompt, stderr, effective)
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat open: %v\n", err)
		return 1
	}
	line, err := executor.Open(ctx, request)
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

// newOpenExecutor is action.New, the one executor constructor both open doors
// build through. It is a package var solely so this package's jailed tests can
// swap the tmux and process effects while keeping the dependencies the
// preparation below wired — production never assigns it.
var newOpenExecutor = action.New

// prepareOpen is the preparation both open doors share: the executor carrying
// the pre-resume Codex heal, the fresh socket for the row's engine, and the
// action request itself. One preparation, two doors — OpenRow dispatches the
// K1 eval line it yields, OpenDetachedID drives the detached door with it, and
// neither may drift from the other's idea of how a chat is opened.
func prepareOpen(
	row compose.Row,
	primary int,
	cache1H bool,
	prompt string,
	stderr io.Writer,
	effective config.Runtime,
) (*action.Executor, action.Request, error) {
	healCodexRoot := effective.Paths.FirstRoot(pfmengine.Codex)
	if account, found := effective.Config.CodexAccountByID(primary); found {
		healCodexRoot = account.Home
	}
	executor, err := newOpenExecutor(action.Dependencies{
		Stderr: stderr,
		Heal: func(ctx context.Context, threadID string) string {
			return heal.Thread(ctx, healCodexRoot, threadID)
		},
	})
	if err != nil {
		return nil, action.Request{}, err
	}
	fresh, err := socketForKind(row.Kind)
	if err != nil {
		return nil, action.Request{}, err
	}
	return executor, action.Request{
		Row:            row,
		Prompt:         prompt,
		PrimaryAccount: primary,
		Cache1H:        cache1H,
		Bunker:         fleet.CurrentSocket() == "vsct",
		Home:           effective.Paths.Home,
		FreshSocket:    fresh,
		CurrentTMUX:    (paths.OSEnv{}).Get("TMUX"),
		Config:         effective.Config,
	}, nil
}

func socketForKind(kind compose.Kind) (string, error) {
	id, err := compose.EngineForKindChecked(kind)
	if err != nil {
		return "", err
	}
	return spawn.FreshSocket(id), nil
}
