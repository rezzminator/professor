package installer

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/themesync"
)

// themeProfileKeyPrefix keys a profile's whole copy in the ownership ledger,
// beside the record of the Claude theme file under the bare name.
const themeProfileKeyPrefix = "profile:"

type themeAction int

const (
	themeUnchanged themeAction = iota // the file already holds the bytes and the ledger owns it
	themeAdopt                        // an unowned file holds exactly the bytes: record it, write nothing
	themeWrite                        // the file is absent or owned and unmodified: write it
)

// themeRemoval is the wording of one ledger removal, shared by uninstall and
// by retiring a theme install no longer ships (its theme directory gone).
type themeRemoval struct {
	verb     string // the change line: "{verb} theme {label} {path}"
	phase    string // the failure lines: "theme {label} {phase} failed: …"
	modified string // why a modified file stays: "theme {label} {modified}; left in place …"
}

var (
	themeUninstall = themeRemoval{verb: "remove", phase: "uninstall", modified: "locally modified"}
	themeRetire    = themeRemoval{
		verb: "retire", phase: "retire", modified: "no longer shipped and locally modified",
	}
)

// themeFile is one file install writes for a theme: the Claude theme file or
// pfm's whole copy of the profile.
type themeFile struct {
	key     string // the ownership ledger key
	label   string // names the file in a report: empty for the Claude theme file
	path    string
	content []byte

	// state read by inspect, decision made by settle
	existing []byte
	exists   bool
	record   themeOwnershipRecord
	owned    bool
	action   themeAction
}

func profileLedgerKey(name string) string { return themeProfileKeyPrefix + name }

// themeLedgerOwner splits a ledger key into the theme it belongs to and
// whether it records the profile copy rather than the Claude theme file.
func themeLedgerOwner(key string) (string, bool) {
	if name, isProfile := strings.CutPrefix(key, themeProfileKeyPrefix); isProfile {
		return name, true
	}
	return key, false
}

// themeLedgerLabel is a ledger key as a report names it.
func themeLedgerLabel(key string) string {
	if name, isProfile := themeLedgerOwner(key); isProfile {
		return name + " profile copy"
	}
	return key
}

// validateThemeName refuses a theme name that could leave its directory (the
// profile copy is named after it) or collide with a ledger key.
func validateThemeName(name string) error {
	if name == "." || name == ".." || strings.ContainsAny(name, `/\:`) {
		return errors.New("name must be a bare profile name: no path separator or ':'")
	}
	return nil
}

// themeFiles is what install writes for one theme, content its assembled
// profile document (loadThemeContent): the Claude file is the palette alone
// and the profile copy keeps every byte; a profile that does not parse is an
// error naming its theme.
func themeFiles(home, name string, claude themeFile, content []byte) ([]themeFile, error) {
	profile, err := themesync.ParseProfile(content)
	if err != nil {
		return nil, fmt.Errorf("profile %s rejected: %w", name, err)
	}
	palette, err := profile.ClaudeFile(content)
	if err != nil {
		return nil, fmt.Errorf("profile %s rejected: %w", name, err)
	}
	claude.content = palette
	return []themeFile{claude, {
		key:     profileLedgerKey(name),
		label:   " profile copy",
		path:    themesync.ProfilePath(home, name),
		content: content,
	}}, nil
}

// inspect reads the file against the ledger and returns the skip line when
// install must leave the theme alone: an unreadable file, a ledger path that
// drifted from the install's, or an owned file modified since install.
func (file *themeFile) inspect(name string, ownership map[string]themeOwnershipRecord) string {
	existing, exists, err := readOptionalRegularFile(file.path)
	if err != nil {
		return "theme " + name + file.label + " inspect failed: " + err.Error()
	}
	file.existing, file.exists = existing, exists
	file.record, file.owned = ownership[file.key]
	if !file.owned {
		return ""
	}
	if filepath.Clean(file.record.Path) != filepath.Clean(file.path) {
		return fmt.Sprintf(
			"theme %s%s ownership target drift: ledger=%s install=%s", name, file.label, file.record.Path, file.path,
		)
	}
	if file.exists && contentSHA256(file.existing) != file.record.SHA256 {
		return "theme " + name + file.label + " locally modified; left in place at " + file.path
	}
	return ""
}

// settle decides an inspected file's action, or returns the skip line for an
// unowned file that differs from what install would write.
func (file *themeFile) settle(name string) string {
	switch {
	case !file.exists:
		file.action = themeWrite
	case bytes.Equal(file.existing, file.content) && file.owned:
		file.action = themeUnchanged
	case bytes.Equal(file.existing, file.content):
		file.action = themeAdopt
	case !file.owned:
		return "theme " + name + file.label + " already exists but is not installer-owned; left in place at " + file.path
	default:
		file.action = themeWrite
	}
	return ""
}

// installThemeFiles writes one theme's files and their ledger records as a
// unit: a skip on any file leaves them all, a failure rolls the writes back.
// It returns the ledger as it stands afterwards.
func (installer *engine) installThemeFiles(
	name, ownershipPath string,
	ownership map[string]themeOwnershipRecord,
	files []themeFile,
) map[string]themeOwnershipRecord {
	pending := false
	for index := range files {
		file := &files[index]
		if skip := file.inspect(name, ownership); skip != "" {
			installer.skip(skip)
			return ownership
		}
		if skip := file.settle(name); skip != "" {
			installer.skip(skip)
			return ownership
		}
		pending = pending || file.action != themeUnchanged
	}
	if !pending {
		installer.ok("theme " + name + " unchanged at " + files[0].path)
		return ownership
	}
	next := cloneThemeOwnership(ownership)
	if err := writeThemeFiles(ownershipPath, next, files); err != nil {
		installer.skip("theme " + name + " install failed: " + err.Error())
		return ownership
	}
	for index := range files {
		file := &files[index]
		switch file.action {
		case themeWrite:
			if changeErr := installer.change("write theme "+name+file.label+" -> "+file.path, nil); changeErr != nil {
				installer.skip("theme " + name + " report failed after install: " + changeErr.Error())
			}
		case themeAdopt:
			installer.ok("theme " + name + file.label + " adopted: " + file.path +
				" is byte-identical to the install and is now installer-owned")
		case themeUnchanged:
		}
	}
	return next
}

// writeThemeFiles writes every file whose action is themeWrite, records each
// changed file in next, and saves the ledger; any failure puts the previous
// bytes back.
func writeThemeFiles(ownershipPath string, next map[string]themeOwnershipRecord, files []themeFile) error {
	var written []*themeFile
	for index := range files {
		file := &files[index]
		if file.action == themeWrite {
			if err := atomicfile.Write(file.path, file.content, 0o644); err != nil {
				return rollbackThemeFiles(written, fmt.Errorf("write %s: %w", file.path, err))
			}
			written = append(written, file)
		}
		if file.action != themeUnchanged {
			next[file.key] = themeOwnershipRecord{Path: file.path, SHA256: contentSHA256(file.content)}
		}
	}
	if err := writeThemeOwnership(ownershipPath, next); err != nil {
		return rollbackThemeFiles(written, fmt.Errorf("record ownership: %w", err))
	}
	return nil
}

func rollbackThemeFiles(written []*themeFile, cause error) error {
	var failures []error
	for index := len(written) - 1; index >= 0; index-- {
		if err := rollbackTheme(written[index].path, written[index].existing, written[index].exists); err != nil {
			failures = append(failures, err)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("%w; rollback failed: %v", cause, errors.Join(failures...))
	}
	return cause
}

// retireThemes removes what the ledger owns for a theme no profile in
// profiles ships, whatever once installed it: the file when it is still the
// bytes install wrote and no account selects it, else it stays and is
// reported. It returns the ledger as it stands afterwards.
func (installer *engine) retireThemes(
	ownershipPath string,
	profiles map[string]themeProfile,
	ownership map[string]themeOwnershipRecord,
) map[string]themeOwnershipRecord {
	for _, key := range sortedThemeOwnershipNames(ownership) {
		name, _ := themeLedgerOwner(key)
		if _, listed := profiles[name]; listed {
			continue
		}
		if installer.retireBlockedBySelection(key, ownership[key].Path) {
			continue
		}
		ownership = installer.removeOwnedTheme(ownershipPath, ownership, key, themeRetire)
	}
	return ownership
}

// retireBlockedBySelection reports, and so keeps, a retiring theme file some
// account's settings.json still selects (`custom:{stem}`): deleting it would
// leave that account on a theme that no longer exists. An account it cannot
// read keeps the file too, reported. true when the file stays.
func (installer *engine) retireBlockedBySelection(key, path string) bool {
	label := themeLedgerLabel(key)
	stem := strings.TrimSuffix(filepath.Base(path), ".json")
	selecting, err := themesync.AccountsSelecting(installer.options.Home, themesync.CustomValue(stem))
	if err != nil {
		installer.skip("theme " + label + " retire check failed, left in place: " + err.Error())
		return true
	}
	if len(selecting) == 0 {
		return false
	}
	installer.skip(
		"theme " + label + " is no longer shipped but " + strings.Join(selecting, ", ") +
			" still selects " + themesync.CustomValue(stem) +
			"; left in place: select another theme with `pfm theme use`, then install again to retire it",
	)
	return true
}

// removeOwnedTheme removes one ledger record and its file. A file modified
// since install stays, its record retained for recovery; a file already gone
// just drops the record. It returns the ledger as it stands afterwards.
func (installer *engine) removeOwnedTheme(
	ownershipPath string,
	ownership map[string]themeOwnershipRecord,
	key string,
	removal themeRemoval,
) map[string]themeOwnershipRecord {
	label := themeLedgerLabel(key)
	record := ownership[key]
	content, exists, readErr := readOptionalRegularFile(record.Path)
	if readErr != nil {
		installer.skip("theme " + label + " " + removal.phase + " inspect failed: " + readErr.Error())
		return ownership
	}
	if exists && contentSHA256(content) != record.SHA256 {
		installer.skip(
			"theme " + label + " " + removal.modified + "; left in place and retained recovery ownership at " + record.Path,
		)
		return ownership
	}
	message := removal.verb + " theme " + label + " " + record.Path
	if !installer.apply {
		if changeErr := installer.change(message, nil); changeErr != nil {
			installer.skip("theme " + label + " " + removal.phase + " preview failed: " + changeErr.Error())
		}
		return ownership
	}

	next := cloneThemeOwnership(ownership)
	delete(next, key)
	if exists {
		if removeErr := os.Remove(record.Path); removeErr != nil {
			installer.skip(
				"theme " + label + " " + removal.phase + " failed: remove " + record.Path + ": " + removeErr.Error(),
			)
			return ownership
		}
	}
	if ledgerErr := writeThemeOwnership(ownershipPath, next); ledgerErr != nil {
		var rollbackErr error
		if exists {
			rollbackErr = atomicfile.Write(record.Path, content, 0o644)
		}
		failure := "theme " + label + " " + removal.phase + " failed: update ownership: " + ledgerErr.Error()
		if rollbackErr != nil {
			failure += "; rollback failed: " + rollbackErr.Error()
		}
		installer.skip(failure)
		return ownership
	}
	if changeErr := installer.change(message, nil); changeErr != nil {
		installer.skip("theme " + label + " " + removal.phase + " report failed: " + changeErr.Error())
	}
	return next
}

// SourceProfiles are the theme directories a source clone ships, the ones
// `pfm install` discovers: every subdirectory of its templates/themes/, as
// absolute paths, sorted.
func SourceProfiles(sourceRepo string) ([]string, error) {
	directory := filepath.Join(sourceRepo, filepath.FromSlash(themeProfilesRelative))
	names, err := localProfileDirs(directory)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(names))
	for _, name := range names {
		paths = append(paths, filepath.Join(directory, name))
	}
	return paths, nil
}

// CheckProfile runs on one theme directory every check `pfm install` runs on
// it before writing anything — the theme name, the directory layout and each
// file's canonical form (themesync.ReadProfileDir), the profile schema (every
// unknown key refused by name), the Claude palette and the VS Code theme
// rendered from it — then the one install does not run, the rendered theme's
// readability (themesync.CheckVSCodeReadability), and returns that VS Code
// theme. The error opens "theme {dir}: ", as ReadProfileDir's does.
func CheckProfile(dir string) (themesync.VSCodeThemeFile, error) {
	name := filepath.Base(dir)
	if err := validateThemeName(name); err != nil {
		return themesync.VSCodeThemeFile{}, fmt.Errorf("theme %s: %w", dir, err)
	}
	content, err := themesync.ReadProfileDir(dir)
	if err != nil {
		return themesync.VSCodeThemeFile{}, err
	}
	if _, err := themeFiles("", name, themeFile{}, content); err != nil {
		return themesync.VSCodeThemeFile{}, fmt.Errorf("theme %s: %w", dir, err)
	}
	rendered, err := themesync.RenderVSCodeTheme(name, content)
	if err != nil {
		return themesync.VSCodeThemeFile{}, fmt.Errorf("theme %s: %w", dir, err)
	}
	if err := themesync.CheckVSCodeReadability(rendered.Content); err != nil {
		return themesync.VSCodeThemeFile{}, fmt.Errorf("theme %s: %w", dir, err)
	}
	return rendered, nil
}
