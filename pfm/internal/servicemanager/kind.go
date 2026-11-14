package servicemanager

import (
	"fmt"
	"strings"
)

// Kind names a per-user service manager. Native is the only place the
// operating system picks one; every consumer selects its per-kind
// implementation from a Kind it is handed, so both kinds run in a Linux test.
type Kind string

const (
	// KindLaunchd is launchd's per-user agents in the gui/<uid> domain (macOS).
	KindLaunchd Kind = "launchd"
	// KindSystemd is systemd's user manager (Linux).
	KindSystemd Kind = "systemd"
)

// The service managers' binaries, named once for every consumer's LookPath
// and argv.
const (
	LaunchctlBinary  = "launchctl"
	SystemctlBinary  = "systemctl"
	SystemdRunBinary = "systemd-run"
)

// Native is this host's per-user service manager.
func Native() Kind { return native }

// invocationIDEnv is systemd's per-invocation marker. Descendants inherit it,
// so its presence means the caller runs inside a user service's cgroup.
const invocationIDEnv = "INVOCATION_ID"

// ScopeArgv returns argv wrapped to escape the systemd user service that
// happened to start the caller: `systemd-run --user --collect --scope -- argv…`
// when kind is systemd and getenv reports INVOCATION_ID, so a long-lived child
// (a chat server) outlives the service. Outside a service, or under launchd,
// argv comes back unchanged. lookPath resolves systemd-run; a scope that
// cannot be made is an error, never a silent mortal launch.
func ScopeArgv(
	kind Kind,
	getenv func(string) string,
	lookPath func(string) (string, error),
	argv []string,
) ([]string, error) {
	if kind != KindSystemd || strings.TrimSpace(getenv(invocationIDEnv)) == "" {
		return argv, nil
	}
	systemdRun, err := lookPath(SystemdRunBinary)
	if err != nil {
		return nil, fmt.Errorf(
			"running inside a systemd user service requires systemd-run to create a durable chat scope: %w",
			err,
		)
	}
	return append([]string{systemdRun, "--user", "--collect", "--scope", "--"}, argv...), nil
}

// WithoutInvocationID is environment minus systemd's invocation marker: a
// scoped child is no longer the requesting service, and must not classify its
// own nested spawns as service-owned.
func WithoutInvocationID(environment []string) []string {
	prefix := invocationIDEnv + "="
	filtered := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
