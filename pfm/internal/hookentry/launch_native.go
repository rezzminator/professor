package hookentry

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/action"
	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/launchnotice"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/spawn"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

// launcherSeatAccount picks the fleet primary unless the shell explicitly chose
// a different account; the login default is not an operator choice.
func launcherSeatAccount(runtime config.Runtime, primary int, env paths.Env) (string, int) {
	configDir := config.AmbientClaudeConfigDirFrom(env)
	if claudelaunch.InheritedConfigDir(env.Get) {
		configDir = ""
	}
	if configDir == "" {
		if account, found := runtime.Config.AccountByID(primary); found {
			configDir = account.ConfigDir
		}
	}
	accountID := primary
	for _, account := range runtime.Config.Accounts {
		if configDir != "" && filepath.Clean(account.ConfigDir) == filepath.Clean(configDir) {
			accountID = account.ID
			break
		}
	}
	return configDir, accountID
}

func launchBackend(ctx context.Context, requested string, env paths.Env) (spawn.BackendChoice, error) {
	// Read the supplied environment seam, not the host's inherited Herdr keys.
	environment := []string{"TMUX=" + env.Get("TMUX")}
	for _, key := range herdr.Env() {
		environment = append(environment, key+"="+env.Get(key))
	}
	return spawn.ChooseBackend(ctx, requested, environment, nil)
}

func launchNative(
	choice spawn.BackendChoice,
	request claudelaunch.Request,
	cwd string,
	_, stderr io.Writer,
	runtime config.Runtime,
) int {
	accountID := request.Account
	prefs := runtime.Config.EffectiveClaude(accountID)
	machine := config.Config{Claude: prefs, MCPServers: runtime.Config.MCPServers, MCP: runtime.Config.MCP}
	request.Account = 0 // ConfigDir already came from launcherSeatAccount.
	launch, err := claudelaunch.Render(request, machine)
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: build native Claude command: %v\n", err)
		return 1
	}
	if warning := claudelaunch.PromptWarning(
		request,
		machine,
	); warning != "" &&
		launchnotice.HoldCancellable(stderr, "pfm internal launch", []string{warning}) {
		return 130
	}
	identity, _, _ := action.LauncherIdentity(request.Args)
	if launch.SessionID != "" {
		identity = launch.SessionID
	}
	id, err := claudelaunch.NewSessionID()
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: new seat id: %v\n", err)
		return 1
	}
	environment := nativeLaunchEnvironment(launch)
	// The supervisor, not a birth-door process, applies the saved ledger policy.
	environment = append(environment, paths.EnvStateDB+"="+runtime.Paths.StateDB)
	ctx := context.Background()
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	if owner, found, err := db.SeatByPane(ctx, choice.HerdrSocket, choice.HerdrPane); err != nil || found {
		fmt.Fprintf(
			stderr,
			"pfm internal launch: pane %s already owned by seat %s or unavailable: %v\n",
			choice.HerdrPane,
			owner.SeatID,
			err,
		)
		if err := db.Close(); err != nil {
			fmt.Fprintf(stderr, "pfm internal launch: close occupied seat state: %v\n", err)
		}
		return 1
	}
	seat := fleetdb.Seat{
		SeatID:         id,
		SessionID:      identity,
		Engine:         string(pfmengine.Claude),
		Backend:        spawn.BackendHerdr,
		HerdrSocket:    choice.HerdrSocket,
		HerdrPane:      choice.HerdrPane,
		HerdrWorkspace: choice.HerdrWorkspace,
		Name:           request.Name,
		State:          nativeSeatLive,
		UpdatedAt:      clock.Real.Now().Unix(),
	}
	run := fleetdb.SeatRun{
		Binary:     launch.Binary,
		Args:       launch.Argv,
		Env:        environment,
		CWD:        cwd,
		CostLedger: prefs.CostLedger,
	}
	if err := db.WriteSeatRun(id, run); err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: %v\n", err)
		_ = db.Close()
		return 1
	}
	if err := db.PutSeat(ctx, seat); err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: persist native seat: %v\n", err)
		if cleanupErr := db.DeleteSeatRun(id); cleanupErr != nil {
			fmt.Fprintf(stderr, "pfm internal launch: remove unbound recipe: %v\n", cleanupErr)
		}
		_ = db.Close()
		return 1
	}
	if err := db.Close(); err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: close native seat state: %v\n", err)
		return 1
	}
	if identity != "" {
		if err := fleetdb.RecordLaunch(
			ctx,
			runtime.Paths,
			fleetdb.Launch{SessionID: identity, Engine: pfmengine.Claude, Account: accountID, Cache1H: launch.Cache1H},
			clock.Real.Now().Unix(),
		); err != nil {
			fmt.Fprintf(stderr, "pfm: record launch %s: %v\n", identity, err)
		}
	}
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: resolve running executable: %v\n", err)
		return 1
	}
	argv := []string{os.Args[0]}
	if runtime.Config.Path != "" {
		argv = append(argv, "--config", runtime.Config.Path)
	}
	argv = append(argv, "internal", "seat-host", id)
	if err := LaunchExec(executable, argv, os.Environ()); err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: exec native supervisor: %v\n", err)
		return 1
	}
	return 0
}

func nativeLaunchEnvironment(launch claudelaunch.Launch) []string {
	environment := seatEnvironment(os.Environ(), nil, launch.Unset)
	// Unset names and assigned names can overlap: assignments always win.
	keys := make([]string, 0, len(launch.Env))
	for _, entry := range launch.Env {
		key, _, _ := strings.Cut(entry, "=")
		keys = append(keys, key)
	}
	return seatEnvironment(environment, launch.Env, keys)
}

func recordLauncherTmuxSeat(ctx context.Context, runtime config.Runtime, socket, identity, name string) error {
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	defer func() { _ = db.Close() }()
	return db.PutSeat(
		ctx,
		fleetdb.Seat{
			SeatID:     socket,
			SessionID:  identity,
			Engine:     string(pfmengine.Claude),
			Backend:    spawn.BackendTmux,
			TmuxSocket: filepath.Join(runtime.Paths.TmuxDir, socket),
			TmuxPane:   "%0",
			Name:       name,
			State:      nativeSeatLive,
			UpdatedAt:  clock.Real.Now().Unix(),
		},
	)
}

// attachArguments releases the gated pane and attaches this terminal to the
// seat. Nested inside a tmux pfm does not own, the outer pane already keeps the
// chat alive, so the seat also takes destroy-unattached: killing that pane or
// its server ends the chat instead of leaving a detached seat holding a Claude
// process nobody can see. The option is set after attach-session, so the seat
// is never unattached while it is on.
func attachArguments(socketPath, startChannel, session string, nested bool) []string {
	arguments := []string{
		pfmtmux.Binary,
		"-S",
		socketPath,
		"wait-for",
		"-S",
		startChannel,
		";",
		"attach-session",
		"-t",
		session,
	}
	if nested {
		arguments = append(arguments, ";", "set-option", "-t", session, "destroy-unattached", "on")
	}
	return arguments
}

func cleanupLauncherTmuxSeat(ctx context.Context, runtime config.Runtime, socket string, stderr io.Writer) {
	db := fleetdb.OpenSharedState(ctx, runtime.Paths)
	if err := db.DeleteSeat(ctx, socket); err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: remove ended tmux seat: %v\n", err)
	}
	if err := db.Close(); err != nil {
		fmt.Fprintf(stderr, "pfm internal launch: close ended tmux seat state: %v\n", err)
	}
}
