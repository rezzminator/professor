package themesync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// A theme is one directory, templates/themes/{name}/, of a fixed file set
// (ProfileFiles): every theme holds every file, `{}` when it sets
// nothing there, and nothing else. Each file holds one part of the profile:
const (
	profileThemeFile     = "theme.json"           // name, base, vscodeSemanticHighlighting
	profileClaudeFile    = "claude.json"          // overrides: Claude Code's palette
	profileTerminalFile  = "terminal.json"        // surfaces.vscode.colors: the terminal pins
	profileTmuxFile      = "tmux.json"            // surfaces.tmux
	profileHerdrFile     = "herdr.json"           // surfaces.herdr: custom tokens and tabGlyphs
	profileChromeFile    = "vscode/chrome.json"   // surfaces.vscode.chrome
	profileSyntaxDir     = "vscode/syntax"        // surfaces.vscode.theme.tokenColors, one file per syntaxFamilies family
	profileSemanticFile  = "vscode/semantic.json" // surfaces.vscode.theme.semanticTokenColors
	profileWorkbenchDir  = "vscode/workbench"     // surfaces.vscode.theme.colors, one file per workbenchGroups group
	profileCanonicalHint = "keys sorted, two-space indent, one trailing newline (jq -S --indent 2 . writes it)"
)

// profileMeta is theme.json. VSCodeSemanticHighlighting is
// surfaces.vscode.theme.semanticHighlighting, omitted when unset.
type profileMeta struct {
	Name                       string `json:"name"`
	Base                       string `json:"base,omitempty"`
	VSCodeSemanticHighlighting *bool  `json:"vscodeSemanticHighlighting,omitempty"`
}

// ProfileFiles is a theme directory's file set, slash-separated paths
// relative to it, sorted: the same for every theme.
func ProfileFiles() []string {
	files := []string{
		profileThemeFile, profileClaudeFile, profileTerminalFile, profileTmuxFile, profileHerdrFile,
		profileChromeFile, profileSemanticFile,
	}
	for _, group := range workbenchGroupNames() {
		files = append(files, workbenchFile(group))
	}
	for _, family := range syntaxFamilyNames() {
		files = append(files, syntaxFile(family))
	}
	slices.Sort(files)
	return files
}

func workbenchFile(group string) string { return profileWorkbenchDir + "/" + group + ".json" }

// ReadProfileDir reads the theme directory dir: exactly ProfileFiles, every
// one a regular file and no other entry (a missing or extra file is an
// error), assembled by AssembleProfile.
func ReadProfileDir(dir string) ([]byte, error) {
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("theme %s: %w", dir, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("theme %s: not a directory: a theme is a directory of %s", dir, profileThemeFile)
	}
	want := map[string]bool{}
	dirs := map[string]bool{}
	for _, file := range ProfileFiles() {
		want[file] = true
		for parent := path.Dir(file); parent != "."; parent = path.Dir(parent) {
			dirs[parent] = true
		}
	}
	found := map[string]bool{}
	var extra []string
	walkErr := filepath.WalkDir(dir, func(at string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, relErr := filepath.Rel(dir, at)
		if relErr != nil {
			return relErr
		}
		relative = filepath.ToSlash(relative)
		switch {
		case relative == ".":
		case entry.IsDir() && dirs[relative]:
		case entry.Type().IsRegular() && want[relative]:
			found[relative] = true
		case entry.IsDir():
			extra = append(extra, relative+"/")
			return fs.SkipDir
		default:
			extra = append(extra, relative)
		}
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("theme %s: list: %w", dir, walkErr)
	}
	var missing []string
	for _, file := range ProfileFiles() {
		if !found[file] {
			missing = append(missing, file)
		}
	}
	if len(missing) > 0 || len(extra) > 0 {
		return nil, fmt.Errorf(
			"theme %s: the file set differs from every theme's (ProfileFiles): missing [%s], extra [%s]",
			dir,
			strings.Join(missing, ", "),
			strings.Join(extra, ", "),
		)
	}
	content, err := AssembleProfile(func(file string) ([]byte, error) {
		return os.ReadFile(filepath.Join(dir, filepath.FromSlash(file)))
	})
	if err != nil {
		return nil, fmt.Errorf("theme %s: %w", dir, err)
	}
	return content, nil
}

// AssembleProfile reads every ProfileFiles file through read and returns the
// profile they hold as one document, the form ParseProfile reads and pfm
// install copies: each file strictly decoded, each workbench colour in its
// group's file, each tokenColors rule in its family's file under its ordinal
// (joinSyntax), each file byte-identical to its canonical form
// (themeDirFiles), and the whole profile valid (ParseProfile). The error
// names the file.
func AssembleProfile(read func(file string) ([]byte, error)) ([]byte, error) {
	files := map[string][]byte{}
	for _, file := range ProfileFiles() {
		content, err := read(file)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		if err := checkCanonicalJSON(content); err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		files[file] = content
	}
	profile, err := assembleParts(files)
	if err != nil {
		return nil, err
	}
	document, err := encodeIndented(profile)
	if err != nil {
		return nil, fmt.Errorf("encode profile %s: %w", profile.Name, err)
	}
	if _, err := ParseProfile(document); err != nil {
		return nil, err
	}
	canonical, err := themeDirFiles(profile)
	if err != nil {
		return nil, err
	}
	for _, file := range ProfileFiles() {
		if !bytes.Equal(files[file], canonical[file]) {
			return nil, fmt.Errorf("%s: differs from its canonical form at line %d: want %q",
				file, firstDifferentLine(files[file], canonical[file]), canonicalLine(canonical[file], files[file]))
		}
	}
	return document, nil
}

// assembleParts decodes each file into its part of the profile. A part left
// empty stays nil, as decoding a profile without that key leaves it.
func assembleParts(files map[string][]byte) (Profile, error) {
	var meta profileMeta
	if err := decodeProfilePart(files[profileThemeFile], &meta); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", profileThemeFile, err)
	}
	if meta.Name == "" {
		return Profile{}, fmt.Errorf("%s: no name", profileThemeFile)
	}
	if err := validatePalette(Profile{Base: meta.Base}); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", profileThemeFile, err)
	}
	profile := Profile{Name: meta.Name, Base: meta.Base, Overrides: map[string]string{}}
	if err := decodeProfilePart(files[profileClaudeFile], &profile.Overrides); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", profileClaudeFile, err)
	}
	if err := validatePalette(profile); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", profileClaudeFile, err)
	}
	pins := SurfacePins{}
	vscode := VSCodePins{}
	theme := VSCodeTheme{SemanticHighlighting: meta.VSCodeSemanticHighlighting}
	var herdrPart HerdrPins
	for file, target := range map[string]any{
		profileTerminalFile: &vscode.Colors, profileChromeFile: &vscode.Chrome, profileTmuxFile: &pins.Tmux,
		profileHerdrFile: &herdrPart, profileSemanticFile: &theme.SemanticTokenColors,
	} {
		if err := decodeProfilePart(files[file], target); err != nil {
			return Profile{}, fmt.Errorf("%s: %w", file, err)
		}
	}
	for _, part := range []struct {
		file string
		pins SurfacePins
	}{
		{profileTerminalFile, SurfacePins{VSCode: &VSCodePins{Colors: vscode.Colors}}},
		{profileChromeFile, SurfacePins{VSCode: &VSCodePins{Chrome: vscode.Chrome}}},
		{profileTmuxFile, SurfacePins{Tmux: pins.Tmux}},
		{profileHerdrFile, SurfacePins{Herdr: &herdrPart}},
		{profileSemanticFile, SurfacePins{VSCode: &VSCodePins{Theme: &theme}}},
	} {
		if err := validatePins(&part.pins); err != nil {
			return Profile{}, fmt.Errorf("%s: %w", part.file, err)
		}
	}
	families := map[string]map[string]VSCodeTokenColor{}
	for _, family := range syntaxFamilyNames() {
		file := syntaxFile(family)
		var rules map[string]VSCodeTokenColor
		if err := decodeProfilePart(files[file], &rules); err != nil {
			return Profile{}, fmt.Errorf("%s: %w", file, err)
		}
		for _, ordinal := range slices.Sorted(maps.Keys(rules)) {
			if err := validateVSCodeTheme(&VSCodeTheme{TokenColors: []VSCodeTokenColor{rules[ordinal]}}); err != nil {
				return Profile{}, fmt.Errorf("%s: rule %q: %w", file, ordinal, err)
			}
		}
		families[family] = rules
	}
	tokenColors, err := joinSyntax(families)
	if err != nil {
		return Profile{}, err
	}
	theme.TokenColors = tokenColors
	for _, group := range workbenchGroupNames() {
		file := workbenchFile(group)
		var colors map[string]string
		if err := decodeProfilePart(files[file], &colors); err != nil {
			return Profile{}, fmt.Errorf("%s: %w", file, err)
		}
		for _, id := range slices.Sorted(maps.Keys(colors)) {
			owner, err := workbenchGroup(id)
			if err != nil {
				return Profile{}, fmt.Errorf("%s: %w", file, err)
			}
			if owner != group {
				return Profile{}, fmt.Errorf("%s: colour %q belongs in %s", file, id, workbenchFile(owner))
			}
			if theme.Colors == nil {
				theme.Colors = map[string]string{}
			}
			theme.Colors[id] = colors[id]
		}
		if err := validateVSCodeTheme(&VSCodeTheme{Colors: colors}); err != nil {
			return Profile{}, fmt.Errorf("%s: %w", file, err)
		}
	}
	vscode.Colors, vscode.Chrome = nilIfEmpty(vscode.Colors), nilIfEmpty(vscode.Chrome)
	theme.TokenColors = nilIfEmptySlice(theme.TokenColors)
	if len(theme.SemanticTokenColors) == 0 {
		theme.SemanticTokenColors = nil
	}
	if len(theme.Colors) > 0 || theme.TokenColors != nil || theme.SemanticTokenColors != nil ||
		theme.SemanticHighlighting != nil {
		vscode.Theme = &theme
	}
	if vscode.Colors != nil || vscode.Chrome != nil || vscode.Theme != nil {
		pins.VSCode = &vscode
	}
	pins.Tmux = nilIfEmpty(pins.Tmux)
	if herdrPart.Tokens != nil || herdrPart.TabGlyphs != nil {
		pins.Herdr = &herdrPart
	}
	if pins.Tmux != nil || pins.Herdr != nil || pins.VSCode != nil {
		profile.Surfaces = &pins
	}
	return profile, nil
}

// SplitProfile is the theme directory holding the profile document content:
// every ProfileFiles file, keyed by its slash-separated path, in canonical
// form. AssembleProfile of it is content re-encoded; a profile that does not
// parse is an error.
func SplitProfile(content []byte) (map[string][]byte, error) {
	profile, err := ParseProfile(content)
	if err != nil {
		return nil, err
	}
	return themeDirFiles(profile)
}

// themeDirFiles is profile as its directory's files, each in canonical form
// (canonicalJSON): what AssembleProfile holds every file to. A semantic style
// that sets a foreground alone is written as the bare colour.
func themeDirFiles(profile Profile) (map[string][]byte, error) {
	pins := SurfacePins{}
	if profile.Surfaces != nil {
		pins = *profile.Surfaces
	}
	vscode, theme, herdrPart := VSCodePins{}, VSCodeTheme{}, HerdrPins{}
	if pins.VSCode != nil {
		vscode = *pins.VSCode
	}
	if vscode.Theme != nil {
		theme = *vscode.Theme
	}
	if pins.Herdr != nil {
		herdrPart = *pins.Herdr
	}
	semantic := map[string]any{}
	for selector, style := range theme.SemanticTokenColors {
		if style == (VSCodeSemanticStyle{Foreground: style.Foreground}) {
			semantic[selector] = style.Foreground
			continue
		}
		semantic[selector] = style
	}
	families, err := splitSyntax(theme.TokenColors)
	if err != nil {
		return nil, err
	}
	parts := map[string]any{
		profileThemeFile: profileMeta{
			Name: profile.Name, Base: profile.Base, VSCodeSemanticHighlighting: theme.SemanticHighlighting,
		},
		profileClaudeFile:   emptyIfNil(profile.Overrides),
		profileTerminalFile: emptyIfNil(vscode.Colors),
		profileChromeFile:   emptyIfNil(vscode.Chrome),
		profileTmuxFile:     emptyIfNil(pins.Tmux),
		profileHerdrFile:    herdrPart,
		profileSemanticFile: semantic,
	}
	for family, rules := range families {
		parts[syntaxFile(family)] = rules
	}
	groups := map[string]map[string]string{}
	for _, group := range workbenchGroupNames() {
		groups[group] = map[string]string{}
		parts[workbenchFile(group)] = groups[group]
	}
	for id, colour := range theme.Colors {
		group, err := workbenchGroup(id)
		if err != nil {
			return nil, err
		}
		groups[group][id] = colour
	}
	files := make(map[string][]byte, len(parts))
	for file, part := range parts {
		content, err := json.Marshal(part)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", file, err)
		}
		if files[file], err = canonicalJSON(content); err != nil {
			return nil, fmt.Errorf("encode %s: %w", file, err)
		}
	}
	return files, nil
}

// canonicalJSON is content's canonical form: every object's keys sorted,
// two-space indent, one trailing newline, numbers as written.
func canonicalJSON(content []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("data after the top-level value")
	}
	return encodeIndented(value)
}

// checkCanonicalJSON refuses a file that is not JSON or not in its canonical
// form, naming the first line that differs.
func checkCanonicalJSON(content []byte) error {
	canonical, err := canonicalJSON(content)
	if err != nil {
		return fmt.Errorf("not JSON: %w", err)
	}
	if !bytes.Equal(content, canonical) {
		return fmt.Errorf("not in canonical form, %s: line %d should read %q", profileCanonicalHint,
			firstDifferentLine(content, canonical), canonicalLine(canonical, content))
	}
	return nil
}

// decodeProfilePart decodes one theme file into target: an unknown key or trailing data
// is an error.
func decodeProfilePart(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return errors.New("data after the top-level value")
	}
	return nil
}

// firstDifferentLine is the 1-based number of the first line where got and
// want differ.
func firstDifferentLine(got, want []byte) int {
	gotLines, wantLines := bytes.Split(got, []byte("\n")), bytes.Split(want, []byte("\n"))
	for index := range max(len(gotLines), len(wantLines)) {
		if index >= len(gotLines) || index >= len(wantLines) || !bytes.Equal(gotLines[index], wantLines[index]) {
			return index + 1
		}
	}
	return len(gotLines)
}

// canonicalLine is want's line where got first differs from it, "" past its end.
func canonicalLine(want, got []byte) string {
	lines := strings.Split(string(want), "\n")
	if index := firstDifferentLine(got, want) - 1; index < len(lines) {
		return lines[index]
	}
	return ""
}

func nilIfEmpty(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	return values
}

func nilIfEmptySlice(rules []VSCodeTokenColor) []VSCodeTokenColor {
	if len(rules) == 0 {
		return nil
	}
	return rules
}

func emptyIfNil(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	return values
}
