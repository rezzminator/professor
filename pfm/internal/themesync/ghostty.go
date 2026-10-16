package themesync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	ghosttyIncludeFile    = "pfm-theme.ghostty"
	ghosttyThemeDir       = "pfm-theme"
	ghosttyLegacyInclude  = "config-file = ?pfm-theme.ghostty\n" // the relative form an earlier pfm wrote
	ghosttyMarkerLine     = "# pfm theme (managed: pfm theme off removes these two lines)\n"
	ghosttyAppSuffix      = "/Ghostty.app/Contents/MacOS/ghostty"
	ghosttySystemApps     = "/Applications"
	ghosttyDeferred       = "applies when Ghostty starts"
	ghosttyPaletteEntries = 16
)

// ghosttyProcess is one running Ghostty main process.
type ghosttyProcess struct {
	PID    int
	Binary string
}

// ghosttyCandidates are the config files Ghostty reads, in the order the sync
// takes the first that exists.
func ghosttyCandidates(home string) []string {
	support := filepath.Join(home, "Library", "Application Support", "com.mitchellh.ghostty")
	xdg := filepath.Join(home, ".config", "ghostty")
	return []string{
		filepath.Join(support, "config.ghostty"), filepath.Join(support, "config"),
		filepath.Join(xdg, "config.ghostty"), filepath.Join(xdg, "config"),
	}
}

func ghosttyThemeFile(dir, palette string, appearance Appearance) string {
	return filepath.Join(dir, ghosttyThemeDir, palette+"-"+string(appearance))
}

func ghosttyThemeContent(palette Palette, appearance Appearance) string {
	variant := palette.Variant(appearance)
	var out strings.Builder
	fmt.Fprintf(&out, "# pfm theme · %s · %s\n", palette.Name, appearance)
	fmt.Fprintf(
		&out,
		"background = %s\nforeground = %s\ncursor-color = %s\n",
		variant.Background,
		variant.Foreground,
		variant.Cursor,
	)
	fmt.Fprintf(
		&out,
		"selection-background = %s\nselection-foreground = %s\n",
		variant.SelectionBackground,
		variant.SelectionForeground,
	)
	for index, colour := range variant.ANSI {
		fmt.Fprintf(&out, "palette = %d=%s\n", index, colour)
	}
	return out.String()
}

// ghosttyRender is every pfm-owned file for a config dir: the include file
// (stamped with the generation) and both theme files.
func ghosttyRender(palette Palette, mode Mode, generation int, dir string) map[string]string {
	dark := ghosttyThemeFile(dir, palette.Name, Dark)
	light := ghosttyThemeFile(dir, palette.Name, Light)
	theme := "theme = light:" + light + ",dark:" + dark
	switch mode {
	case ModeDark:
		theme = "theme = " + dark
	case ModeLight:
		theme = "theme = " + light
	case ModeAuto:
	}
	return map[string]string{
		filepath.Join(dir, ghosttyIncludeFile): fmt.Sprintf("# pfm theme · %s · gen %d · mode %s\n%s\n",
			palette.Name, generation, mode, theme),
		dark:  ghosttyThemeContent(palette, Dark),
		light: ghosttyThemeContent(palette, Light),
	}
}

// ghosttyIncludeLine names the include file pfm writes in dir by its absolute
// path, so a config kept as a symlink into another directory still finds it.
func ghosttyIncludeLine(dir string) string {
	return "config-file = ?" + filepath.Join(dir, ghosttyIncludeFile) + "\n"
}

// ghosttyInclude appends the include pair for dir to the user's config; added
// is the exact text appended ("" when the pair is already there).
func ghosttyInclude(original, dir string) (updated, added string) {
	line := ghosttyIncludeLine(dir)
	if strings.Contains(original, line) || strings.HasSuffix(original, strings.TrimSuffix(line, "\n")) {
		return original, ""
	}
	prefix := ""
	if original != "" && !strings.HasSuffix(original, "\n") {
		prefix = "\n"
	}
	added = prefix + ghosttyMarkerLine + line
	return original + added, added
}

// ghosttyExclude undoes ghosttyInclude: the exact appended text when it is
// still there, else the two lines wherever the user moved them; either way the
// legacy relative include line an earlier pfm wrote goes too.
func ghosttyExclude(current, added, dir string) string {
	if added == "" {
		return current
	}
	if strings.Contains(current, added) {
		current = strings.Replace(current, added, "", 1)
	} else {
		current = strings.Replace(strings.Replace(current, ghosttyMarkerLine, "", 1), ghosttyIncludeLine(dir), "", 1)
	}
	return strings.ReplaceAll(strings.ReplaceAll(current, ghosttyMarkerLine+ghosttyLegacyInclude, ""),
		ghosttyLegacyInclude, "")
}

// ghosttyProcesses picks the Ghostty main processes out of `ps -axo pid=,args=`:
// the app binary alone, with no arguments (a process started with its own
// config flags does not read the default files).
func ghosttyProcesses(psOutput string) []ghosttyProcess {
	var found []ghosttyProcess
	for _, line := range strings.Split(psOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		binary := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
		if err != nil || !strings.HasPrefix(binary, "/") || !strings.HasSuffix(binary, ghosttyAppSuffix) ||
			strings.Contains(strings.TrimSuffix(binary, ghosttyAppSuffix), " /") {
			continue
		}
		found = append(found, ghosttyProcess{PID: pid, Binary: binary})
	}
	return found
}

// ghosttyConfig is the first existing Ghostty config file, or "" with every path looked for;
// a candidate that cannot be looked at is an error, never a missing config.
func (s *session) ghosttyConfig() (string, []string, error) {
	candidates := ghosttyCandidates(s.env.Home)
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", candidates, fmt.Errorf("themesync: stat %s: %w", candidate, err)
		}
		if err == nil && info.Mode().IsRegular() {
			return candidate, candidates, nil
		}
	}
	return "", candidates, nil
}

func (s *session) ghosttyAbsent(candidates []string) SurfaceResult {
	return s.absent(surfaceGhostty, "", "no Ghostty config at "+strings.Join(candidates, ", "))
}

// ghosttyRunning lists the Ghostty main processes now running.
func (s *session) ghosttyRunning(ctx context.Context) ([]ghosttyProcess, error) {
	result, err := s.env.Runner.Run(ctx, []string{"ps", "-axo", "pid=,args="}, deps.RunOptions{})
	if err != nil {
		return nil, fmt.Errorf("themesync: list processes: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("themesync: ps exited %d: %s", result.ExitCode, strings.TrimSpace(string(result.Stderr)))
	}
	var running []ghosttyProcess
	for _, process := range ghosttyProcesses(string(result.Stdout)) {
		if s.ghosttyExecuting(ctx, process) {
			running = append(running, process)
		}
	}
	return running, nil
}

// ghosttyExecuting reports whether process still runs the bare app binary:
// its arguments are exactly the binary and the executable it runs is that
// binary (`comm` is the executable's path on macOS), so a process that only
// names an app path among its arguments, or a reused pid, never qualifies.
func (s *session) ghosttyExecuting(ctx context.Context, process ghosttyProcess) bool {
	pid := strconv.Itoa(process.PID)
	for _, column := range []string{"args=", "comm="} {
		result, err := s.env.Runner.Run(ctx, []string{"ps", "-o", column, "-p", pid}, deps.RunOptions{})
		if err != nil || result.ExitCode != 0 || strings.TrimSpace(string(result.Stdout)) != process.Binary {
			return false
		}
	}
	return true
}

// ghosttyBinaryPath is the Ghostty binary to validate with: the running
// process's, else the app in the system or the user's Applications, else
// `ghostty` on PATH; "" with every place tried when there is none.
func (s *session) ghosttyBinaryPath(processes []ghosttyProcess) (string, []string, error) {
	if len(processes) > 0 {
		return processes[0].Binary, nil, nil
	}
	system := s.env.systemApps
	if system == "" {
		system = ghosttySystemApps
	}
	var tried []string
	for _, apps := range []string{system, filepath.Join(s.env.Home, "Applications")} {
		candidate := apps + ghosttyAppSuffix
		info, err := os.Stat(candidate)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", tried, fmt.Errorf("themesync: stat %s: %w", candidate, err)
		}
		if err == nil && info.Mode().IsRegular() {
			return candidate, tried, nil
		}
		tried = append(tried, candidate)
	}
	if path, err := s.env.Runner.LookPath("ghostty"); err == nil {
		return path, tried, nil
	}
	return "", append(tried, "ghostty on PATH"), nil
}

// ghosttyValidate runs `ghostty +validate-config` over the default config
// files; with no Ghostty binary anywhere it returns the note saying so, and
// the row stands on the file read-back alone.
func (s *session) ghosttyValidate(ctx context.Context, processes []ghosttyProcess) (string, error) {
	binary, tried, err := s.ghosttyBinaryPath(processes)
	if err != nil {
		return "", err
	}
	if binary == "" {
		return "not validated: Ghostty binary not found (tried " + strings.Join(tried, ", ") + ")", nil
	}
	result, err := s.env.Runner.Run(ctx, []string{binary, "+validate-config"}, deps.RunOptions{})
	if err != nil {
		return "", fmt.Errorf("themesync: ghostty +validate-config: %w", err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("themesync: ghostty +validate-config exited %d: %s", result.ExitCode,
			strings.TrimSpace(string(result.Stdout)+" "+string(result.Stderr)))
	}
	return "", nil
}

// ghosttySignal sends SIGUSR2 (reload) to each process that, re-read right
// before the kill, still executes the bare app binary.
func (s *session) ghosttySignal(ctx context.Context, processes []ghosttyProcess) error {
	var failures []string
	for _, process := range processes {
		pid := strconv.Itoa(process.PID)
		if !s.ghosttyExecuting(ctx, process) {
			continue // the process exited or was replaced: never signal a reused pid
		}
		result, err := s.env.Runner.Run(ctx, []string{"kill", "-USR2", pid}, deps.RunOptions{})
		if err != nil || result.ExitCode != 0 {
			failures = append(
				failures,
				fmt.Sprintf("pid %s: %v %s", pid, err, strings.TrimSpace(string(result.Stderr))),
			)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("themesync: reload ghostty: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (s *session) applyGhostty(ctx context.Context) SurfaceResult {
	config, candidates, err := s.ghosttyConfig()
	if err != nil {
		return s.failed(surfaceGhostty, "", err)
	}
	if config == "" {
		return s.ghosttyAbsent(candidates)
	}
	dir := filepath.Dir(config)
	original, err := os.ReadFile(config)
	if err != nil {
		return s.failed(surfaceGhostty, config, fmt.Errorf("themesync: read %s: %w", config, err))
	}
	changed := false
	files := ghosttyRender(s.palette, s.state.Mode, s.state.Generation, dir)
	if updated, added := ghosttyInclude(string(original), dir); added != "" {
		backup, err := s.backupBeside(config, original)
		if err != nil {
			return s.failed(surfaceGhostty, config, err)
		}
		s.state.Ghostty = &ghosttySnapshot{Config: config, Added: added, Backup: backup}
		if err := s.persist(); err != nil {
			return s.failed(surfaceGhostty, config, err)
		}
		files[config] = updated
	}
	for path, content := range files {
		current, readErr := os.ReadFile(path)
		if readErr == nil && string(current) == content {
			continue
		}
		if err := s.replaceFile(path, []byte(content)); err != nil {
			return s.failed(surfaceGhostty, config, err)
		}
		changed = true
	}
	stale, err := s.ghosttyStale(dir, files)
	if err != nil {
		return s.failed(surfaceGhostty, config, err)
	}
	if stale > 0 {
		changed = true
	}
	processes, err := s.ghosttyRunning(ctx)
	note := ""
	if err == nil {
		note, err = s.ghosttyValidate(ctx, processes)
	}
	if err != nil {
		s.setDue(surfaceGhostty, true)
		return s.failed(surfaceGhostty, config, err)
	}
	if !s.reloadDue(surfaceGhostty, changed) {
		return s.result(surfaceGhostty, VerdictApplied, config, note)
	}
	if len(processes) == 0 {
		deferred := ghosttyDeferred
		if note != "" {
			deferred += "; " + note
		}
		return s.settle(surfaceGhostty, config, deferred, nil)
	}
	return s.settle(surfaceGhostty, config, "", s.ghosttySignal(ctx, processes))
}

// ghosttyStale deletes pfm-theme/ files of a palette no longer in force and reports how many.
func (s *session) ghosttyStale(dir string, wanted map[string]string) (int, error) {
	themes := filepath.Join(dir, ghosttyThemeDir)
	entries, err := os.ReadDir(themes)
	if err != nil {
		return 0, fmt.Errorf("themesync: list %s: %w", themes, err)
	}
	removed := 0
	var failures []error
	for _, entry := range entries {
		path := filepath.Join(themes, entry.Name())
		if _, keep := wanted[path]; keep {
			continue
		}
		if err := os.Remove(path); err != nil {
			failures = append(failures, fmt.Errorf("themesync: remove stale %s: %w", path, err))
			continue
		}
		removed++
	}
	return removed, errors.Join(failures...)
}

func (s *session) readGhostty(ctx context.Context) SurfaceResult {
	config, candidates, err := s.ghosttyConfig()
	if err != nil {
		return s.failed(surfaceGhostty, "", err)
	}
	if config == "" && s.state.Ghostty == nil {
		return s.ghosttyAbsent(candidates)
	}
	if config == "" {
		return s.in(surfaceGhostty, VerdictDrift, "", "config file is gone: "+strings.Join(candidates, ", "), 0)
	}
	dir := filepath.Dir(config)
	user, err := os.ReadFile(config)
	if err != nil {
		return s.failed(surfaceGhostty, config, fmt.Errorf("themesync: read %s: %w", config, err))
	}
	if !strings.Contains(string(user), strings.TrimSuffix(ghosttyIncludeLine(dir), "\n")) {
		return s.in(surfaceGhostty, VerdictDrift, config, "include line missing from "+config, 0)
	}
	for path, want := range ghosttyRender(s.palette, s.state.Mode, s.state.Generation, dir) {
		got, readErr := os.ReadFile(path)
		if readErr != nil || string(got) != want {
			return s.in(surfaceGhostty, VerdictDrift, config, filepath.Base(path)+" differs from the render", 0)
		}
	}
	processes, err := s.ghosttyRunning(ctx)
	note := ""
	if err == nil {
		note, err = s.ghosttyValidate(ctx, processes)
	}
	if err != nil {
		return s.failed(surfaceGhostty, config, err)
	}
	return s.in(surfaceGhostty, VerdictInSync, config, note, s.state.Generation)
}

func (s *session) restoreGhostty(ctx context.Context) SurfaceResult {
	snapshot := s.state.Ghostty
	if snapshot == nil {
		config, _, _ := s.ghosttyConfig() // the row's path only: nothing was applied either way
		return s.absent(surfaceGhostty, config, "nothing applied")
	}
	dir := filepath.Dir(snapshot.Config)
	current, err := os.ReadFile(snapshot.Config)
	if err != nil {
		return s.failed(surfaceGhostty, snapshot.Config, fmt.Errorf("themesync: read %s: %w", snapshot.Config, err))
	}
	if err := s.replaceFile(snapshot.Config, []byte(ghosttyExclude(string(current), snapshot.Added, dir))); err != nil {
		return s.failed(surfaceGhostty, snapshot.Config, err)
	}
	for _, owned := range []string{filepath.Join(dir, ghosttyIncludeFile), filepath.Join(dir, ghosttyThemeDir)} {
		if err := os.RemoveAll(owned); err != nil {
			return s.failed(surfaceGhostty, snapshot.Config, fmt.Errorf("themesync: remove %s: %w", owned, err))
		}
	}
	s.state.Ghostty = nil
	processes, err := s.ghosttyRunning(ctx)
	if err == nil {
		err = s.ghosttySignal(ctx, processes)
	}
	if err != nil {
		return s.failed(surfaceGhostty, snapshot.Config, err)
	}
	return s.result(surfaceGhostty, VerdictRestored, snapshot.Config, "")
}
