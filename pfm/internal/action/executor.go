package action

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
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
	place := dependencies.Place
	if place == nil {
		place = func(context.Context, string, string) (spawn.BackendChoice, error) {
			return spawn.BackendChoice{Backend: spawn.BackendTmux}, nil
		}
	}
	return &Executor{
		place:     place,
		tmux:      tmux,
		processes: processes,
		gate:      gate,
		runner:    runner,
		stderr:    stderr,
		sidDir:    resolved.SIDDir,
		heal:      dependencies.Heal,
	}, nil
}

// Open is the one door that opens a chat row: it performs every preparatory
// effect and asks the request's placement once for a birth. Interactive, it
// returns the sole eval line in result.Line (a self-switch returns an empty
// line by design). Detached (request.Detached: the MCP daemon, a schedule's
// wake) it never dispatches a K1 eval line and never calls syscall.Exec: a
// live chat already has a running session, so nothing is spawned and its own
// socket is reported ("live"); a resumable chat is born where the placement
// says (a new Herdr tab, never in place, or its own fresh tmux server) and
// the socket it now lives on is reported ("opened").
func (executor *Executor) Open(
	ctx context.Context,
	request Request,
) (result OpenResult, err error) {
	trail := obs.NewTrail(ctx, "action", "requested")
	defer func() { trail.End(err) }()
	if request.Detached {
		defer func() {
			if err != nil {
				err = fmt.Errorf("open detached: %w", err)
			}
		}()
	}
	if executor == nil {
		return result, errors.New("action executor is nil")
	}
	if request.Row.Kind.IsLiveSeat() {
		gone, probeErr := liveSocketGone(ctx, executor.tmux, request.Row.Socket)
		if probeErr != nil {
			return result, probeErr
		}
		if gone {
			// A live row whose socket has disappeared resumes fresh rather
			// than being reported live over a socket nothing answers on.
			if request.Row.ID == "" {
				return result, fmt.Errorf(
					"live socket %q died and its split row has no resumable id",
					request.Row.Socket,
				)
			}
			if !request.Detached {
				fmt.Fprintf(
					executor.stderr,
					"pfm: live socket %s disappeared; resuming %s in a fresh server\n",
					request.Row.Socket,
					request.Row.ID,
				)
			}
			request.Row.Kind = compose.ResumeKindFor(request.Row.Kind)
			request.Row.Socket = ""
			request.Row.SessionName = ""
			request.Row.WindowName = ""
		} else if !request.Detached {
			if err := executor.prepareLive(ctx, request); err != nil {
				return result, err
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
				return result, nil
			}
			plan, err := Synthesize(request)
			if err != nil {
				return result, err
			}
			trail.Reach("opened", "pane attached")
			return OpenResult{Line: plan.Line}, nil
		}
	}

	// A workbench refusal comes before Solo closes any seat holding the chat.
	personaWarnings, err := applyWorkbench(&request)
	if err != nil {
		return result, err
	}
	if err := checkCodexLaunch(request); err != nil {
		return result, err
	}
	switch request.Row.Kind {
	case compose.Agent, compose.ResumeClaude:
		if err := executor.Solo(
			ctx, request.Row.ID, "", request.Row.Kind == compose.Agent, request.Config.Claude.Binary,
		); err != nil {
			return result, err
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
		return result, err
	}
	plan.Warnings = slices.Concat(personaWarnings, plan.Warnings)
	if plan.ChatServer == nil && request.Detached {
		// Live and booting rows already have a running session: there is
		// nothing to spawn, and the eval line Synthesize built is an attach
		// instruction for a terminal this caller does not have.
		trail.Reach("opened", "already live")
		return OpenResult{Name: request.Row.Name, Socket: request.Row.Socket, State: "live"}, nil
	}
	if born(request.Row.Kind) {
		choice, err := executor.place(ctx, "picker", request.Config.Chat.Backend)
		if err != nil {
			return result, err
		}
		if choice.Backend == spawn.BackendHerdr {
			return executor.openHerdr(ctx, request, plan, choice)
		}
	}
	if plan.ChatServer != nil {
		if err := executor.openTmux(ctx, request, plan); err != nil {
			return result, err
		}
		held, err := executor.watchLaunch(
			ctx,
			*plan.ChatServer,
			request.Row.Name,
			request.Config.Claude.Binary,
			request.Config.Codex.Binary,
			request.Config.OpenCode.Binary,
		)
		if err != nil {
			return result, err
		}
		warnings := launchWarnings(plan.Warnings, held)
		if request.Detached {
			// A caller with no terminal never sees the pane: a chat that died
			// at launch, or one held at a dialog only the human may answer,
			// reaches it through this result or not at all.
			trail.Reach("opened", "detached server created")
			return OpenResult{
				Name: request.Row.Name, Socket: plan.ChatServer.Socket, State: "opened", Warnings: warnings,
			}, nil
		}
		launchnotice.Hold(executor.stderr, "pfm", warnings)
	}
	trail.Reach("opened", "pane attached")
	return OpenResult{Line: plan.Line}, nil
}

// born reports a row kind whose open starts a new engine process.
func born(kind compose.Kind) bool {
	switch kind {
	case compose.NewClaude, compose.NewCodex, compose.NewOpenCode,
		compose.ResumeClaude, compose.ResumeCodex, compose.ResumeOpenCode:
		return true
	}
	return false
}

// openHerdr births the planned chat on the placement's Herdr host: a new
// tab, or (interactive, when placed in place and the caller's workspace is the
// chat's place's: spawn.HerdrOpensInPlace) the caller's own pane, whose shell
// then execs the seat host through the returned eval line. Asked from another
// place's pane, the chat is born in a new tab of its place's workspace and
// focus follows it there; the caller's pane stays as it was.
func (executor *Executor) openHerdr(
	ctx context.Context,
	request Request,
	plan Plan,
	choice spawn.BackendChoice,
) (OpenResult, error) {
	values, err := pfmconfig.ResolvePaths()
	if err != nil {
		return OpenResult{}, err
	}
	if plan.SeatRun == nil {
		return OpenResult{}, errors.New("native picker launch has no structured recipe or identity")
	}
	sessionID := request.Row.ID
	if plan.Record != nil {
		sessionID = plan.Record.SessionID
	} else if sessionID == "" {
		if sessionID, err = newSessionID(); err != nil {
			return OpenResult{}, err
		}
	}
	inPlace := choice.InPlace && !request.Detached
	if inPlace {
		if inPlace, err = spawn.HerdrOpensInPlace(ctx, values, &choice, plan.SeatRun.CWD); err != nil {
			return OpenResult{}, fmt.Errorf("place the open of chat %s: %w", sessionID, err)
		}
	}
	birth := spawn.HerdrRequest{
		Choice: choice,
		Paths:  values,
		Run:    *plan.SeatRun,
		Seat: fleetdb.Seat{
			SessionID: sessionID,
			Engine:    string(compose.EngineForKind(request.Row.Kind)),
			Name:      request.LaunchName,
		},
		Launch: plan.Record,
	}
	if !inPlace {
		birth.Choice.InPlace = false
		born, err := spawn.SpawnHerdr(ctx, birth)
		if err != nil {
			return OpenResult{}, err
		}
		warnings := slices.Concat(plan.Warnings, born.Warnings)
		if request.Detached {
			seat := born.Seat
			seat.Name = cmp.Or(seat.Name, request.Row.Name)
			return OpenResult{
				Name: seat.Name, Socket: seat.HerdrSocket, State: "opened",
				Detail: spawn.HerdrLocation(seat), Warnings: warnings,
			}, nil
		}
		if choice.InPlace {
			// Asked from the user's own pane: focus follows the chat to its place.
			if err := herdr.FocusPane(ctx, born.Seat.HerdrSocket, born.Seat.HerdrPane); err != nil {
				warnings = append(warnings, err.Error())
			}
		}
		launchnotice.Print(executor.stderr, "pfm", warnings)
		return OpenResult{}, nil
	}
	// The chat is born in the caller's own pane: the reservation names this
	// pane and the shell execs the seat host in place, so the pane and its tab
	// stay the user's (no new tab, nothing left to close). Only a name a user
	// chose (launch --name, a resumed chat's own user name) replaces a label
	// the user set on this tab; an AI title, a first prompt or the project
	// folder is re-announced.
	birth.TabName = cmp.Or(request.LaunchName, request.Row.Name)
	birth.UserNamed = request.LaunchName != "" ||
		request.Row.UserName != "" && request.Row.Name == request.Row.UserName
	prepared, err := spawn.PrepareHerdr(ctx, birth)
	if err != nil {
		return OpenResult{}, err
	}
	launchnotice.Print(executor.stderr, "pfm", slices.Concat(plan.Warnings, prepared.Warnings))
	return OpenResult{Line: prepared.Eval}, nil
}

// openTmux creates the planned chat's own tmux server and registers its seat.
func (executor *Executor) openTmux(ctx context.Context, request Request, plan Plan) error {
	executor.recordLaunch(ctx, plan.Record)
	if err := executor.tmux.CreateChatServer(ctx, *plan.ChatServer); err != nil {
		return err
	}
	if plan.Record == nil {
		return nil
	}
	values, err := pfmconfig.ResolvePaths()
	if err != nil {
		return err
	}
	if err := spawn.RecordTmuxSeat(
		ctx,
		values,
		fleetdb.Seat{
			SessionID:  plan.Record.SessionID,
			Engine:     string(plan.Record.Engine),
			Name:       request.LaunchName,
			TmuxSocket: plan.ChatServer.Socket,
		},
	); err != nil {
		fmt.Fprintf(executor.stderr, "pfm: tmux picker chat exists; seat registration failed: %v\n", err)
	}
	return nil
}

func checkCodexLaunch(request Request) error {
	if request.Row.Kind != compose.NewCodex && request.Row.Kind != compose.ResumeCodex {
		return nil
	}
	account, found := request.Config.CodexAccountByID(request.PrimaryAccount)
	if !found {
		return nil
	}
	if err := pfmconfig.CodexLoginError(account.Home); err != nil {
		//nolint:staticcheck // Codex is the proper noun in the required login instruction.
		return fmt.Errorf("Codex account %d: %w", account.ID, err)
	}
	return nil
}

func (executor *Executor) recordLaunch(ctx context.Context, record *fleetdb.Launch) {
	if record == nil {
		return
	}
	values, err := pfmconfig.ResolvePaths()
	if err == nil {
		err = fleetdb.RecordLaunch(ctx, values, *record, clock.Real.Now().Unix())
	}
	if err != nil {
		fmt.Fprintf(executor.stderr, "pfm: record launch %s: %v\n", record.SessionID, err)
	}
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
			cacheValue := "5m"
			if request.Cache1H {
				cacheValue = "1h"
			}
			arguments := []string{
				"chat",
				"reload",
				"--sock",
				request.Row.Socket,
				strconv.Itoa(request.PrimaryAccount),
				"--cache",
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
	currentPath := pfmtmux.SocketPath(currentTMUX)
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
	engines := enginePaneCommands(engineCommands...)
	for _, pane := range panes {
		if engines[pane.CurrentCommand] {
			return pane.WindowIndex
		}
	}
	for _, pane := range panes {
		if isEngineRuntime(pane.CurrentCommand) {
			return pane.WindowIndex
		}
	}
	return panes[0].WindowIndex
}

// enginePaneCommands is the set of pane commands that name an engine: every
// registered engine's binary plus the base name of each configured command.
func enginePaneCommands(engineCommands ...string) map[string]bool {
	engines := make(map[string]bool, len(pfmengine.All())+len(engineCommands))
	for _, id := range pfmengine.All() {
		engines[pfmengine.MustLookup(id).Binary] = true
	}
	for _, command := range engineCommands {
		if base := filepath.Base(command); base != "." && base != "" {
			engines[base] = true
		}
	}
	return engines
}

// isEngineRuntime reports a pane command an engine runs under without naming
// it: node, or a versioned binary such as 2.1.3.
func isEngineRuntime(command string) bool {
	return command == "node" || isNumericVersion(command)
}

// isEngineCommand reports whether a pane's command is an engine, by name or by
// the runtime it runs under.
func isEngineCommand(command string, engines map[string]bool) bool {
	return engines[command] || isEngineRuntime(command)
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
