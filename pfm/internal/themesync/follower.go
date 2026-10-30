package themesync

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/agentlauncher"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	launchctlBinary  = "launchctl"
	followerLabel    = "com.professor.pfm.theme"
	followerInterval = 60
	// followerThrottle is launchd's minimum gap between two runs, one second:
	// a rename-replaced settings.json and its watch event land within it.
	followerThrottle = 1
)

func followerEscape(text string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(text)) // a bytes.Buffer write cannot fail
	return buffer.String()
}

// followerWatchPaths are the files whose change runs the follower: each
// account's settings.json by real path, each distinct real themes dir once
// and, on macOS, the global preferences. Discovery failures must not replace
// the last valid watches with a reduced set. No accounts is a valid empty set.
func followerWatchPaths(home string, darwin bool) ([]string, error) {
	var watch []string
	seen := map[string]bool{}
	add := func(path string) {
		if !seen[path] {
			seen[path] = true
			watch = append(watch, path)
		}
	}
	settings, err := accountSettings(home)
	if err != nil && !errors.Is(err, ErrNoAccounts) {
		return nil, fmt.Errorf("themesync: discover follower watches: %w", err)
	}
	for _, path := range settings {
		add(path)
		add(themesDir(filepath.Dir(path)))
	}
	if darwin {
		add(globalPreferences(home))
	}
	return watch, nil
}

// followerPlist is the launch agent that runs `pfm theme sync`, through the
// stable launcher program (agentlauncher: macOS re-registers an agent that
// names a rebuilt pfm), whenever a
// Claude account's settings.json or themes dir changes or macOS rewrites its
// global preferences, with a one-minute backstop and at load, on the one
// service PATH (launchd's bare default holds neither tmux nor herdr).
func followerPlist(program, home, stateDir string) (string, error) {
	watch, err := followerWatchPaths(home, true)
	if err != nil {
		return "", err
	}
	stringItems := func(values ...string) string {
		var out strings.Builder
		for _, value := range values {
			out.WriteString("\t\t<string>" + followerEscape(value) + "</string>\n")
		}
		return out.String()
	}
	logPath := filepath.Join(stateDir, "follow.log")
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + followerLabel + `</string>
	<key>ProgramArguments</key>
	<array>
` + stringItems(program, "theme", "sync") + `	</array>
	<key>WatchPaths</key>
	<array>
` + stringItems(watch...) + `	</array>
	<key>ThrottleInterval</key>
	<integer>` + fmt.Sprint(followerThrottle) + `</integer>
	<key>StartInterval</key>
	<integer>` + fmt.Sprint(followerInterval) + `</integer>
	<key>RunAtLoad</key>
	<true/>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>` + followerEscape(paths.ServicePath(home)) + `</string>
	</dict>
	<key>StandardOutPath</key>
	<string>` + followerEscape(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + followerEscape(logPath) + `</string>
</dict>
</plist>
`, nil
}

func (s *session) followerPath() string {
	return filepath.Join(s.env.Home, "Library", "LaunchAgents", followerLabel+".plist")
}

func (s *session) followerTarget() string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), followerLabel)
}

// followerLoaded asks launchd whether the agent is bootstrapped.
func (s *session) followerLoaded(ctx context.Context) (bool, error) {
	result, err := s.env.Runner.Run(ctx, []string{launchctlBinary, "print", s.followerTarget()}, deps.RunOptions{})
	if err != nil {
		return false, fmt.Errorf("themesync: launchctl print: %w", err)
	}
	return result.ExitCode == 0, nil
}

func (s *session) followerBootout(ctx context.Context) error {
	result, err := s.env.Runner.Run(ctx, []string{launchctlBinary, "bootout", s.followerTarget()}, deps.RunOptions{})
	if err != nil {
		return fmt.Errorf("themesync: launchctl bootout: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf(
			"themesync: launchctl bootout exited %d: %s",
			result.ExitCode,
			strings.TrimSpace(string(result.Stderr)),
		)
	}
	return nil
}

// followerRetire boots the agent out when loaded and deletes its plist; reports whether anything existed.
func (s *session) followerRetire(ctx context.Context) (bool, error) {
	loaded, err := s.followerLoaded(ctx)
	if err != nil {
		return false, err
	}
	if loaded {
		if err := s.followerBootout(ctx); err != nil {
			return false, err
		}
	}
	_, statErr := os.Stat(s.followerPath())
	existed := loaded || statErr == nil
	if err := os.Remove(s.followerPath()); err != nil && !os.IsNotExist(err) {
		return existed, fmt.Errorf("themesync: remove %s: %w", s.followerPath(), err)
	}
	return existed, nil
}

// errFollowerSelfReload is the follower's row when its plist changed inside
// its own launchd job: bootout there would make launchd stop the running sync
// before bootstrap, leaving no agent, so the reload waits for a run outside.
var errFollowerSelfReload = errors.New(
	"themesync: launch agent plist rewritten; the agent cannot reload itself from inside launchd: run `pfm theme sync` in a shell",
)

// errFollowerReloadDue is the follower's read-back while a rewritten plist
// still owes launchd its reload (refused inside the job, or a launchctl step
// that failed): the loaded agent runs the old definition.
var errFollowerReloadDue = errors.New(
	"themesync: launch agent plist rewritten but the loaded agent was not reloaded: run `pfm theme sync` in a shell",
)

// followerInOwnJob says whether this process is the follower's launchd job.
func (s *session) followerInOwnJob() bool { return s.env.LaunchdJob == followerLabel }

func (s *session) applyFollower(ctx context.Context) SurfaceResult {
	if !s.env.darwin() {
		return s.applySystemdFollower(ctx)
	}
	path := s.followerPath()
	if s.env.PfmBinary == "" {
		return s.failed(
			surfaceFollower,
			path,
			fmt.Errorf("themesync: Env.PfmBinary is empty; the follower cannot run pfm"),
		)
	}
	if _, err := agentlauncher.Ensure(s.env.Home); err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	want, err := followerPlist(agentlauncher.Program(s.env.Home), s.env.Home, s.store.dir)
	if err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	current, readErr := os.ReadFile(path)
	changed := readErr != nil || string(current) != want
	if changed {
		if err := s.replaceFile(path, []byte(want)); err != nil {
			return s.failed(surfaceFollower, path, err)
		}
		s.setDue(surfaceFollower, true) // owed until a bootstrap succeeds
	}
	loaded, err := s.followerLoaded(ctx)
	if err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	if loaded && !s.reloadDue(surfaceFollower, changed) {
		return s.result(surfaceFollower, VerdictApplied, path, "")
	}
	if loaded && s.followerInOwnJob() {
		s.setDue(surfaceFollower, true)
		return s.failed(surfaceFollower, path, errFollowerSelfReload)
	}
	if loaded {
		if err := s.followerBootout(ctx); err != nil {
			return s.failed(surfaceFollower, path, err)
		}
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	result, err := s.env.Runner.Run(ctx, []string{launchctlBinary, "bootstrap", domain, path}, deps.RunOptions{})
	if err != nil || result.ExitCode != 0 {
		return s.failed(surfaceFollower, path, fmt.Errorf("themesync: launchctl bootstrap: %v (exit %d): %s",
			err, result.ExitCode, strings.TrimSpace(string(result.Stderr))))
	}
	return s.settle(surfaceFollower, path, "", nil)
}

func (s *session) readFollower(ctx context.Context) SurfaceResult {
	if !s.env.darwin() {
		return s.readSystemdFollower(ctx)
	}
	path := s.followerPath()
	want, err := followerPlist(agentlauncher.Program(s.env.Home), s.env.Home, s.store.dir)
	if err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	current, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return s.in(surfaceFollower, VerdictDrift, path, "launch agent plist is missing", 0)
	case err != nil:
		return s.failed(surfaceFollower, path, fmt.Errorf("themesync: read %s: %w", path, err))
	case string(current) != want:
		return s.in(surfaceFollower, VerdictDrift, path, "launch agent plist differs from the render", 0)
	}
	launcher, err := agentlauncher.UpToDate(s.env.Home)
	switch {
	case err != nil:
		return s.failed(surfaceFollower, path, err)
	case !launcher:
		return s.in(surfaceFollower, VerdictDrift, path,
			"agent launcher "+agentlauncher.Program(s.env.Home)+" is missing or differs", 0)
	}
	loaded, err := s.followerLoaded(ctx)
	switch {
	case err != nil:
		return s.failed(surfaceFollower, path, err)
	case !loaded:
		return s.in(surfaceFollower, VerdictDrift, path, "launch agent is not loaded in "+s.followerTarget(), 0)
	case s.state.Due[surfaceFollower]:
		return s.failed(surfaceFollower, path, errFollowerReloadDue)
	}
	return s.in(surfaceFollower, VerdictInSync, path, "", 0)
}

func (s *session) restoreFollower(ctx context.Context) SurfaceResult {
	if !s.env.darwin() {
		return s.restoreSystemdFollower(ctx)
	}
	existed, err := s.followerRetire(ctx)
	switch {
	case err != nil:
		return s.failed(surfaceFollower, s.followerPath(), err)
	case !existed:
		return s.absent(surfaceFollower, s.followerPath(), "no launch agent installed")
	}
	return s.result(surfaceFollower, VerdictRestored, s.followerPath(), "")
}
