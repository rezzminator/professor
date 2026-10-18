package themesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The Herdr plugin that paints the sidebar indicators: a manifest pfm writes
// next to Herdr's config and links into Herdr's plugin registry. Its hooks run
// `pfm internal herdr-indicators`, which reports one custom token per pane and
// workspace; herdr_rows.go styles the sidebar rows that name those tokens.
const (
	herdrPluginDirName   = "pfm-indicators"
	herdrPluginManifest  = "herdr-plugin.toml"
	herdrPluginName      = "Professor sidebar indicators"
	herdrPluginVersion   = "1.0.0"
	herdrPluginMinHerdr  = "0.9.5"
	herdrRegistryName    = "plugins.json"
	herdrPluginNotLinked = "applies when Herdr is running"
)

func (s *session) herdrConfigDir() string { return filepath.Join(s.env.Home, ".config", "herdr") }

func (s *session) herdrPluginDir() string {
	return filepath.Join(s.herdrConfigDir(), herdrPluginDirName)
}

func (s *session) herdrPluginPath() string {
	return filepath.Join(s.herdrPluginDir(), herdrPluginManifest)
}

// herdrPluginRender is the manifest: the plugin's identity, one startup hook
// and one event hook per Herdr event that repaints the indicators, each running
// `pfm internal herdr-indicators`.
func herdrPluginRender(pfmBinary string) string {
	command := fmt.Sprintf("command = [%q, \"internal\", %q]\n", pfmBinary, herdr.IndicatorVerb)
	var out strings.Builder
	fmt.Fprintf(&out, "id = %q\nname = %q\nversion = %q\nmin_herdr_version = %q\nplatforms = [\"macos\", \"linux\"]\n",
		herdr.IndicatorPluginID, herdrPluginName, herdrPluginVersion, herdrPluginMinHerdr)
	out.WriteString("\n[[startup]]\n" + command)
	for _, event := range herdr.IndicatorEvents() {
		fmt.Fprintf(&out, "\n[[events]]\non = %q\n%s", event, command)
	}
	return out.String()
}

// herdrPluginLinked reads Herdr's plugin registry (plugins.json, a JSON array
// of entries carrying plugin_id): whether the indicator plugin is in it. A
// missing registry is no plugin; an unreadable or malformed one is an error.
func (s *session) herdrPluginLinked() (bool, error) {
	path := filepath.Join(s.herdrConfigDir(), herdrRegistryName)
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("themesync: read %s: %w", path, err)
	}
	var entries []struct {
		PluginID string `json:"plugin_id"`
	}
	if err := json.Unmarshal(content, &entries); err != nil {
		return false, fmt.Errorf("themesync: parse %s: %w", path, err)
	}
	for _, entry := range entries {
		if entry.PluginID == herdr.IndicatorPluginID {
			return true, nil
		}
	}
	return false, nil
}

// herdrPluginCommand runs `herdr plugin <verb> <arg>`; deferred says no herdr
// is on PATH, or Herdr answered that no server is up, the case the next sync
// completes. Herdr 0.9.5 links with no server up by writing plugins.json
// itself, so only an unlink meets the second case.
func (s *session) herdrPluginCommand(ctx context.Context, verb, arg string) (deferred bool, err error) {
	binary, lookErr := s.env.Runner.LookPath("herdr")
	if lookErr != nil {
		return true, nil //nolint:nilerr // no herdr binary is the deferred case, not a failure
	}
	argv := []string{binary, "plugin", verb, arg}
	result, err := s.env.Runner.Run(ctx, argv, deps.RunOptions{})
	if err != nil {
		return false, fmt.Errorf("themesync: %s: %w", strings.Join(argv[1:], " "), err)
	}
	if result.ExitCode == 0 {
		return false, nil
	}
	text := strings.TrimSpace(string(result.Stderr) + " " + string(result.Stdout))
	for _, marker := range herdrNotRunning {
		if strings.Contains(strings.ToLower(text), marker) {
			return true, nil
		}
	}
	return false, fmt.Errorf("themesync: %s exited %d: %s", strings.Join(argv[1:], " "), result.ExitCode, text)
}

// applyHerdrPlugin retires every theme off's indicator stop marker, then
// writes the manifest and links it: when the registry lacks the plugin, or the
// manifest just changed, so the registry's entry is taken again from the
// manifest on disk (Herdr re-reads every linked manifest before it runs the
// hooks). A link that cannot run yet (no herdr on PATH) is a note; any other
// failure is an error naming the command.
func (s *session) applyHerdrPlugin(ctx context.Context) (note string, err error) {
	if s.env.PfmBinary == "" {
		return "", errors.New("themesync: no pfm binary to run the herdr indicator hooks")
	}
	// The theme off's markers outlive it, so a painter that starts after it
	// stands down; the theme on is their only remover.
	if err := herdr.RetireIndicatorStops(paths.HerdrLockDir(s.env.Home)); err != nil {
		return "", fmt.Errorf("themesync: %w", err)
	}
	path := s.herdrPluginPath()
	manifest := herdrPluginRender(s.env.PfmBinary)
	current, readErr := os.ReadFile(path)
	if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
		return "", fmt.Errorf("themesync: read %s: %w", path, readErr)
	}
	rewritten := string(current) != manifest
	if rewritten {
		if err := s.replaceFile(path, []byte(manifest)); err != nil {
			return "", err
		}
	}
	linked, err := s.herdrPluginLinked()
	if err != nil {
		return "", err
	}
	if linked && !rewritten {
		return "", nil
	}
	deferred, err := s.herdrPluginCommand(ctx, "link", s.herdrPluginDir())
	if err != nil {
		return "", err
	}
	if deferred {
		return "indicator plugin " + herdrPluginNotLinked, nil
	}
	return "", nil
}

// readHerdrPlugin reports how the plugin differs from its render: "" when it
// matches (an unlinked plugin counts only when herdr is on PATH to link it).
func (s *session) readHerdrPlugin() (string, error) {
	path := s.herdrPluginPath()
	content, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "indicator plugin manifest missing from " + path, nil
	case err != nil:
		return "", fmt.Errorf("themesync: read %s: %w", path, err)
	case string(content) != herdrPluginRender(s.env.PfmBinary):
		return "indicator plugin manifest differs from its render", nil
	}
	linked, err := s.herdrPluginLinked()
	if err != nil {
		return "", err
	}
	if _, lookErr := s.env.Runner.LookPath("herdr"); !linked && lookErr == nil {
		return "indicator plugin " + herdr.IndicatorPluginID + " is not linked in Herdr", nil
	}
	return "", nil
}

// restoreHerdrPlugin unlinks the plugin and removes its manifest and
// directory. An unlink that cannot run because Herdr is not up leaves the
// registry entry and says so; the directory goes either way.
func (s *session) restoreHerdrPlugin(ctx context.Context) (note string, err error) {
	linked, err := s.herdrPluginLinked()
	if err != nil {
		return "", err
	}
	if linked {
		deferred, err := s.herdrPluginCommand(ctx, "unlink", herdr.IndicatorPluginID)
		if err != nil {
			return "", err
		}
		if deferred {
			note = "indicator plugin stays linked until `herdr plugin unlink " + herdr.IndicatorPluginID + "` runs"
		}
	}
	path := s.herdrPluginPath()
	if _, statErr := os.Stat(path); statErr == nil {
		if err := s.removeFile(path); err != nil {
			return "", err
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("themesync: stat %s: %w", path, statErr)
	}
	if err := os.Remove(s.herdrPluginDir()); err != nil && !errors.Is(err, os.ErrNotExist) &&
		!errors.Is(err, syscall.ENOTEMPTY) { // a directory holding the user's own files stays
		return "", fmt.Errorf("themesync: remove %s: %w", s.herdrPluginDir(), err)
	}
	return note, nil
}
