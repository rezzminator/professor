package themesync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

const (
	herdrBlockOpen  = "# >>> pfm theme"
	herdrBlockClose = "# <<< pfm theme <<<"
	herdrThemeTable = "theme"
	herdrDeferred   = "applies when Herdr starts"
)

var (
	herdrOpenPattern   = regexp.MustCompile(`^# >>> pfm theme · \S+ · gen (\d+) >>>$`)
	herdrHeaderPattern = regexp.MustCompile(`^\s*\[\[?\s*([^\]\s][^\]]*?)\s*\]\]?\s*(#.*)?$`)
	herdrRootTheme     = regexp.MustCompile(`^\s*theme\s*[.=]`)
	// herdrNotRunning matches the texts `herdr server reload-config` prints when no server is up.
	herdrNotRunning = []string{
		"not running", "no server", "no running", "connection refused", "no such file", "could not connect",
		"couldn't connect", "failed to connect",
	}
)

// herdrToken is one `[theme.custom]` token.
type herdrToken struct{ name, value string }

// Herdr's built-in themes the custom tokens layer onto, one per side.
const (
	herdrDarkName  = "tokyo-night"
	herdrLightName = "tokyo-night-day"
)

// herdrTokens maps a variant onto Herdr's 19 documented custom tokens (D11).
func (variant Variant) herdrTokens() []herdrToken {
	return []herdrToken{
		{"accent", variant.Accent},
		{"panel_bg", variant.Background},
		{"sidebar_bg", variant.Surface},
		{"active_row_bg", variant.SelectionBackground},
		{"selection_bg", variant.SelectionBackground},
		{"surface0", variant.Surface},
		{"surface1", variant.Overlay},
		{"surface_dim", variant.Surface},
		{"overlay0", variant.Muted},
		{"overlay1", variant.State.Idle},
		{"text", variant.Foreground},
		{"subtext0", variant.State.Idle},
		{"mauve", variant.ANSI[5]},
		{"green", variant.State.Idle},
		{"yellow", variant.State.Working},
		{"red", variant.State.Blocked},
		{"blue", variant.State.Done},
		{"teal", variant.State.Done},
		{"peach", variant.Orange},
	}
}

func herdrWriteTokens(out *strings.Builder, table string, variant Variant) {
	out.WriteString("[" + table + "]\n")
	for _, token := range variant.herdrTokens() {
		fmt.Fprintf(out, "%s = %q\n", token.name, token.value)
	}
}

// herdrRender is the marker-fenced block that replaces the user's [theme]
// tables: name / auto_switch (an auto source) / dark_name / light_name and
// every custom token, the primary variant in [theme.custom], an auto source's
// light one in [theme.custom.light].
func herdrRender(applied Applied, generation int) string {
	var out strings.Builder
	fmt.Fprintf(&out, "%s · %s · gen %d >>>\n[%s]\n", herdrBlockOpen,
		strings.Join(strings.Fields(applied.Value), "-"), generation, herdrThemeTable)
	switch {
	case applied.Auto && applied.Alternate != nil:
		fmt.Fprintf(&out, "name = %q\nauto_switch = true\ndark_name = %q\nlight_name = %q\n\n",
			herdrDarkName, herdrDarkName, herdrLightName)
		herdrWriteTokens(&out, "theme.custom", applied.Primary)
		out.WriteString("\n")
		herdrWriteTokens(&out, "theme.custom.light", *applied.Alternate)
	default:
		name := herdrDarkName
		if applied.Side == Light {
			name = herdrLightName
		}
		fmt.Fprintf(&out, "name = %q\nauto_switch = false\n\n", name)
		herdrWriteTokens(&out, "theme.custom", applied.Primary)
	}
	out.WriteString(herdrBlockClose + "\n")
	return out.String()
}

// herdrLine is one line with its byte range [start, end) in the file, newline included.
type herdrLine struct {
	start, end int
	text       string
}

func herdrLines(content string) []herdrLine {
	var lines []herdrLine
	for start := 0; start < len(content); {
		end := len(content)
		if newline := strings.IndexByte(content[start:], '\n'); newline >= 0 {
			end = start + newline + 1
		}
		lines = append(lines, herdrLine{start: start, end: end, text: strings.TrimRight(content[start:end], "\r\n")})
		start = end
	}
	return lines
}

// herdrBlockRange finds the first pfm block's byte range, markers included.
func herdrBlockRange(content string) (start, end int, found bool) {
	open := -1
	lines := herdrLines(content)
	for index, line := range lines {
		if open < 0 && strings.HasPrefix(line.text, herdrBlockOpen) {
			open = index
		} else if open >= 0 && line.text == herdrBlockClose {
			return lines[open].start, line.end, true
		}
	}
	return 0, 0, false
}

// herdrExtract returns the pfm block and the generation its header carries.
func herdrExtract(content string) (block string, generation int, found bool) {
	start, end, found := herdrBlockRange(content)
	if !found {
		return "", 0, false
	}
	block = content[start:end]
	header, _, _ := strings.Cut(block, "\n")
	if match := herdrOpenPattern.FindStringSubmatch(header); match != nil {
		generation, _ = strconv.Atoi(match[1])
	}
	return block, generation, true
}

// herdrReplace swaps an existing pfm block for a new one in place.
func herdrReplace(content, block string) (string, bool) {
	start, end, found := herdrBlockRange(content)
	if !found {
		return content, false
	}
	return content[:start] + block + content[end:], true
}

func herdrThemeHeader(text string) bool {
	match := herdrHeaderPattern.FindStringSubmatch(text)
	return len(match) > 1 && (match[1] == herdrThemeTable || strings.HasPrefix(match[1], herdrThemeTable+"."))
}

// herdrInstall puts the block where the user's theme tables are (the first
// [theme] header through the last [theme.*] section's last content line,
// trailing blank and comment lines excluded), or appends it when there is
// none; the snapshot is what herdrRemove needs to undo it byte for byte.
func herdrInstall(original, block string) (string, herdrSnapshot, error) {
	lines := herdrLines(original)
	var headers, themeHeaders []int
	for index, line := range lines {
		if herdrHeaderPattern.MatchString(line.text) {
			headers = append(headers, index)
			if herdrThemeHeader(line.text) {
				themeHeaders = append(themeHeaders, index)
			}
		} else if len(headers) == 0 && herdrRootTheme.MatchString(line.text) {
			return "", herdrSnapshot{}, fmt.Errorf("themesync: herdr config sets theme at the root (line %d: %q); "+
				"move it under a [theme] table", index+1, line.text)
		}
	}
	if len(themeHeaders) == 0 {
		prefix := ""
		switch {
		case original == "":
		case strings.HasSuffix(original, "\n"):
			prefix = "\n"
		default:
			prefix = "\n\n"
		}
		return original + prefix + block, herdrSnapshot{Added: prefix + block}, nil
	}
	first, last := themeHeaders[0], themeHeaders[len(themeHeaders)-1]
	for _, header := range headers {
		if header > first && header < last && !herdrThemeHeader(lines[header].text) {
			return "", herdrSnapshot{}, fmt.Errorf(
				"themesync: herdr config splits its theme tables around line %d (%q); "+
					"keep [theme] and [theme.*] together",
				header+1,
				lines[header].text,
			)
		}
	}
	endLine := len(lines) // exclusive index of the last line the span may hold
	for _, header := range headers {
		if header > last {
			endLine = header
			break
		}
	}
	for endLine-1 > last {
		trimmed := strings.TrimSpace(lines[endLine-1].text)
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			break
		}
		endLine--
	}
	start, end := lines[first].start, lines[endLine-1].end
	return original[:start] + block + original[end:], herdrSnapshot{Span: original[start:end]}, nil
}

// herdrRemove takes the pfm block out and puts the original back: the replaced
// span, or (when the block was appended) nothing plus the separator it added.
func herdrRemove(content string, snapshot herdrSnapshot) (string, bool) {
	start, end, found := herdrBlockRange(content)
	if !found {
		return content, false
	}
	if snapshot.Span == "" {
		prefix, _, _ := strings.Cut(snapshot.Added, herdrBlockOpen)
		if strings.HasSuffix(content[:start], prefix) {
			start -= len(prefix)
		}
	}
	return content[:start] + snapshot.Span + content[end:], true
}

func (s *session) herdrPath() string {
	return filepath.Join(s.env.Home, ".config", "herdr", "config.toml")
}

// herdrReload asks a running Herdr server to reread its config; a server that
// is not up is the "applies when Herdr starts" case, anything else an error.
func (s *session) herdrReload(ctx context.Context) (string, error) {
	binary, err := s.env.Runner.LookPath("herdr")
	if err != nil {
		return "herdr not on PATH; " + herdrDeferred, nil //nolint:nilerr // absence of the binary is the deferred case, not a failure
	}
	result, err := s.env.Runner.Run(ctx, []string{binary, "server", "reload-config"}, deps.RunOptions{})
	if err != nil {
		return "", fmt.Errorf("themesync: herdr server reload-config: %w", err)
	}
	if result.ExitCode == 0 {
		return "", nil
	}
	text := strings.TrimSpace(string(result.Stderr) + " " + string(result.Stdout))
	for _, marker := range herdrNotRunning {
		if strings.Contains(strings.ToLower(text), marker) {
			return herdrDeferred, nil
		}
	}
	return "", fmt.Errorf("themesync: herdr server reload-config exited %d: %s", result.ExitCode, text)
}

func (s *session) applyHerdr(ctx context.Context) SurfaceResult {
	path := s.herdrPath()
	original, err := os.ReadFile(path)
	missing := errors.Is(err, os.ErrNotExist)
	if missing {
		if _, lookErr := s.env.Runner.LookPath("herdr"); lookErr != nil {
			return s.absent(surfaceHerdr, path, "no config at "+path+" and herdr not on PATH")
		}
	} else if err != nil {
		return s.failed(surfaceHerdr, path, fmt.Errorf("themesync: read %s: %w", path, err))
	}
	block := herdrRender(s.theme(), s.state.Generation)
	updated, replaced := herdrReplace(string(original), block)
	if !replaced {
		var snapshot herdrSnapshot
		if updated, snapshot, err = herdrInstall(string(original), block); err != nil {
			return s.failed(surfaceHerdr, path, err)
		}
		snapshot.Created = missing
		if !missing {
			if snapshot.Backup, err = s.backupBeside(path, original); err != nil {
				return s.failed(surfaceHerdr, path, err)
			}
		}
		s.state.Herdr = &snapshot
		if err := s.persist(); err != nil {
			return s.failed(surfaceHerdr, path, err)
		}
	}
	changed := updated != string(original) || missing
	if changed {
		if err := s.replaceFile(path, []byte(updated)); err != nil {
			return s.failed(surfaceHerdr, path, err)
		}
	}
	if !s.reloadDue(surfaceHerdr, changed) {
		return s.result(surfaceHerdr, VerdictApplied, path, "")
	}
	note, err := s.herdrReload(ctx)
	return s.settle(surfaceHerdr, path, note, err)
}

func (s *session) readHerdr(_ context.Context) SurfaceResult {
	path := s.herdrPath()
	content, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist) && s.state.Herdr == nil:
		return s.absent(surfaceHerdr, path, "no config at "+path)
	case errors.Is(err, os.ErrNotExist):
		return s.in(surfaceHerdr, VerdictDrift, path, "config file is gone", 0)
	case err != nil:
		return s.failed(surfaceHerdr, path, fmt.Errorf("themesync: read %s: %w", path, err))
	}
	block, generation, found := herdrExtract(string(content))
	switch {
	case !found:
		return s.in(surfaceHerdr, VerdictDrift, path, "pfm block missing from "+path, 0)
	case block != herdrRender(s.theme(), generation) || generation != s.state.Generation:
		return s.in(
			surfaceHerdr,
			VerdictDrift,
			path,
			fmt.Sprintf(
				"block at generation %d differs from the render at generation %d",
				generation,
				s.state.Generation,
			),
			generation,
		)
	}
	return s.in(surfaceHerdr, VerdictInSync, path, "", generation)
}

func (s *session) restoreHerdr(ctx context.Context) SurfaceResult {
	path := s.herdrPath()
	snapshot := s.state.Herdr
	if snapshot == nil {
		return s.absent(surfaceHerdr, path, "nothing applied")
	}
	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return s.failed(surfaceHerdr, path, fmt.Errorf("themesync: read %s: %w", path, err))
	}
	restored, found := herdrRemove(string(content), *snapshot)
	detail := ""
	switch {
	case !found:
		detail = "pfm block already gone"
	case snapshot.Created && strings.TrimSpace(restored) == "":
		if err := s.removeFile(path); err != nil {
			return s.failed(surfaceHerdr, path, err)
		}
	default:
		if err := s.replaceFile(path, []byte(restored)); err != nil {
			return s.failed(surfaceHerdr, path, err)
		}
	}
	s.state.Herdr = nil
	if note, err := s.herdrReload(ctx); err != nil {
		return s.failed(surfaceHerdr, path, err)
	} else if note != "" {
		detail = strings.TrimSpace(detail + " " + note)
	}
	return s.result(surfaceHerdr, VerdictRestored, path, detail)
}
