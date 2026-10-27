package doctor

import (
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
		choice, placeErr := spawn.Place(ctx, "doctor", spawn.BackendAuto, caller, err)
		if placeErr != nil {
			fmt.Fprintf(stdout, "doctor: herdr backend UNRESOLVED — %v\n", placeErr)
			return 0, 1
		}
		backend = choice.Backend
		backendReason = "resolves to tmux here: no proven Herdr caller"
		if backend == spawn.BackendHerdr {
			fmt.Fprintf(
				stdout,
				"doctor: herdr backend CHECKING — chat.backend=auto (%s) resolved to Herdr from the proven caller\n",
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
	resume, err := herdrDoctorRestoreConfig(dependencies.Env)
	switch {
	case err != nil:
		warnings++
		fmt.Fprintf(stdout, "doctor: herdr restore config UNREADABLE — %v\n", err)
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
	if socket := dependencies.Env.Get(herdr.EnvSocket); socket != "" {
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

func herdrDoctorRestoreConfig(env paths.Env) (bool, error) {
	path := env.Get("HERDR_CONFIG_PATH")
	if path == "" {
		directory := env.Get("XDG_CONFIG_HOME")
		if directory == "" {
			home, err := paths.HomeFrom(env)
			if err != nil {
				return false, fmt.Errorf("resolve Herdr config home: %w", err)
			}
			directory = filepath.Join(home, ".config")
		}
		debugPath := filepath.Join(directory, "herdr-dev", "config.toml")
		if _, err := os.Stat(debugPath); err == nil {
			return false, fmt.Errorf(
				"debug Herdr config exists at %s; set HERDR_CONFIG_PATH to identify the server's config",
				debugPath,
			)
		} else if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("inspect debug Herdr config %s: %w", debugPath, err)
		}
		path = filepath.Join(directory, "herdr", "config.toml")
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	var config struct {
		Session struct {
			ResumeAgentsOnRestore *bool `toml:"resume_agents_on_restore"`
		} `toml:"session"`
	}
	if _, err := toml.Decode(strings.TrimPrefix(string(content), "\uFEFF"), &config); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	return config.Session.ResumeAgentsOnRestore == nil || *config.Session.ResumeAgentsOnRestore, nil
}
