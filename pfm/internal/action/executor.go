package action

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"hostops/pfm/internal/compose"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/obs"
	"hostops/pfm/internal/paths"
)

// New constructs an action executor using only K4-overridable paths.
func New(dependencies Dependencies) (*Executor, error) {
	resolved, err := paths.Resolve()
	if err != nil {
		return nil, fmt.Errorf("resolve action paths: %w", err)
	}
	tmux := dependencies.Tmux
	if tmux == nil {
		tmux = TmuxExecutor{TmuxDir: resolved.TmuxDir}
	}
	processes := dependencies.Processes
	if processes == nil {
		processes = RealProcesses{Root: resolved.ProcRoot}
	}
	gate := dependencies.Gate
	if gate == nil {
		gate = DeviceGate{}
	}
	runner := dependencies.Runner
	if runner == nil {
		// Script diagnostics are informational; preserve the one-line stdout
		// eval protocol even when a helper writes to its stdout.
		runner = ExecRunner{Stdout: os.Stderr, Stderr: os.Stderr}
	}
	stderr := dependencies.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	return &Executor{
		tmux:      tmux,
		processes: processes,
		gate:      gate,
		runner:    runner,
		stderr:    stderr,
		sidDir:    resolved.SIDDir,
		heal:      dependencies.Heal,
	}, nil
}

// Open performs all preparatory effects and returns the sole eval line. A
// self-switch returns an empty line by design.
func (executor *Executor) Open(
	ctx context.Context,
	request Request,
) (line string, err error) {
	trail := obs.NewTrail(ctx, "action", "requested")
	defer func() { trail.End(err) }()
	if executor == nil {
		return "", errors.New("action executor is nil")
	}
	if request.Row.Kind.IsLiveSeat() {
		if !executor.tmux.SocketAlive(ctx, request.Row.Socket) {
			if request.Row.ID == "" {
				return "", fmt.Errorf(
					"live socket %q died and its split row has no resumable id",
					request.Row.Socket,
				)
			}
			fmt.Fprintf(
				executor.stderr,
				"pfm: live socket %s disappeared; resuming %s in a fresh server\n",
				request.Row.Socket,
				request.Row.ID,
			)
			request.Row.Kind = compose.ResumeKindFor(request.Row.Kind)
			request.Row.Socket = ""
			request.Row.SessionName = ""
			request.Row.WindowName = ""
		} else {
			if err := executor.prepareLive(ctx, request); err != nil {
				return "", err
			}
			if request.Row.Kind == compose.LiveCodex {
				request.Row.WindowName = executor.verifiedCodexWindow(
					ctx,
					request.Row.Socket,
					request.Row.WindowName,
				)
			}
			if executor.SelfSwitch(
				ctx,
				request.CurrentTMUX,
				request.Row.Socket,
				request.Config.Claude.Binary,
				request.Config.Codex.Binary,
				request.Config.OpenCode.Binary,
			) {
				return "", nil
			}
			plan, err := Synthesize(request)
			if err != nil {
				return "", err
			}
			trail.Reach("opened", "pane attached")
			return plan.Line, nil
		}
	}

	switch request.Row.Kind {
	case compose.Agent:
		if err := executor.Solo(ctx, request.Row.ID, "", true, request.Config.Claude.Binary); err != nil {
			return "", err
		}
	case compose.ResumeClaude:
		if err := executor.Solo(ctx, request.Row.ID, "", false, request.Config.Claude.Binary); err != nil {
			return "", err
		}
	case compose.ResumeCodex:
		// A Codex thread whose history projection is wedged resumes amnesiac
		// at its first prompt while its rollout is whole on disk. Repairing
		// it HERE — before the seat is created — is the one moment the thread
		// is provably not held by a running seat. It never blocks the resume:
		// the repair reports what it did and the chat opens either way.
		if executor.heal != nil {
			if message := executor.heal(ctx, request.Row.ID); message != "" {
				fmt.Fprintln(executor.stderr, message)
			}
		}
	}
	plan, err := Synthesize(request)
	if err != nil {
		return "", err
	}
	if plan.ChatServer != nil {
		if err := executor.tmux.CreateChatServer(
			ctx,
			*plan.ChatServer,
		); err != nil {
			return "", err
		}
	}
	trail.Reach("opened", "pane attached")
	return plan.Line, nil
}

func (executor *Executor) verifiedCodexWindow(
	ctx context.Context,
	socket, expected string,
) string {
	if expected == "" {
		return ""
	}
	panes, err := executor.tmux.ListPanes(ctx, socket)
	if err != nil {
		// A failed probe is not proof the cached window name is stale — it
		// is a probe that could not run. Falling back to unverified (the
		// caller attaches without naming a window, tmux picks its own last-
		// active one) stays the conservative choice; only the silence was
		// wrong.
		obs.Logger(ctx).WarnContext(
			ctx, "verify codex window: list panes failed",
			"err", err, "socket", socket,
		)
		return ""
	}
	for _, pane := range panes {
		if pane.WindowName == expected {
			return expected
		}
	}
	return ""
}

func (executor *Executor) prepareLive(
	ctx context.Context,
	request Request,
) error {
	if request.Row.Kind == compose.LiveClaude || request.Row.Kind == compose.LiveCodex {
		birthCache, wantCache := request.Row.C1H, request.Cache1H
		if request.Row.Kind == compose.LiveCodex {
			birthCache, wantCache = false, false
		}
		reboot, err := executor.gate.Confirm(ctx, GateRequest{
			Name:           request.Row.Name,
			BirthAccount:   request.Row.Account,
			PrimaryAccount: request.PrimaryAccount,
			BirthCache1H:   birthCache,
			WantCache1H:    wantCache,
		})
		if err != nil {
			return fmt.Errorf("open gate: %w", err)
		}
		if reboot {
			cacheValue := "0"
			if request.Cache1H {
				cacheValue = "1"
			}
			arguments := []string{
				"chat",
				"reload",
				"--sock",
				request.Row.Socket,
				strconv.Itoa(request.PrimaryAccount),
				"--1h",
				cacheValue,
			}
			if request.Config.Path != "" {
				arguments = append([]string{"--config", request.Config.Path}, arguments...)
			}
			if err := executor.runner.Run(ctx, "pfm", arguments...); err != nil {
				fmt.Fprintf(
					executor.stderr,
					"pfm: reboot-to-match failed; attaching the live chat as-is: %v\n",
					err,
				)
			}
		}
	}
	if err := executor.Solo(
		ctx,
		request.Row.ID,
		request.Row.Socket,
		false,
		request.Config.Claude.Binary,
	); err != nil {
		return err
	}
	_ = executor.tmux.SetWindowSizeLatest(ctx, request.Row.Socket)
	return nil
}

// SelfSwitch selects the engine window when the caller already lives on the
// target server. It returns true even if selection fails, because nesting the
// server into itself must never become the fallback.
func (executor *Executor) SelfSwitch(
	ctx context.Context,
	currentTMUX, targetSocket string,
	engineCommands ...string,
) bool {
	currentPath := currentTMUX
	if comma := strings.IndexByte(currentPath, ','); comma >= 0 {
		currentPath = currentPath[:comma]
	}
	if currentPath == "" || filepath.Base(currentPath) != targetSocket {
		return false
	}
	panes, err := executor.tmux.ListPanes(ctx, targetSocket)
	if err != nil || len(panes) == 0 {
		if err != nil {
			// A probe failure folds into the same "refuse to nest" outcome a
			// genuinely empty pane list gets — refusing is the conservative
			// choice either way — but the cause is never the same thing as
			// "no panes" and must not vanish silently.
			obs.Logger(ctx).WarnContext(
				ctx, "self-switch: list panes failed",
				"err", err, "socket", targetSocket,
			)
		}
		fmt.Fprintln(
			executor.stderr,
			"pfm: already inside this chat's tmux — refusing to nest it inside itself; switch windows yourself (prefix + w)",
		)
		obs.Transition(ctx, "action", "attached", "switched", "self-switch")(nil)
		return true
	}
	sort.SliceStable(panes, func(left, right int) bool {
		return panes[left].WindowIndex < panes[right].WindowIndex
	})
	window := chooseEngineWindow(panes, engineCommands...)
	if err := executor.tmux.SelectWindow(
		ctx,
		targetSocket,
		window,
	); err != nil {
		fmt.Fprintln(
			executor.stderr,
			"pfm: already inside this chat's tmux — refusing to nest it inside itself; switch windows yourself (prefix + w)",
		)
		obs.Transition(ctx, "action", "attached", "switched", "self-switch")(nil)
		return true
	}
	fmt.Fprintln(
		executor.stderr,
		"pfm: already inside this chat's tmux — switched to its window (a session must never nest inside itself)",
	)
	obs.Transition(ctx, "action", "attached", "switched", "self-switch")(nil)
	return true
}

func chooseEngineWindow(panes []ActionPane, engineCommands ...string) int {
	engines := make(map[string]bool, len(pfmengine.All())+len(engineCommands))
	for _, id := range pfmengine.All() {
		engines[pfmengine.MustLookup(id).Binary] = true
	}
	for _, command := range engineCommands {
		if base := filepath.Base(command); base != "." && base != "" {
			engines[base] = true
		}
	}
	for _, pane := range panes {
		if engines[pane.CurrentCommand] {
			return pane.WindowIndex
		}
	}
	for _, pane := range panes {
		if pane.CurrentCommand == "node" ||
			isNumericVersion(pane.CurrentCommand) {
			return pane.WindowIndex
		}
	}
	return panes[0].WindowIndex
}

func isNumericVersion(command string) bool {
	dot := strings.IndexByte(command, '.')
	if dot <= 0 {
		return false
	}
	for _, character := range command[:dot] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
