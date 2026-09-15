// Package deps owns the external-command contract for pfm.
package deps

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"unicode"

	pfmengine "hostops/pfm/internal/engine"
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
	Harvest        bool
	SelfDoctorArgs []string
}

// Options materializes the config- and platform-owned registry entries.
type Options struct {
	Home         string
	ClaudeBinary string
	CodexBinary  string
	GOOS         string
	GOARCH       string
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
		Name:        "setsid",
		Purpose:     "detached Linux helper processes",
		Required:    true,
		Platforms:   []string{platformLinux},
		VersionArgs: []string{versionFlag},
		Parse:       firstVersion,
		InstallHint: "install util-linux (setsid)",
	},
	{
		Name:        "nohup",
		Purpose:     "detached helper fallback where setsid is absent",
		Platforms:   []string{platformLinux, platformDarwin},
		InstallHint: "install coreutils (nohup)",
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
		InstallHint: "install Go 1.24 or newer to use pfm update",
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
		Name:        "security",
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

// Registry is the one complete dependency table. Configured engine names and
// provisioned harvestpy paths are materialized here rather than copied into
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
		if id == pfmengine.Codex {
			doctorArgs = append(doctorArgs, "--summary", "--ascii", "--no-color")
		}
		entries = append(entries, Entry{
			Name: descriptor.LongName, Command: binary, Engine: id,
			Purpose:     "configured " + descriptor.Short + " engine",
			VersionArgs: []string{versionFlag}, Parse: firstVersion,
			InstallHint: "install the configured " + descriptor.Short + " CLI", SelfDoctorArgs: doctorArgs,
		})
	}
	harvestRoot := filepath.Join(resolved.Home, ".local", "state", "pfm", "harvest-python")
	current := filepath.Join(harvestRoot, "env", resolved.GOOS+"-"+resolved.GOARCH, "current")
	entries = append(
		entries,
		Entry{
			Name:        "uv",
			Command:     filepath.Join(current, "uv"),
			Purpose:     "provisioned harvestpy package verifier",
			Required:    true,
			Platforms:   []string{platformLinux, platformDarwin},
			VersionArgs: []string{versionFlag},
			Parse:       firstVersion,
			InstallHint: "run pfm install to provision harvestpy",
			Harvest:     true,
		},
		Entry{
			Name:        "harvestpy",
			Command:     filepath.Join(current, "project", ".venv", "bin", "python"),
			Purpose:     "provisioned harvestpy interpreter",
			Required:    true,
			Platforms:   []string{platformLinux, platformDarwin},
			VersionArgs: []string{versionFlag},
			Parse:       firstVersion,
			InstallHint: "run pfm install to provision harvestpy",
			Harvest:     true,
		},
	)
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

// resolveCache memoizes Resolve's successful lookups, keyed by binary name
// and the $PATH they were resolved under. Every tmux capture-pane, every
// spawned codex app-server, and every other exec that runs through
// deps.Executable paid for a fresh Registry() build (a slice allocation plus
// a walk of every fixed and engine-configured entry) and a fresh
// exec.LookPath PATH walk on EVERY call — cheap once, ruinous at the cadence
// a parked picker's Codex idle-identity poll and Codex limits sampler drive
// it (2026-09-08 measurement, devbox). Keying on $PATH rather than name alone
// keeps this invisible to tests that vary PATH per case: a changed PATH is a
// cache miss, not a stale hit.
var (
	resolveCacheMu sync.Mutex
	resolveCache   = map[string]string{}
)

func resolveCacheKey(name string) string {
	return name + "\x00" + os.Getenv("PATH")
}

// resolveCached returns a memoized resolution for name, invalidating it if
// the file it names no longer exists — a binary can move or be uninstalled
// mid-process, and a cache must never outlive the thing it names.
func resolveCached(name string) (string, bool) {
	resolveCacheMu.Lock()
	defer resolveCacheMu.Unlock()
	key := resolveCacheKey(name)
	path, ok := resolveCache[key]
	if !ok {
		return "", false
	}
	if _, err := os.Stat(path); err != nil {
		delete(resolveCache, key)
		return "", false
	}
	return path, true
}

func rememberResolved(name, path string) {
	resolveCacheMu.Lock()
	defer resolveCacheMu.Unlock()
	resolveCache[resolveCacheKey(name)] = path
}

// Resolve is the only production seam that obtains an executable path.
// Config-owned names are permitted even though source-literal names are held
// to the registry by the source guard.
func Resolve(name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("dependency command is empty")
	}
	if cached, ok := resolveCached(name); ok {
		return cached, nil
	}
	command := name
	entries := Registry(Options{Home: ".", GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	for index := range entries {
		entry := entries[index]
		if entry.Name != name && entry.Command != name {
			continue
		}
		if !entry.AppliesTo(runtime.GOOS) {
			return "", fmt.Errorf("dependency %s is not supported on %s", entry.Name, runtime.GOOS)
		}
		command = entry.Command
		break
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return path, err
	}
	rememberResolved(name, path)
	return path, nil
}

// Executable preserves exec.Cmd's normal not-found error while ensuring that
// every successful PATH lookup crosses Resolve.
func Executable(name string) string {
	path, err := Resolve(name)
	if err != nil {
		return name
	}
	return path
}

// DetachLauncher resolves how this host detaches a helper into its own
// session: setsid where it exists (Linux), nohup as the documented POSIX
// fallback (macOS, and any other host without setsid). setsidOverride and
// nohupOverride let a caller pin a specific binary (tests, config); an empty
// override falls back to the bare name for a normal PATH lookup through
// Resolve.
//
// prefixArgs carries setsid's own "-f" flag, prepended ahead of the caller's
// argv; nohup takes no such flag, so prefixArgs is empty on that branch.
// forked reports whether the launcher forks into the background and returns
// immediately (setsid -f) or IS the detached helper itself (nohup) — a
// caller may Run() the former but must Start() and Release() the latter, or
// it blocks until the helper it meant to detach from finishes.
func DetachLauncher(setsidOverride, nohupOverride string) (path string, prefixArgs []string, forked bool, err error) {
	setsid := setsidOverride
	if setsid == "" {
		setsid = "setsid"
	}
	setsidPath, setsidErr := Resolve(setsid)
	if setsidErr == nil {
		return setsidPath, []string{"-f"}, true, nil
	}
	nohup := nohupOverride
	if nohup == "" {
		nohup = "nohup"
	}
	nohupPath, nohupErr := Resolve(nohup)
	if nohupErr != nil {
		return "", nil, false, fmt.Errorf(
			"setsid unavailable (%v) and nohup unavailable (%w)",
			setsidErr,
			nohupErr,
		)
	}
	return nohupPath, nil, false, nil
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
