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
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	vscodeContractFile = "current.json"
	vscodeAckFile      = "vscode-ack.json"
	// vscodeChromeRecordFile is the extension's record of the chrome it wrote
	// into each local workspace file (theme.js CHROME_RECORD); pfm only reads it.
	vscodeChromeRecordFile = "vscode-chrome.json"
	vscodeGrace            = 10 * time.Second
	vscodeDeferred         = "applies when VS Code starts"
	backupStampLayout      = "20060102-150405"
)

// wallClock is the time seam for backup stamps and the VS Code ack grace window.
var wallClock clock.Clock = clock.Real

// vscodeProducts are the macOS VS Code family user dirs whose settings.json is
// backed up; elsewhere the candidates are paths.VSCodeSettingsCandidates'.
var vscodeProducts = []string{"Code", "Code - Insiders", "VSCodium", "Cursor"}

// vscodeLinuxMarkers identify a running VS Code family main binary or VS Code
// Server (the extension host of a Remote-SSH window) in `ps -axo args=`
// outside macOS.
var vscodeLinuxMarkers = []string{
	"/.vscode-server/cli/servers/",
	"/.vscode-server/bin/",
	"/.vscode-server-insiders/cli/servers/",
	"/.vscode-server-insiders/bin/",
	"/usr/share/code/code",
	"/usr/share/code-insiders/code-insiders",
	"/usr/share/codium/codium",
	"/opt/visual-studio-code/code",
	"/snap/code/",
}

// vscodeAppMarkers identify a running VS Code family main binary in `ps -axo args=` on macOS.
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
	case haveAck && chromeFailure(ack, generation) != "":
		return VerdictError, chromeFailure(ack, generation)
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

// chromeErrors lists, by path order, each local workspace whose chrome the
// extension could not apply, current or not; empty when none failed.
func chromeErrors(ack vscodeAck, current bool, generation int) string {
	var failed []string
	for path, entry := range ack.Chrome {
		if entry.Error != "" && (entry.Generation == generation) == current {
			failed = append(failed, path)
		}
	}
	sort.Strings(failed)
	parts := make([]string, 0, len(failed))
	for _, path := range failed {
		entry := ack.Chrome[path]
		part := "window chrome " + filepath.Base(path) + ": " + entry.Error
		if !current {
			part = fmt.Sprintf("window chrome %s failed at generation %d (%s); retried when it next opens",
				filepath.Base(path), entry.Generation, entry.Error)
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "; ")
}

// chromeFailure is the ERROR detail of the local workspaces whose chrome the
// extension could not apply at the generation in force; an entry of an
// earlier generation is a window that has not opened since, never a current
// failure. Empty when none failed.
func chromeFailure(ack vscodeAck, generation int) string {
	return chromeErrors(ack, true, generation)
}

// chromeDetail names the local workspaces holding this generation's chrome
// and each earlier-generation failure still waiting for its window; empty
// when there are none.
func chromeDetail(ack vscodeAck, generation int) string {
	count := 0
	for _, entry := range ack.Chrome {
		if entry.Generation == generation && entry.Error == "" {
			count++
		}
	}
	var parts []string
	switch count {
	case 0:
	case 1:
		parts = append(parts, "window chrome in 1 local workspace")
	default:
		parts = append(parts, fmt.Sprintf("window chrome in %d local workspaces", count))
	}
	if stale := chromeErrors(ack, false, generation); stale != "" {
		parts = append(parts, stale)
	}
	return strings.Join(parts, "; ")
}

// dropGoneWorkspaces removes the chrome entries whose workspace file no
// longer exists: no window can open it again, so its outcome no longer
// counts. A workspace file whose stat fails otherwise keeps its entry.
func dropGoneWorkspaces(ack *vscodeAck) {
	for path := range ack.Chrome {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			delete(ack.Chrome, path)
		}
	}
}

// vscodeRunning reports whether `ps -axo args=` lists a process one of markers names.
func vscodeRunning(psOutput string, markers []string) bool {
	for _, line := range strings.Split(psOutput, "\n") {
		for _, marker := range markers {
			if strings.Contains(line, marker) {
				return true
			}
		}
	}
	return false
}

// vscodeSettings are the settings.json files the vscode row looks for: on
// macOS each product's under Application Support, elsewhere the one lookup
// pfm install --vscode shares, the VS Code Server's Machine settings included.
func (s *session) vscodeSettings() []string {
	if !s.env.darwin() {
		return paths.VSCodeSettingsCandidates(s.env.Home, false, paths.OSEnv{})
	}
	var settings []string
	for _, product := range vscodeProducts {
		settings = append(
			settings,
			filepath.Join(s.env.Home, "Library", "Application Support", product, "User", "settings.json"),
		)
	}
	return settings
}

// vscodeServerRoots are the VS Code Server installs outside macOS: a
// Remote-SSH window's extension host runs from one, and its Machine
// settings.json exists only once a Remote setting was saved, so the root
// alone already makes VS Code present (the installer links its extension
// there on the same evidence).
func (s *session) vscodeServerRoots() []string {
	if s.env.darwin() {
		return nil
	}
	return []string{filepath.Join(s.env.Home, ".vscode-server"), filepath.Join(s.env.Home, ".vscode-server-insiders")}
}

// vscodePresent says whether this host has a VS Code the contract serves: a
// settings.json the row looks for, or a VS Code Server root. A path that
// cannot be looked at is the error, naming it, never absence.
func (s *session) vscodePresent() (bool, string, error) {
	for _, settings := range s.vscodeSettings() {
		_, err := os.Stat(settings)
		if err == nil {
			return true, "", nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return false, settings, fmt.Errorf("themesync: stat %s: %w", settings, err)
		}
	}
	for _, root := range s.vscodeServerRoots() {
		info, err := os.Stat(root)
		if err == nil && info.IsDir() {
			return true, "", nil
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, root, fmt.Errorf("themesync: stat %s: %w", root, err)
		}
	}
	return false, "", nil
}

// vscodeMarkers are the process markers of a running VS Code on this platform.
func (s *session) vscodeMarkers() []string {
	if s.env.darwin() {
		return vscodeAppMarkers
	}
	return vscodeLinuxMarkers
}

func (s *session) vscodeContractPath() string { return filepath.Join(s.store.dir, vscodeContractFile) }

// vscodeServerNote names the VS Code Server roots an ABSENT row also looked
// for, "" on macOS.
func vscodeServerNote(roots []string) string {
	if len(roots) == 0 {
		return ""
	}
	return " and no VS Code Server at " + strings.Join(roots, ", ")
}

func (s *session) vscodeAbsent() SurfaceResult {
	return s.absent(
		surfaceVscode,
		s.vscodeContractPath(),
		"no VS Code settings.json at "+strings.Join(s.vscodeSettings(), ", ")+vscodeServerNote(s.vscodeServerRoots()),
	)
}

// vscodeEnsureBackups copies each existing settings.json beside itself once, before the first set.
func (s *session) vscodeEnsureBackups() error {
	for _, settings := range s.vscodeSettings() {
		content, readErr := os.ReadFile(settings)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return fmt.Errorf("themesync: read %s: %w", settings, readErr)
		}
		if s.state.VSCode[settings] != "" {
			continue
		}
		backup, err := s.backupBeside(settings, content)
		if err != nil {
			return err
		}
		if s.state.VSCode == nil {
			s.state.VSCode = map[string]string{}
		}
		s.state.VSCode[settings] = backup
		if err := s.persist(); err != nil {
			return err
		}
	}
	return nil
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
	if err := s.vscodeEnsureBackups(); err != nil {
		return s.failed(surfaceVscode, s.vscodeContractPath(), err)
	}
	present, failedPath, err := s.vscodePresent()
	if err != nil {
		return s.failed(surfaceVscode, failedPath, err)
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
	present, failedPath, err := s.vscodePresent()
	if err != nil {
		return s.failed(surfaceVscode, failedPath, err)
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
	dropGoneWorkspaces(&ack)
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
	verdict, detail := vscodeVerdict(
		ack,
		haveAck,
		s.state.Generation,
		vscodeRunning(string(result.Stdout), s.vscodeMarkers()),
		wallClock.Now().Sub(info.ModTime()),
		filepath.Join(s.store.dir, vscodeAckFile),
	)
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

// paintedWorkspaces lists, by path order, the local workspace files the
// extension's chrome record says still carry chrome: each one is restored only
// by a window that opens it. A workspace file that no longer exists is left
// out; an absent record is none, an unreadable one an error.
func (s *session) paintedWorkspaces() ([]string, error) {
	recordPath := filepath.Join(s.store.dir, vscodeChromeRecordFile)
	raw, err := os.ReadFile(recordPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("themesync: read %s: %w", recordPath, err)
	}
	var records map[string]struct {
		Keys map[string]json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(raw, &records); err != nil {
		return nil, fmt.Errorf("themesync: parse %s: %w", recordPath, err)
	}
	var painted []string
	for workspace, record := range records {
		if len(record.Keys) == 0 {
			continue
		}
		if _, err := os.Stat(workspace); errors.Is(err, os.ErrNotExist) {
			continue
		}
		painted = append(painted, workspace)
	}
	sort.Strings(painted)
	return painted, nil
}

func (s *session) restoreVscode(_ context.Context) SurfaceResult {
	if err := s.writeVscodeContract(vscodeOffContract(s.state.Generation)); err != nil {
		return s.failed(surfaceVscode, s.vscodeContractPath(), err)
	}
	painted, err := s.paintedWorkspaces()
	if err != nil {
		return s.failed(surfaceVscode, s.vscodeContractPath(), err)
	}
	present, failedPath, err := s.vscodePresent()
	if err != nil {
		return s.failed(surfaceVscode, failedPath, err)
	}
	if !present {
		return s.vscodeAbsent()
	}
	s.state.VSCode = nil
	detail := "the extension clears the colours when it next reads " + vscodeContractFile
	if len(painted) == 0 {
		return s.result(surfaceVscode, VerdictApplied, s.vscodeContractPath(), detail)
	}
	detail += "; window chrome stays in " + strings.Join(painted, ", ") +
		" until a VS Code window on each workspace applies the off"
	return s.result(surfaceVscode, VerdictPending, s.vscodeContractPath(), detail)
}
