package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/rezzminator/professor/pfm/internal/chat"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// printSelectedHerdrBackendDoctor explains the configured/environment candidate
// and checks Herdr when selected. Doctor has no requester-seat override.
func printSelectedHerdrBackendDoctor(
	ctx context.Context,
	stdout io.Writer,
	dependencies Dependencies,
	cfg pfmconfig.Config,
	lifetimeUnverified bool,
) (warnings, failures int) {
	backend := cfg.Chat.Backend
	backendReason := "selects " + backend
	if backend == spawn.BackendAuto {
		environment := resolve.CallerEnvironmentFrom(dependencies.Env)
		identifier, err := resolve.NewWhoami(
			resolve.WhoamiDependencies{Environment: &environment, CodexSeats: chat.CodexSeats(nil)},
		)
		var caller resolve.Identity
		if err == nil {
			caller, err = identifier.Identify(ctx)
		}
		choice, placeErr := spawn.Place(ctx, "doctor", spawn.BackendAuto, "", false, caller, err)
		if placeErr != nil {
			fmt.Fprintf(stdout, "doctor: herdr backend UNRESOLVED — %v\n", placeErr)
			return 0, 1
		}
		backend = choice.Backend
		backendReason = "resolves to tmux here: no proven Herdr caller"
		if backend == spawn.BackendHerdr {
			fmt.Fprintf(
				stdout,
				"doctor: herdr backend CHECKING — chat.backend=auto (%s) resolved to Herdr from the proven caller; "+
					"a new chat is born in its place's workspace\n",
				cfg.Source("chat.backend"),
			)
		}
	}

	if backend == spawn.BackendHerdr {
		return printHerdrBackendDoctor(ctx, stdout, dependencies, lifetimeUnverified)
	}
	fmt.Fprintf(stdout, "doctor: herdr backend NOT CHECKED — chat.backend=%s (%s) %s\n",
		cfg.Chat.Backend, cfg.Source("chat.backend"), backendReason)
	return 0, 0
}

// printHerdrBackendDoctor checks a required Herdr backend; it never changes it.
// The config row describes disk, not the running server's last loaded settings.
func printHerdrBackendDoctor(
	ctx context.Context,
	stdout io.Writer,
	dependencies Dependencies,
	lifetimeUnverified bool,
) (warnings, failures int) {
	unreachable := func(reason string) {
		failures++
		fmt.Fprintf(stdout, "doctor: herdr backend UNREACHABLE — %s\n", reason)
		fmt.Fprintln(
			stdout,
			"doctor: herdr backend repair — install or start Herdr, then run herdr status --json; change chat.backend if Herdr is not required",
		)
	}
	socket, err := herdrDoctorSocket(ctx, dependencies)
	if err != nil {
		unreachable(err.Error())
		return warnings, failures
	}
	pong, err := herdr.ProbeProtocol(ctx, socket)
	var mismatch *herdr.ProtocolMismatch
	switch {
	case errors.As(err, &mismatch):
		failures++
		fmt.Fprintf(stdout, "doctor: %v\n", mismatch)
	case err != nil:
		unreachable(err.Error())
		return warnings, failures
	default:
		fmt.Fprintf(stdout, "doctor: herdr backend ok (protocol %d, %s)\n", pong.Protocol, socket)
	}
	if lifetimeUnverified {
		warnings++
		fmt.Fprintf(
			stdout,
			"doctor: herdr macOS lifetime fix (#5236) UNVERIFIED in this Herdr build (%s) — no supported fixed release established\n",
			pong.Version,
		)
	}
	resume, shell, err := herdrDoctorRestoreConfig(dependencies.Env)
	switch {
	case err != nil:
		warnings++
		fmt.Fprintf(stdout, "doctor: herdr restore config UNREADABLE — %v\n", err)
	case !resume && filepath.Base(shell) == herdrKeepBinary:
		// herdr-keep requires native resume off and keeps panes alive itself, so its own doctor is the verdict.
		if failure := herdrKeepDoctor(ctx, dependencies, shell); failure != "" {
			warnings++
			fmt.Fprintf(stdout, "doctor: herdr resume_agents_on_restore=false and herdr-keep doctor FAILED — %s;"+
				" Herdr chats will not come back after a restart until it passes\n", failure)
		} else {
			fmt.Fprintln(
				stdout,
				"doctor: herdr resume_agents_on_restore=false by design: herdr-keep keeps panes alive (herdr-keep doctor OK)",
			)
		}
	case !resume:
		warnings++
		fmt.Fprintln(
			stdout,
			"doctor: herdr resume_agents_on_restore=false — Herdr chats will not come back after a restart",
		)
	default:
		fmt.Fprintln(stdout, "doctor: herdr resume_agents_on_restore=true (on-disk config)")
	}
	return warnings, failures
}

func herdrDoctorSocket(ctx context.Context, dependencies Dependencies) (string, error) {
	if socket := resolve.CallerEnvironmentFrom(dependencies.Env).HerdrSocket; socket != "" {
		return socket, nil
	}
	var lookupError error
	binary, ok := resolveHerdrBinary(dependencies.Runner, dependencies, func(checked, detail, _ string) {
		lookupError = fmt.Errorf("%s: %s", checked, detail)
	})
	if !ok {
		return "", lookupError
	}
	if binary == "" {
		return "", errors.New("herdr not installed but chat.backend=herdr requires it")
	}
	bounded, cancel := context.WithTimeout(ctx, herdrStatusBound)
	defer cancel()
	result, err := dependencies.Runner.Run(bounded, []string{binary, "status", "--json"}, deps.RunOptions{
		WaitDelay: 2 * time.Second, ProcessGroup: true,
	})
	switch {
	case bounded.Err() != nil:
		return "", fmt.Errorf("herdr status --json: %w", bounded.Err())
	case err != nil:
		return "", fmt.Errorf("herdr status --json: %w", err)
	case result.ExitCode != 0:
		return "", fmt.Errorf("herdr status --json: exit %d: %s", result.ExitCode, capturedLine(result.Stderr, "empty"))
	}
	var status struct {
		Server *struct {
			Running *bool  `json:"running"`
			Socket  string `json:"socket"`
		} `json:"server"`
	}
	if err := json.Unmarshal(result.Stdout, &status); err != nil {
		return "", fmt.Errorf("herdr status --json: malformed status: %w", err)
	}
	if status.Server == nil || status.Server.Running == nil || !*status.Server.Running ||
		strings.TrimSpace(status.Server.Socket) == "" {
		return "", fmt.Errorf("herdr status --json: no running server endpoint reported")
	}
	return status.Server.Socket, nil
}

// herdrDoctorRestoreConfig reads the on-disk Herdr config: whether native
// agent resume is on, and terminal.default_shell ("" when unset).
func herdrDoctorRestoreConfig(env paths.Env) (resume bool, shell string, err error) {
	path := env.Get("HERDR_CONFIG_PATH")
	if path == "" {
		directory := env.Get("XDG_CONFIG_HOME")
		if directory == "" {
			home, err := paths.HomeFrom(env)
			if err != nil {
				return false, "", fmt.Errorf("resolve Herdr config home: %w", err)
			}
			directory = filepath.Join(home, ".config")
		}
		debugPath := filepath.Join(directory, "herdr-dev", "config.toml")
		if _, err := os.Stat(debugPath); err == nil {
			return false, "", fmt.Errorf(
				"debug Herdr config exists at %s; set HERDR_CONFIG_PATH to identify the server's config",
				debugPath,
			)
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, "", fmt.Errorf("inspect debug Herdr config %s: %w", debugPath, err)
		}
		path = filepath.Join(directory, "herdr", "config.toml")
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("read %s: %w", path, err)
	}
	var config struct {
		Session struct {
			ResumeAgentsOnRestore *bool `toml:"resume_agents_on_restore"`
		} `toml:"session"`
		Terminal struct {
			DefaultShell string `toml:"default_shell"`
		} `toml:"terminal"`
	}
	if _, err := toml.Decode(strings.TrimPrefix(string(content), "\uFEFF"), &config); err != nil {
		return false, "", fmt.Errorf("parse %s: %w", path, err)
	}
	resume = config.Session.ResumeAgentsOnRestore == nil || *config.Session.ResumeAgentsOnRestore
	return resume, config.Terminal.DefaultShell, nil
}

// herdrKeepBinary is the herdr-keep plugin's program, Herdr's default_shell when it guards panes.
const herdrKeepBinary = "herdr-keep"

// herdrKeepDoctor runs `{shell} doctor` and returns "" when it reports OK, else
// the first line of why it did not: its own refusal, a timeout or a failed run.
func herdrKeepDoctor(ctx context.Context, dependencies Dependencies, shell string) string {
	bounded, cancel := context.WithTimeout(ctx, herdrStatusBound)
	defer cancel()
	// A doctor run from inside a held pane inherits the keeper's marks, which
	// herdr-keep refuses as a recursive invocation: never a verdict on the keeper.
	result, err := dependencies.Runner.Run(bounded, []string{shell, "doctor"}, deps.RunOptions{
		Env: herdr.KeepOuterEnviron(os.Environ()), WaitDelay: 2 * time.Second, ProcessGroup: true,
	})
	switch {
	case bounded.Err() != nil:
		return fmt.Sprintf("%s doctor: %v", shell, bounded.Err())
	case err != nil:
		return fmt.Sprintf("%s doctor: %v", shell, err)
	case result.ExitCode != 0 || !bytes.HasPrefix(result.Stdout, []byte("OK:")):
		return capturedLine(
			append(result.Stderr, result.Stdout...),
			fmt.Sprintf("%s doctor: exit %d", shell, result.ExitCode),
		)
	}
	return ""
}
