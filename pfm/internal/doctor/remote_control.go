package doctor

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// printRuntimeRemoteControlDoctor includes the default Claude home when no
// account roster exists. Account zero uses machine preferences, as Render does.
func printRuntimeRemoteControlDoctor(out io.Writer, runtime config.Runtime, env paths.Env, tally *doctorTally) {
	machine := runtime.Config
	if len(machine.Accounts) == 0 {
		machine.Accounts = []config.Account{{ConfigDir: filepath.Join(runtime.Paths.Home, ".claude")}}
	}
	printRemoteControlDoctor(out, runtime.Paths.ManagedSettingsDir, machine, env, tally)
}

// printRemoteControlDoctor reports intent and detectable blockers, not login or
// connection health. Read each account's settings (following shared-store
// symlinks) and the environment that survives canonical launch hygiene. Claude
// applies settings.env after that cleanup. No credential or complete URL reaches
// the report; absent settings do not suppress surviving launch-env blockers.
func printRemoteControlDoctor(
	stdout io.Writer,
	managedDir string,
	machine config.Config,
	env paths.Env,
	tally *doctorTally,
) {
	hygiene := claudelaunch.Hygiene()
	managed, managedErr := readManagedRemoteControlPolicy(managedDir)
	for _, account := range machine.Accounts {
		settingsPath := filepath.Join(account.ConfigDir, "settings.json")
		settings, err := readRemoteControlSettings(settingsPath)
		if err != nil {
			tally.fail()
			fmt.Fprintf(stdout, "doctor: remote_control claude[%d] FAIL %v\n", account.ID, err)
			continue
		}
		if managedErr != nil {
			tally.fail()
			fmt.Fprintf(stdout, "doctor: remote_control claude[%d] FAIL %v\n", account.ID, managedErr)
			continue
		}
		if !machine.EffectiveClaude(account.ID).RemoteControl {
			fmt.Fprintf(
				stdout,
				"doctor: remote_control claude[%d] off by config (claude.remoteControl=false)\n",
				account.ID,
			)
			continue
		}
		if settings == nil {
			settings = &remoteControlSettings{}
		}
		blockers := managed.blockers()
		if managed.value == nil && settings.DisableRemoteControl != nil && *settings.DisableRemoteControl {
			blockers = append(blockers, "settings disableRemoteControl=true")
		}
		helper, helperSource := settings.APIKeyHelper, "settings"
		if managed.helper != nil {
			helper, helperSource = managed.helper, "managed settings"
		}
		if helper != nil && strings.TrimSpace(*helper) != "" {
			blockers = append(blockers, helperSource+" apiKeyHelper is incompatible with Remote Control")
		}
		accountEnv := map[string]string{}
		launchEnv := map[string]string{}
		for _, name := range remoteControlEnvNames {
			if _, overridden := managed.env[name]; overridden {
				continue
			}
			if value, overridden := settings.Env[name]; overridden {
				accountEnv[name] = value
			} else if !slices.Contains(hygiene, name) {
				launchEnv[name] = env.Get(name)
			}
		}
		blockers = append(blockers, remoteControlEnvBlockers(managed.env, "managed settings env")...)
		blockers = append(blockers, remoteControlEnvBlockers(accountEnv, "settings env")...)
		blockers = append(blockers, remoteControlEnvBlockers(launchEnv, "launch env")...)
		if len(blockers) > 0 {
			tally.warn()
			fmt.Fprintf(
				stdout,
				"doctor: remote_control claude[%d] BLOCKED: %s\n",
				account.ID,
				strings.Join(blockers, "; "),
			)
			continue
		}
		fmt.Fprintf(stdout,
			"doctor: remote_control claude[%d] on (knob on; no blocker seen; login and connection not verified)\n",
			account.ID,
		)
	}
}

type remoteControlSettings struct {
	Env                  map[string]string `json:"env"`
	DisableRemoteControl *bool             `json:"disableRemoteControl"`
	APIKeyHelper         *string           `json:"apiKeyHelper"`
}

func readRemoteControlSettings(path string) (*remoteControlSettings, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		// A dangling shared-store symlink is broken state, not an account
		// that was never set up. Lstat distinguishes it from an absent file.
		if _, statErr := os.Lstat(path); errors.Is(statErr, fs.ErrNotExist) {
			return nil, nil
		} else if statErr != nil {
			return nil, fmt.Errorf("inspect settings %s: %w", path, statErr)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("read settings %s: %w", path, err)
	}
	var settings *remoteControlSettings
	if err := json.Unmarshal(raw, &settings); err != nil {
		// JSON type errors can contain input values; never expose credentials.
		return nil, fmt.Errorf("parse settings %s: invalid settings object", path)
	}
	if settings == nil {
		return nil, fmt.Errorf("parse settings %s: expected settings object", path)
	}
	return settings, nil
}

var remoteControlEnvNames = []string{
	"ANTHROPIC_BASE_URL",
	"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
	"DISABLE_GROWTHBOOK",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"CLAUDE_CODE_OAUTH_TOKEN",
}

func remoteControlEnvBlockers(env map[string]string, source string) []string {
	var blockers []string
	for _, name := range remoteControlEnvNames {
		value := strings.TrimSpace(env[name])
		if value == "" {
			continue
		}
		switch name {
		case "ANTHROPIC_BASE_URL":
			host := remoteControlHost(value)
			if host == "" {
				blockers = append(blockers, source+" "+name+" has an invalid URL host")
			} else if !strings.EqualFold(host, "api.anthropic.com") {
				blockers = append(blockers, source+" "+name+" host="+host+" is not api.anthropic.com")
			}
		case "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "DISABLE_GROWTHBOOK":
			switch strings.ToLower(value) {
			case "1", "true", "yes", "on":
				blockers = append(blockers, source+" "+name+" disables Remote Control discovery")
			}
		default:
			blockers = append(blockers, source+" "+name+" is incompatible with Remote Control")
		}
	}
	return blockers
}

func remoteControlHost(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return ""
	}
	host := parsed.Hostname()
	if net.ParseIP(host) != nil {
		return host
	}
	// Only a plain ASCII host may be displayed, never URL userinfo, paths,
	// queries, percent escapes or terminal controls. Parser errors stay private.
	for _, char := range host {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '-' && char != '.' {
			return ""
		}
	}
	return host
}
