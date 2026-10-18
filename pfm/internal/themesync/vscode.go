package themesync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	vscodeContractFile = "current.json"
	vscodeAckFile      = "vscode-ack.json"
	vscodeGrace        = 10 * time.Second
	vscodeDeferred     = "applies when VS Code starts"
	backupStampLayout  = "20060102-150405"
)

// wallClock is the time seam for backup stamps and the VS Code ack grace window.
var wallClock clock.Clock = clock.Real

// vscodeProducts are the VS Code family user dirs whose settings.json is backed up.
var vscodeProducts = []string{"Code", "Code - Insiders", "VSCodium", "Cursor"}

// vscodeAppMarkers identify a running VS Code family main binary in `ps -axo args=`.
var vscodeAppMarkers = []string{
	"/Visual Studio Code.app/Contents/MacOS/",
	"/Code.app/Contents/MacOS/",
	"/Visual Studio Code - Insiders.app/Contents/MacOS/",
	"/VSCodium.app/Contents/MacOS/",
	"/Cursor.app/Contents/MacOS/",
}

// vscodeContract is current.json, the file the pfm VS Code extension watches.
type vscodeContract struct {
	Enabled    bool              `json:"enabled"`
	Generation int               `json:"generation"`
	Palette    string            `json:"palette"`
	Title      string            `json:"title"`
	Mode       string            `json:"mode"`
	Appearance string            `json:"appearance"`
	Colors     map[string]string `json:"colors"`
	// Chrome is the window chrome (title, activity and status bars) the
	// extension writes into a LOCAL window's workspace colorCustomizations
	// only; a remote window never takes it.
	Chrome map[string]string `json:"chrome"`
}

// vscodeAck is vscode-ack.json, written by the extension after it applies a generation.
type vscodeAck struct {
	Generation int      `json:"generation"`
	AppliedAt  string   `json:"appliedAt"`
	Keys       []string `json:"keys"`
	Error      string   `json:"error"`
	// Chrome is each local workspace file's last chrome outcome, keyed by
	// its path; a remote or folder-only window adds no entry.
	Chrome map[string]vscodeChromeAck `json:"chrome"`
}

// vscodeChromeAck is one local workspace's chrome outcome: the generation
// whose chrome it carries, the keys set, and why it was not applied.
type vscodeChromeAck struct {
	Generation int      `json:"generation"`
	Keys       []string `json:"keys"`
	Error      string   `json:"error"`
}

// vscodeContract renders one appearance: the terminal colour keys in Colors
// and the window chrome in Chrome. They are separate fields because the
// extension routes them differently: colors go to the user's Global settings,
// chrome only to a local window's Workspace settings, where Peacock paints
// the same keys; every key Peacock paints there is set, so none stays its
// colour. The theme's VS Code pins replace the colour of the key they name.
func (applied Applied) vscodeContract(appearance Appearance, generation int) vscodeContract {
	variant := applied.variant(appearance)
	mode := string(applied.Side)
	if applied.Auto {
		mode = "auto"
	}
	colors := map[string]string{
		"terminal.background": variant.Background, "terminal.foreground": variant.Foreground,
		"terminalCursor.foreground": variant.Cursor, "terminal.selectionBackground": variant.SelectionBackground,
		"terminal.tab.activeBorder": variant.Accent,
	}
	names := []string{
		"Black",
		"Red",
		"Green",
		"Yellow",
		"Blue",
		"Magenta",
		"Cyan",
		"White",
		"BrightBlack",
		"BrightRed",
		"BrightGreen",
		"BrightYellow",
		"BrightBlue",
		"BrightMagenta",
		"BrightCyan",
		"BrightWhite",
	}
	for index, name := range names {
		colors["terminal.ansi"+name] = variant.ANSI[index]
	}
	chrome := map[string]string{}
	for _, key := range []string{
		"titleBar.activeBackground", "activityBar.activeBackground", "activityBarTop.activeBackground",
		"statusBar.background",
	} {
		chrome[key] = variant.Surface
	}
	for _, key := range []string{
		"titleBar.activeForeground", "activityBar.foreground", "activityBarTop.foreground",
		"commandCenter.foreground", "statusBar.foreground",
	} {
		chrome[key] = variant.Foreground
	}
	for _, key := range []string{
		"titleBar.inactiveBackground", "activityBar.background", "activityBarTop.background",
		"statusBar.inactiveBackground", "activityBarBadge.foreground", "statusBar.debuggingForeground",
		"statusBarItem.remoteForeground",
	} {
		chrome[key] = variant.Background
	}
	for _, key := range []string{
		"titleBar.inactiveForeground", "activityBar.inactiveForeground", "activityBarTop.inactiveForeground",
	} {
		chrome[key] = variant.Muted
	}
	for _, key := range []string{
		"activityBar.activeBorder", "activityBarTop.activeBorder", "activityBarBadge.background",
		"sash.hoverBorder", "statusBar.debuggingBackground", "statusBarItem.remoteBackground",
	} {
		chrome[key] = variant.Accent
	}
	for _, key := range []string{"commandCenter.border", "statusBarItem.hoverBackground"} {
		chrome[key] = variant.Overlay
	}
	pins := vscodePins(applied.Pins)
	overlayPins(colors, pins.Colors)
	overlayPins(chrome, pins.Chrome)
	return vscodeContract{
		Enabled: true, Generation: generation, Palette: applied.Value, Title: applied.Title,
		Mode: mode, Appearance: string(appearance), Colors: colors, Chrome: chrome,
	}
}

// vscodeOffContract is what Off writes: disabled, no colours, no chrome, a bumped generation.
func vscodeOffContract(generation int) vscodeContract {
	return vscodeContract{Generation: generation, Colors: map[string]string{}, Chrome: map[string]string{}}
}

// vscodeVerdict judges the extension's ack against the generation in force.
// With no ack ever while VS Code runs past the grace the verdict is PENDING:
// the extension is the single writer of settings.json and acknowledges only
// after a window reload (D20). An ack for another generation past the grace
// is DRIFT.
func vscodeVerdict(
	ack vscodeAck, haveAck bool, generation int, running bool, age time.Duration, ackPath string,
) (Verdict, string) {
	switch {
	case haveAck && ack.Error != "":
		return VerdictError, "extension reported: " + ack.Error
	case haveAck && chromeFailure(ack) != "":
		return VerdictError, chromeFailure(ack)
	case haveAck && ack.Generation == generation:
		return VerdictInSync, chromeDetail(ack, generation)
	case !running:
		return VerdictPending, vscodeDeferred
	case age < vscodeGrace:
		return VerdictPending, "waiting for the extension to apply generation " + fmt.Sprint(generation)
	case haveAck:
		return VerdictDrift, fmt.Sprintf("extension ack is at generation %d, want %d", ack.Generation, generation)
	default:
		return VerdictPending, fmt.Sprintf("reload a VS Code window once: no Professor extension has acknowledged "+
			"a theme yet (%s absent)", ackPath)
	}
}

// chromeFailure lists each local workspace whose chrome the extension could
// not apply, by path order, as one ERROR detail; empty when none failed.
func chromeFailure(ack vscodeAck) string {
	var paths []string
	for path, entry := range ack.Chrome {
		if entry.Error != "" {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	parts := make([]string, 0, len(paths))
	for _, path := range paths {
		parts = append(parts, "window chrome "+filepath.Base(path)+": "+ack.Chrome[path].Error)
	}
	return strings.Join(parts, "; ")
}

// chromeDetail names the local workspaces holding this generation's chrome;
// empty when there are none.
func chromeDetail(ack vscodeAck, generation int) string {
	count := 0
	for _, entry := range ack.Chrome {
		if entry.Generation == generation && entry.Error == "" {
			count++
		}
	}
	switch count {
	case 0:
		return ""
	case 1:
		return "window chrome in 1 local workspace"
	default:
		return fmt.Sprintf("window chrome in %d local workspaces", count)
	}
}

// vscodeRunning reports whether `ps -axo args=` lists a VS Code family main binary.
func vscodeRunning(psOutput string) bool {
	for _, line := range strings.Split(psOutput, "\n") {
		for _, marker := range vscodeAppMarkers {
			if strings.Contains(line, marker) {
				return true
			}
		}
	}
	return false
}

func (s *session) vscodeSettings() []string {
	var paths []string
	for _, product := range vscodeProducts {
		paths = append(
			paths,
			filepath.Join(s.env.Home, "Library", "Application Support", product, "User", "settings.json"),
		)
	}
	return paths
}

func (s *session) vscodeContractPath() string { return filepath.Join(s.store.dir, vscodeContractFile) }

func (s *session) vscodeAbsent() SurfaceResult {
	return s.absent(
		surfaceVscode,
		s.vscodeContractPath(),
		"no VS Code settings.json at "+strings.Join(s.vscodeSettings(), ", "),
	)
}

// vscodeEnsureBackups copies each existing settings.json beside itself once, before the first set.
func (s *session) vscodeEnsureBackups() (present bool, err error) {
	for _, settings := range s.vscodeSettings() {
		content, readErr := os.ReadFile(settings)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return present, fmt.Errorf("themesync: read %s: %w", settings, readErr)
		}
		present = true
		if s.state.VSCode[settings] != "" {
			continue
		}
		backup, err := s.backupBeside(settings, content)
		if err != nil {
			return present, err
		}
		if s.state.VSCode == nil {
			s.state.VSCode = map[string]string{}
		}
		s.state.VSCode[settings] = backup
		if err := s.persist(); err != nil {
			return present, err
		}
	}
	return present, nil
}

func (s *session) writeVscodeContract(contract vscodeContract) error {
	content, err := json.MarshalIndent(contract, "", "  ")
	if err != nil {
		return fmt.Errorf("themesync: encode %s: %w", vscodeContractFile, err)
	}
	content = append(content, '\n')
	if current, readErr := os.ReadFile(s.vscodeContractPath()); readErr == nil && bytes.Equal(current, content) {
		return nil
	}
	if err := atomicfile.Write(s.vscodeContractPath(), content, 0o644); err != nil {
		return fmt.Errorf("themesync: write %s: %w", s.vscodeContractPath(), err)
	}
	return nil
}

func (s *session) applyVscode(_ context.Context) SurfaceResult {
	present, err := s.vscodeEnsureBackups()
	if err != nil {
		return s.failed(surfaceVscode, s.vscodeContractPath(), err)
	}
	contract := s.theme().vscodeContract(s.state.Appearance, s.state.Generation)
	if err := s.writeVscodeContract(contract); err != nil {
		return s.failed(surfaceVscode, s.vscodeContractPath(), err)
	}
	if !present {
		return s.vscodeAbsent()
	}
	return s.result(surfaceVscode, VerdictApplied, s.vscodeContractPath(), "")
}

func (s *session) readVscode(ctx context.Context) SurfaceResult {
	path := s.vscodeContractPath()
	present := false
	for _, settings := range s.vscodeSettings() {
		_, err := os.Stat(settings)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return s.failed(surfaceVscode, settings, fmt.Errorf("themesync: stat %s: %w", settings, err))
		}
		present = present || err == nil
	}
	if !present {
		return s.vscodeAbsent()
	}
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s.in(surfaceVscode, VerdictDrift, path, vscodeContractFile+" missing", 0)
	}
	if err != nil {
		return s.failed(surfaceVscode, path, fmt.Errorf("themesync: read %s: %w", vscodeContractFile, err))
	}
	var current vscodeContract
	want := s.theme().vscodeContract(s.state.Appearance, s.state.Generation)
	if err := json.Unmarshal(content, &current); err != nil || fmt.Sprint(current) != fmt.Sprint(want) {
		return s.in(surfaceVscode, VerdictDrift, path, vscodeContractFile+" differs from the render", 0)
	}
	ack, haveAck, err := s.readAck()
	if err != nil {
		return s.failed(surfaceVscode, path, err)
	}
	result, err := s.env.Runner.Run(ctx, []string{"ps", "-axo", "args="}, deps.RunOptions{})
	if err != nil || result.ExitCode != 0 {
		return s.failed(
			surfaceVscode,
			path,
			fmt.Errorf("themesync: list processes: %v (exit %d)", err, result.ExitCode),
		)
	}
	info, err := os.Stat(path)
	if err != nil {
		return s.failed(surfaceVscode, path, fmt.Errorf("themesync: stat %s: %w", path, err))
	}
	verdict, detail := vscodeVerdict(ack, haveAck, s.state.Generation, vscodeRunning(string(result.Stdout)),
		wallClock.Now().Sub(info.ModTime()), filepath.Join(s.store.dir, vscodeAckFile))
	generation := 0
	if verdict == VerdictInSync {
		generation = ack.Generation
	}
	return s.in(surfaceVscode, verdict, path, detail, generation)
}

// readAck reads the extension's ack; none written yet is (zero, false, nil).
func (s *session) readAck() (ack vscodeAck, found bool, err error) {
	raw, readErr := os.ReadFile(filepath.Join(s.store.dir, vscodeAckFile))
	switch {
	case errors.Is(readErr, os.ErrNotExist):
		return ack, false, nil
	case readErr != nil:
		return ack, false, fmt.Errorf("themesync: read %s: %w", vscodeAckFile, readErr)
	}
	if err := json.Unmarshal(raw, &ack); err != nil {
		return ack, false, fmt.Errorf("themesync: parse %s: %w", vscodeAckFile, err)
	}
	return ack, true, nil
}

func (s *session) restoreVscode(_ context.Context) SurfaceResult {
	if err := s.writeVscodeContract(vscodeOffContract(s.state.Generation)); err != nil {
		return s.failed(surfaceVscode, s.vscodeContractPath(), err)
	}
	for _, settings := range s.vscodeSettings() {
		if _, err := os.Stat(settings); err == nil {
			s.state.VSCode = nil
			return s.result(surfaceVscode, VerdictApplied, s.vscodeContractPath(),
				"the extension clears the colours when it next reads "+vscodeContractFile)
		}
	}
	return s.vscodeAbsent()
}
