package themesync

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
}

// vscodeAck is vscode-ack.json, written by the extension after it applies a generation.
type vscodeAck struct {
	Generation int      `json:"generation"`
	AppliedAt  string   `json:"appliedAt"`
	Keys       []string `json:"keys"`
	Error      string   `json:"error"`
}

// vscodeContract renders the terminal colour keys for one appearance.
func (palette Palette) vscodeContract(mode Mode, appearance Appearance, generation int) vscodeContract {
	variant := palette.Variant(appearance)
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
	return vscodeContract{
		Enabled: true, Generation: generation, Palette: palette.Name, Title: palette.Title,
		Mode: string(mode), Appearance: string(appearance), Colors: colors,
	}
}

// vscodeOffContract is what Off writes: disabled, no colours, a bumped generation.
func vscodeOffContract(generation int) vscodeContract {
	return vscodeContract{Generation: generation, Colors: map[string]string{}}
}

// vscodeVerdict judges the extension's ack against the generation in force.
func vscodeVerdict(ack vscodeAck, haveAck bool, generation int, running bool, age time.Duration) (Verdict, string) {
	switch {
	case haveAck && ack.Error != "":
		return VerdictError, "extension reported: " + ack.Error
	case haveAck && ack.Generation == generation:
		return VerdictInSync, ""
	case !running:
		return VerdictPending, vscodeDeferred
	case age < vscodeGrace:
		return VerdictPending, "waiting for the extension to apply generation " + fmt.Sprint(generation)
	case haveAck:
		return VerdictDrift, fmt.Sprintf("extension ack is at generation %d, want %d", ack.Generation, generation)
	default:
		return VerdictDrift, fmt.Sprintf("no ack from the extension for generation %d", generation)
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
	contract := s.palette.vscodeContract(s.state.Mode, s.state.Appearance, s.state.Generation)
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
		if _, err := os.Stat(settings); err == nil {
			present = true
		}
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
	want := s.palette.vscodeContract(s.state.Mode, s.state.Appearance, s.state.Generation)
	if err := json.Unmarshal(content, &current); err != nil || fmt.Sprint(current) != fmt.Sprint(want) {
		return s.in(surfaceVscode, VerdictDrift, path, vscodeContractFile+" differs from the render", 0)
	}
	var ack vscodeAck
	haveAck := false
	switch raw, readErr := os.ReadFile(filepath.Join(s.store.dir, vscodeAckFile)); {
	case errors.Is(readErr, os.ErrNotExist):
	case readErr != nil:
		return s.failed(surfaceVscode, path, fmt.Errorf("themesync: read %s: %w", vscodeAckFile, readErr))
	default:
		if err := json.Unmarshal(raw, &ack); err != nil {
			return s.failed(surfaceVscode, path, fmt.Errorf("themesync: parse %s: %w", vscodeAckFile, err))
		}
		haveAck = true
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
		wallClock.Now().Sub(info.ModTime()))
	generation := 0
	if verdict == VerdictInSync {
		generation = ack.Generation
	}
	return s.in(surfaceVscode, verdict, path, detail, generation)
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
