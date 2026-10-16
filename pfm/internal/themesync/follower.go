package themesync

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	launchctlBinary  = "launchctl"
	followerLabel    = "com.professor.pfm.theme"
	followerInterval = 60
)

func followerEscape(text string) string {
	var buffer bytes.Buffer
	_ = xml.EscapeText(&buffer, []byte(text)) // a bytes.Buffer write cannot fail
	return buffer.String()
}

// followerPlist is the launch agent that runs `pfm theme sync` whenever macOS
// rewrites its global preferences, with a one-minute backstop and at load.
func followerPlist(pfm, home, stateDir string) string {
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
` + stringItems(pfm, "theme", "sync") + `	</array>
	<key>WatchPaths</key>
	<array>
` + stringItems(filepath.Join(home, "Library", "Preferences", ".GlobalPreferences.plist")) + `	</array>
	<key>StartInterval</key>
	<integer>` + fmt.Sprint(followerInterval) + `</integer>
	<key>RunAtLoad</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + followerEscape(logPath) + `</string>
	<key>StandardErrorPath</key>
	<string>` + followerEscape(logPath) + `</string>
</dict>
</plist>
`
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

func (s *session) followerNotNeeded() string {
	return fmt.Sprintf("mode %s: the appearance follower is not needed", s.state.Mode)
}

func (s *session) applyFollower(ctx context.Context) SurfaceResult {
	path := s.followerPath()
	if s.state.Mode != ModeAuto {
		if _, err := s.followerRetire(ctx); err != nil {
			return s.failed(surfaceFollower, path, err)
		}
		return s.absent(surfaceFollower, path, s.followerNotNeeded())
	}
	if s.env.PfmBinary == "" {
		return s.failed(
			surfaceFollower,
			path,
			fmt.Errorf("themesync: Env.PfmBinary is empty; the follower cannot run pfm"),
		)
	}
	want := followerPlist(s.env.PfmBinary, s.env.Home, s.store.dir)
	current, readErr := os.ReadFile(path)
	changed := readErr != nil || string(current) != want
	if changed {
		if err := s.replaceFile(path, []byte(want)); err != nil {
			return s.failed(surfaceFollower, path, err)
		}
	}
	loaded, err := s.followerLoaded(ctx)
	if err != nil {
		return s.failed(surfaceFollower, path, err)
	}
	if loaded && !changed {
		return s.result(surfaceFollower, VerdictApplied, path, "")
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
	return s.result(surfaceFollower, VerdictApplied, path, "")
}

func (s *session) readFollower(ctx context.Context) SurfaceResult {
	path := s.followerPath()
	if s.state.Mode != ModeAuto {
		return s.absent(surfaceFollower, path, s.followerNotNeeded())
	}
	current, err := os.ReadFile(path)
	switch {
	case os.IsNotExist(err):
		return s.in(surfaceFollower, VerdictDrift, path, "launch agent plist is missing", 0)
	case err != nil:
		return s.failed(surfaceFollower, path, fmt.Errorf("themesync: read %s: %w", path, err))
	case string(current) != followerPlist(s.env.PfmBinary, s.env.Home, s.store.dir):
		return s.in(surfaceFollower, VerdictDrift, path, "launch agent plist differs from the render", 0)
	}
	loaded, err := s.followerLoaded(ctx)
	switch {
	case err != nil:
		return s.failed(surfaceFollower, path, err)
	case !loaded:
		return s.in(surfaceFollower, VerdictDrift, path, "launch agent is not loaded in "+s.followerTarget(), 0)
	}
	return s.in(surfaceFollower, VerdictInSync, path, "", 0)
}

func (s *session) restoreFollower(ctx context.Context) SurfaceResult {
	existed, err := s.followerRetire(ctx)
	switch {
	case err != nil:
		return s.failed(surfaceFollower, s.followerPath(), err)
	case !existed:
		return s.absent(surfaceFollower, s.followerPath(), "no launch agent installed")
	}
	return s.result(surfaceFollower, VerdictRestored, s.followerPath(), "")
}
