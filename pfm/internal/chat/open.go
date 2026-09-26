package chat

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleet"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/heal"
	"github.com/rezzminator/professor/pfm/internal/kill"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// KillResolved records one exact chat kill and optionally confirms that its
// selected pane exited. The structured chat keeps split callers pane-scoped.
func KillResolved(
	ctx context.Context,
	chat headless.Chat,
	exit bool,
	stdout, stderr io.Writer,
	runtime *config.Runtime,
) (exitCode int) {
	var runtimes []config.Runtime
	if runtime != nil {
		runtimes = append(runtimes, *runtime)
	}
	database, manager, code := fleet.OpenKillManager(stderr, runtimes...)
	if code != 0 {
		return code
	}
	defer func() { cli.CloseResource(database, "pfm chat kill: close database", stderr, &exitCode) }()
	target, err := manager.Kill(ctx, kill.Request{
		ID: chat.ID, Engine: chat.Engine, RolloutPath: chat.Path,
		SocketName: chat.Socket, PaneID: chat.Pane, Exit: exit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "pfm chat kill: %v\n", err)
		return 1
	}
	recorded := !pfmengine.SocketKeyedID(target.Engine, target.ID, target.SocketName)
	if exit && target.SocketName != "" && target.PaneID != "" {
		if err := manager.ConfirmExit(ctx, target); err != nil {
			fmt.Fprintf(stderr, "pfm chat kill: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, KillOutcome(target.ID, target.SocketName, target.PaneID, recorded))
		return 0
	}
	fmt.Fprintln(stdout, KillOutcome(target.ID, "", "", recorded))
	return 0
}

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
		return openDetachedResolvedRow(ctx, row, stderr, effective)
	}
	return action.OpenResult{}, fmt.Errorf("chat %q is not indexed", id)
}

// OpenDetachedTarget opens one already-resolved chat without scanning for its
// aggregate row again. Request-scoped split callers retain their exact pane,
// transcript identity and current session through this door.
func OpenDetachedTarget(
	ctx context.Context,
	target headless.Chat,
	stderr io.Writer,
	runtime *config.Runtime,
) (action.OpenResult, error) {
	effective, err := config.RuntimeOrDefault(runtime)
	if err != nil {
		return action.OpenResult{}, err
	}
	if target.Engine == pfmengine.Claude && target.Live {
		rows, rowsErr := Rows(ctx, stderr, &effective)
		if rowsErr != nil {
			return action.OpenResult{}, rowsErr
		}
		if row, found := exactAgentRow(rows, target); found {
			return openDetachedResolvedRow(ctx, row, stderr, effective)
		}
	}
	kind, err := detachedTargetKind(target)
	if err != nil {
		return action.OpenResult{}, err
	}
	if kind == compose.Agent {
		return action.OpenResult{}, fmt.Errorf("resolved agent %q disappeared from the fleet", target.ID)
	}
	row := compose.Row{
		Kind: kind, ID: target.ID, Path: target.Path, Name: target.Name,
		CWD: target.CWD, Socket: target.Socket, SessionName: target.Session, PaneID: target.Pane,
	}
	return openDetachedResolvedRow(ctx, row, stderr, effective)
}

// exactAgentRow recovers launch-only metadata that headless.Chat does not
// carry. It never re-resolves the target: the already-resolved id and seat
// must agree, and the row is used only to preserve the owning config dir that
// internal agent-open needs.
func exactAgentRow(rows []compose.Row, target headless.Chat) (compose.Row, bool) {
	for index := range rows {
		row := rows[index]
		if row.Kind != compose.Agent || row.ID != target.ID {
			continue
		}
		if target.Socket != "" && row.Socket != target.Socket {
			continue
		}
		if target.Pane != "" && row.PaneID != target.Pane {
			continue
		}
		return row, true
	}
	return compose.Row{}, false
}

func openDetachedResolvedRow(
	ctx context.Context,
	row compose.Row,
	stderr io.Writer,
	effective config.Runtime,
) (action.OpenResult, error) {
	fleetPrimary, err := fleet.PrimaryAccount(effective.Paths, effective.Config)
	if err != nil {
		return action.OpenResult{}, fmt.Errorf("read primary account: %w", err)
	}
	primary := effective.Config.PrimaryAccountFor(compose.EngineForKind(row.Kind), fleetPrimary)
	return openDetachedRow(ctx, row, primary, stderr, effective)
}

func detachedTargetKind(target headless.Chat) (compose.Kind, error) {
	switch target.Engine {
	case pfmengine.Claude:
		if target.Live {
			// Composed live Claude rows always carry their server. A live Claude
			// target without one is the agent-router shape and must retain Agent's
			// no-Solo open path.
			if target.Socket == "" {
				return compose.Agent, nil
			}
			return compose.LiveClaude, nil
		}
		return compose.ResumeClaude, nil
	case pfmengine.Codex:
		if target.Live {
			return compose.LiveCodex, nil
		}
		return compose.ResumeCodex, nil
	case pfmengine.OpenCode:
		if target.Live {
			return compose.LiveOpenCode, nil
		}
		return compose.ResumeOpenCode, nil
	default:
		return 0, fmt.Errorf("open target %q has unknown engine %q", target.Name, target.Engine)
	}
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
