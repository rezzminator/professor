package deps

import (
	"fmt"
	"runtime"
	"strings"
	"unicode"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

const (
	versionFlag    = "--version"
	platformDarwin = "darwin"
	platformLinux  = "linux"
)

// Entry describes one external command pfm may execute. Command is the
// configured name or absolute path; Name is the stable doctor-row key.
type Entry struct {
	Name           string
	Command        string
	Engine         pfmengine.ID
	Purpose        string
	Required       bool
	Platforms      []string
	VersionArgs    []string
	MinVersion     string
	Parse          func(string) (string, error)
	InstallHint    string
	SelfDoctorArgs []string
	// ProbeHome runs the version and self-doctor probes in a throwaway engine
	// home (NewEngineProbeHome), never the ambient account.
	ProbeHome bool
	// AccountChecks names the self-doctor rows that judge the account, not the
	// binary. A throwaway home has no login, so they fail there by
	// construction; pfm judges the account in its own rows (codex-login), and
	// a self-doctor failing only these rows is a healthy binary.
	AccountChecks []string
}

// Options materializes the config- and platform-owned registry entries.
type Options struct {
	Home           string
	ClaudeBinary   string
	CodexBinary    string
	OpenCodeBinary string
	GOOS           string
	GOARCH         string
}

var fixedCommands = []Entry{
	// tmux 1.8 introduced wait-for, the newest primitive used by the managed
	// Claude launcher. Reference: upstream CHANGES, "CHANGES FROM 1.7 TO 1.8":
	// https://github.com/tmux/tmux/blob/master/CHANGES
	{
		Name:        "tmux",
		Purpose:     "fleet panes and chat transport",
		Required:    true,
		VersionArgs: []string{"-V"},
		MinVersion:  "1.8",
		Parse:       prefixedVersion("tmux"),
		InstallHint: "install tmux 1.8 or newer",
	},
	{
		Name:        "git",
		Purpose:     "repository inspection and updates",
		Required:    true,
		VersionArgs: []string{versionFlag},
		Parse:       prefixedVersion("git version"),
		InstallHint: "install git",
	},
	// python3 is checked, never provisioned: pfm install does not install it. The
	// flights:tracer's tracetree kit is Python 3 standard library only and states no
	// minimum version, so none is pinned here.
	{
		Name:        "python3",
		Purpose:     "flights:tracer's tracetree kit",
		Required:    true,
		Platforms:   []string{platformLinux, platformDarwin},
		VersionArgs: []string{versionFlag},
		Parse:       prefixedVersion("Python"),
		InstallHint: "install python3; flights:tracer's tracetree kit runs on it",
	},
	{Name: "sh", Purpose: "portable shell command execution", Required: true, InstallHint: "install a POSIX shell"},
	{
		Name:        "bash",
		Purpose:     "installed compatibility scripts",
		Required:    true,
		VersionArgs: []string{versionFlag},
		Parse:       firstVersion,
		InstallHint: "install bash",
	},
	{
		Name:        "zsh",
		Purpose:     "interactive generated action execution",
		Required:    true,
		VersionArgs: []string{versionFlag},
		Parse:       firstVersion,
		InstallHint: "install zsh",
	},
	{
		Name:        "dscl",
		Purpose:     "account login shell discovery for Herdr installation",
		Platforms:   []string{platformDarwin},
		InstallHint: "restore the system Directory Services command",
	},
	{
		Name:        "getent",
		Purpose:     "account login shell discovery for Herdr installation",
		Platforms:   []string{platformLinux},
		InstallHint: "install getent (libc-bin or the platform equivalent)",
	},
	{
		Name:        "ps",
		Purpose:     "Darwin process-table inspection",
		Required:    true,
		Platforms:   []string{platformDarwin},
		InstallHint: "restore the system ps command",
	},
	{
		Name:        "lsof",
		Purpose:     "Darwin open-file inspection",
		Required:    true,
		Platforms:   []string{platformDarwin},
		VersionArgs: []string{"-v"},
		Parse:       lsofVersion,
		InstallHint: "install lsof",
	},
	{
		Name:        "script",
		Purpose:     "terminal-backed command execution",
		InstallHint: "install util-linux or the BSD script command",
	},
	{
		Name:        "sleep",
		Purpose:     "bounded shell-side polling",
		Required:    true,
		InstallHint: "restore the system sleep command",
	},
	{
		Name:        "go",
		Purpose:     "building a staged pfm update",
		VersionArgs: []string{"version"},
		Parse:       firstVersion,
		InstallHint: "install Go 1.27.1 or newer to use pfm update",
	},
	{
		Name:        "systemctl",
		Purpose:     "Linux user-service wiring",
		Platforms:   []string{platformLinux},
		VersionArgs: []string{versionFlag},
		Parse:       firstVersion,
		InstallHint: "install systemd to enable user units",
	},
	{
		Name:        "systemd-run",
		Purpose:     "durable chat scopes spawned from Linux user services",
		Platforms:   []string{platformLinux},
		VersionArgs: []string{versionFlag},
		Parse:       firstVersion,
		InstallHint: "install systemd to spawn chats from the MCP service",
	},
	{
		Name:        "vm_stat",
		Purpose:     "Darwin host memory sampling",
		Platforms:   []string{platformDarwin},
		InstallHint: "restore the system vm_stat command",
	},
	{
		Name:        "defaults",
		Purpose:     "Darwin appearance detection for theme following",
		Platforms:   []string{platformDarwin},
		InstallHint: "restore the system defaults command",
	},
	{
		Name:        "sudo",
		Purpose:     "removing root-owned managed files during install cleanup",
		InstallHint: "install sudo",
	},
	{
		Name:        "uv",
		Purpose:     "provisioning rumdl during pfm install",
		InstallHint: "install uv (https://docs.astral.sh/uv/)",
	},
	{
		Name:        "herdr",
		Purpose:     "attaching to Herdr chat panes",
		InstallHint: "install Herdr to attach Herdr chats",
	},
	{
		Name:        "launchctl",
		Purpose:     "Darwin launch-agent wiring",
		Required:    true,
		Platforms:   []string{platformDarwin},
		InstallHint: "restore the system launchctl command",
	},
	// Absolute path: this is the door to the login keychain, where Claude Code
	// keeps every account's OAuth credential on macOS, so it must never
	// resolve to something a $PATH entry shadowed.
	{
		Name:        KeychainCommand,
		Command:     "/usr/bin/security",
		Purpose:     "Darwin login-keychain OAuth credential reads",
		Required:    true,
		Platforms:   []string{platformDarwin},
		InstallHint: "restore the system security command",
	},
	// MinVersion 0.2.73 is the release the shipped .rumdl.toml policy (MD060
	// compact style, per-file-ignores) was validated against.
	{
		Name:        "rumdl",
		Purpose:     "markdown lint and format for prompts and docs",
		Required:    false,
		Platforms:   []string{platformLinux, platformDarwin},
		VersionArgs: []string{versionFlag},
		Parse:       prefixedVersion("rumdl"),
		MinVersion:  "0.2.73",
		InstallHint: "run pfm install to provision rumdl, or: uv tool install rumdl",
	},
}

// KeychainCommand names the registry row of the macOS keychain's command: usagehook's credential
// read resolves it, and testjail serves it as an empty keychain in an armed unit binary.
const KeychainCommand = "security"

// Registry is the one complete dependency table. Configured engine names and
// configured engine paths are materialized here rather than copied into
// doctor or install.
func Registry(options ...Options) []Entry {
	var resolved Options
	if len(options) != 0 {
		resolved = options[0]
	}
	if resolved.Home == "" {
		resolved.Home = "."
	}
	if resolved.GOOS == "" {
		resolved.GOOS, resolved.GOARCH = runtime.GOOS, runtime.GOARCH
	}
	entries := append([]Entry(nil), fixedCommands...)
	engineBinaries := map[pfmengine.ID]string{
		pfmengine.Claude: strings.TrimSpace(resolved.ClaudeBinary),
		pfmengine.Codex:  strings.TrimSpace(resolved.CodexBinary),
	}
	for _, id := range []pfmengine.ID{pfmengine.Claude, pfmengine.Codex} {
		descriptor := pfmengine.MustLookup(id)
		binary := engineBinaries[id]
		if binary == "" {
			binary = descriptor.Binary
		}
		doctorArgs := []string{"doctor"}
		var accountChecks []string
		if id == pfmengine.Codex {
			doctorArgs = append(doctorArgs, "--summary", "--ascii", "--no-color")
			accountChecks = []string{"auth"}
		}
		entries = append(entries, Entry{
			Name: descriptor.LongName, Command: binary, Engine: id,
			Purpose:     "configured " + descriptor.Short + " engine",
			VersionArgs: []string{versionFlag}, Parse: firstVersion,
			InstallHint: "install the configured " + descriptor.Short + " CLI", SelfDoctorArgs: doctorArgs,
			// The probes judge the binary, never an account: every engine
			// run writes its home, so each runs in a throwaway one.
			ProbeHome: true, AccountChecks: accountChecks,
		})
	}
	// OpenCode has no self-doctor command, so its row probes the version only,
	// and outside a throwaway home: --version writes no engine state.
	openCode := pfmengine.MustLookup(pfmengine.OpenCode)
	openCodeBinary := strings.TrimSpace(resolved.OpenCodeBinary)
	if openCodeBinary == "" {
		openCodeBinary = openCode.Binary
	}
	entries = append(entries, Entry{
		Name: openCode.LongName, Command: openCodeBinary, Engine: pfmengine.OpenCode,
		Purpose:     "configured " + openCode.Short + " engine",
		VersionArgs: []string{versionFlag}, Parse: firstVersion,
		InstallHint: "install the configured " + openCode.Short + " CLI",
	})
	for index := range entries {
		if entries[index].Command == "" {
			entries[index].Command = entries[index].Name
		}
	}
	return entries
}

// Registered reports whether name is a fixed literal or stable registry key.
func Registered(name string) bool {
	entries := Registry(Options{Home: ".", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	for index := range entries {
		entry := entries[index]
		if entry.Name == name || entry.Command == name {
			return true
		}
	}
	return false
}

// AppliesTo reports whether the entry belongs to goos.
func (entry Entry) AppliesTo(goos string) bool {
	if len(entry.Platforms) == 0 {
		return true
	}
	for _, platform := range entry.Platforms {
		if platform == goos {
			return true
		}
	}
	return false
}

func prefixedVersion(prefix string) func(string) (string, error) {
	return func(output string) (string, error) {
		line := FirstLine(output)
		if !strings.HasPrefix(line, prefix) {
			return "", fmt.Errorf("expected %q prefix", prefix)
		}
		return firstVersion(strings.TrimSpace(strings.TrimPrefix(line, prefix)))
	}
}

func firstVersion(output string) (string, error) {
	line := FirstLine(output)
	for _, field := range strings.Fields(line) {
		// Strip any leading letter run — not only "v"/"V" — so a field like
		// "go1.24.13" (real `go version` output) reaches its digit run the
		// same way "v1.8" or "0.148.0" already did.
		trimmed := strings.TrimLeftFunc(field, unicode.IsLetter)
		if trimmed != "" && trimmed[0] >= '0' && trimmed[0] <= '9' {
			return strings.TrimRight(trimmed, ",;"), nil
		}
	}
	return "", fmt.Errorf("no version in %q", line)
}

func lsofVersion(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		label, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if !found || !strings.EqualFold(strings.TrimSpace(label), "revision") {
			continue
		}
		return firstVersion(strings.TrimSpace(value))
	}
	return "", fmt.Errorf("no revision in %q", FirstLine(output))
}

// FirstLine bounds arbitrary command output for a visible doctor row.
func FirstLine(output string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(output), "\n")
	return strings.TrimSpace(line)
}
