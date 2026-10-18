package installer

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/agentlauncher"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// agentLauncherMarker is where a launch agent template takes the stable
// launcher as ProgramArguments[0] (agentlauncher: why it is never pfm itself).
const (
	agentLauncherMarker = "__PFM_AGENT_LAUNCHER__"
	pfmHomeMarker       = "__PFM_HOME__"
)

// renderLaunchAgentTemplate fills the home and launcher markers every launch
// agent template carries; the schedule and name-sync renderers add their own on top.
func renderLaunchAgentTemplate(template []byte, home string) ([]byte, error) {
	launcher, err := replaceSingleAssetMarker(string(template), agentLauncherMarker, agentlauncher.Program(home))
	if err != nil {
		return nil, err
	}
	return []byte(strings.ReplaceAll(launcher, pfmHomeMarker, home)), nil
}

// ensureAgentLauncher stages the launcher every launch agent runs, once per
// pass and before any agent is written, so no agent is ever loaded pointing at
// a launcher that is not there. A launcher whose bytes and mode already match
// is reported ok and never rewritten: a rewrite is a new file for macOS to
// register.
func (installer *engine) ensureAgentLauncher() error {
	if installer.agentLauncherStaged {
		return nil
	}
	home := installer.options.Home
	path := agentlauncher.Program(home)
	current, err := agentlauncher.UpToDate(home)
	if err != nil {
		return err
	}
	if current {
		installer.ok(path)
	} else if err := installer.change("write "+path, func() error { return agentlauncher.Stage(home) }); err != nil {
		return err
	}
	installer.agentLauncherStaged = true
	return nil
}

// unwireAgentLauncher removes the launcher once every agent that ran it is
// gone; an uninstall calls it after the last unwire.
func (installer *engine) unwireAgentLauncher() error {
	home := installer.options.Home
	path := agentlauncher.Program(home)
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat agent launcher %s: %w", path, err)
	}
	return installer.change("remove "+path, func() error { return agentlauncher.Remove(home) })
}

// pfmLaunchAgentLabels are the launch agents pfm writes on macOS; doctor holds
// every one that exists to the launcher.
var pfmLaunchAgentLabels = []string{mcpLaunchdLabel, launchdLabel, scheduleLaunchdLabel, themesync.FollowerLabel}

// LauncherFinding is one launch-agent file that does not run through the
// stable launcher, or could not be read to tell.
type LauncherFinding struct {
	Path   string
	Detail string
	// Fix is the command that repairs it: install rewrites every agent but
	// the theme follower's, which only `pfm theme` owns once the sync is off.
	Fix string
}

// installFix repairs every launcher finding install's own wiring reaches.
const installFix = "run pfm install --yes"

// launcherFix names the repair for one agent's plist: the follower's while
// the theme sync is off is out of install's reach (install refreshes it only
// while the sync is on), so `pfm theme off` removes it, or with no sync state
// at all nothing pfm runs owns it any more.
func launcherFix(home, label, path string) string {
	if label != themesync.FollowerLabel {
		return installFix
	}
	stateDir := themesync.DefaultStateDir(home)
	if themesync.SyncOn(stateDir) {
		return installFix
	}
	if _, err := os.Lstat(filepath.Join(stateDir, themesync.StateFileName)); err == nil {
		return "run pfm theme off"
	}
	return "run launchctl bootout gui/$(id -u)/" + label + ", then remove " + path
}

// LaunchAgentLauncherDrift lists every pfm launch agent under home whose
// ProgramArguments[0] is not the stable launcher, and the launcher itself when
// agents exist and it is missing or not what install writes. agents counts the
// plists examined; a plist that cannot be parsed is a finding, never skipped.
func LaunchAgentLauncherDrift(home string) (agents int, findings []LauncherFinding) {
	want := agentlauncher.Program(home)
	for _, label := range pfmLaunchAgentLabels {
		path := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		fix := launcherFix(home, label, path)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		agents++
		if err == nil && !info.Mode().IsRegular() {
			findings = append(findings, LauncherFinding{path, "not a regular file: " + info.Mode().String(), fix})
			continue
		}
		var content []byte
		if err == nil {
			content, err = os.ReadFile(path)
		}
		if err != nil {
			findings = append(findings, LauncherFinding{path, "unreadable: " + err.Error(), fix})
			continue
		}
		program, err := plistProgram(content)
		switch {
		case err != nil:
			findings = append(findings, LauncherFinding{path, "ProgramArguments unreadable: " + err.Error(), fix})
		case program != want:
			findings = append(findings, LauncherFinding{path, "runs " + program + ", not the launcher " + want, fix})
		}
	}
	if agents == 0 {
		return 0, nil
	}
	if current, err := agentlauncher.UpToDate(home); err != nil {
		findings = append(findings, LauncherFinding{want, err.Error(), installFix})
	} else if !current {
		findings = append(
			findings,
			LauncherFinding{want, "launcher is missing or differs from what install writes", installFix},
		)
	}
	return agents, findings
}

// plistProgram returns ProgramArguments[0] of an XML property list.
func plistProgram(content []byte) (string, error) {
	const key = "<key>ProgramArguments</key>"
	_, after, found := bytes.Cut(content, []byte(key))
	if !found {
		return "", errors.New("no ProgramArguments key")
	}
	_, after, found = bytes.Cut(after, []byte("<array>"))
	if !found {
		return "", errors.New("ProgramArguments is not an array")
	}
	_, after, found = bytes.Cut(after, []byte("<string>"))
	if !found {
		return "", errors.New("ProgramArguments is empty")
	}
	value, _, found := bytes.Cut(after, []byte("</string>"))
	if !found {
		return "", errors.New("ProgramArguments[0] is unterminated")
	}
	return unescapePlistText(string(value)), nil
}

var plistText = strings.NewReplacer(
	"&lt;", "<", "&gt;", ">", "&quot;", `"`, "&apos;", "'", "&#39;", "'", "&amp;", "&",
)

func unescapePlistText(text string) string {
	return plistText.Replace(text)
}
