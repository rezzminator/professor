package installer

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// claudePluginCopies is the subdirectory of a config dir's plugins dir holding Claude's plugin copies.
const claudePluginCopies = "cache"

// claudeRecordEdit is one record the take-back edits: its path in the config dir, the member
// holding the keys (the document itself when empty), which are pfm's, and whether an emptied member goes.
// unread, when set, makes a record that cannot be read or parsed a skip row ending in it, never a
// failure: a file whose only business for pfm is this edit.
type claudeRecordEdit struct {
	file, member string
	ours         func(key string) bool
	dropEmpty    bool
	unread       string
}

// claudePlugins is a config dir's plugins dir and installed_plugins.json's id-keyed member.
const claudePlugins = "plugins"

// claudePluginTake is what one take-back removes from a config dir: the keys
// its edits name, then the copies it lists, each only under one of within
// (subdirectories of the dir's plugins dir).
type claudePluginTake struct {
	edits  []claudeRecordEdit
	copies func(dir string) ([]string, error)
	within []string
}

// takeClaudePluginDir edits dir's records take names, then removes the copies
// it lists; a record that fails leaves every copy in place.
func (installer *engine) takeClaudePluginDir(dir string, seen map[string]bool, take claudePluginTake) error {
	copies, err := take.copies(dir)
	if err != nil {
		return err
	}
	for _, edit := range take.edits {
		err = errors.Join(err, installer.takeClaudeRecord(filepath.Join(dir, edit.file), edit, seen))
	}
	if err != nil {
		return err
	}
	plugins := paths.PhysicalPath(filepath.Join(dir, claudePlugins))
	for _, path := range copies {
		err = errors.Join(err, installer.removeClaudeCopy(plugins, take.within, path, seen))
	}
	return err
}

// takeClaudeRecord removes pfm's keys from the record at path through a
// symlinked file (writeSettingsDocument); an absent file or one holding none
// stays, and so does one that cannot be read when edit.unread is set, named.
func (installer *engine) takeClaudeRecord(path string, edit claudeRecordEdit, seen map[string]bool) error {
	physical := paths.PhysicalPath(path)
	if seen[physical] {
		return nil
	}
	seen[physical] = true
	raw, document, err := readSettingsForEdit(path)
	if err != nil && edit.unread != "" {
		installer.skip("claude plugins: " + err.Error() + " — " + edit.unread)
		return nil
	}
	if err != nil || document == nil {
		return err
	}
	taken := edit.take(document)
	if len(taken) == 0 {
		return nil
	}
	where := path
	if edit.member != "" {
		where = edit.member + " in " + path
	}
	return installer.change(
		"remove "+strings.Join(taken, ", ")+" from "+where,
		writeSettingsDocument(path, raw, document, func(fresh map[string]any) error {
			edit.take(fresh)
			return nil
		}),
	)
}

// held names pfm's keys in document, sorted, and the member holding them.
func (edit claudeRecordEdit) held(document map[string]any) ([]string, map[string]any) {
	holder := document
	if edit.member != "" {
		holder, _ = document[edit.member].(map[string]any)
	}
	var keys []string
	for key := range holder {
		if edit.ours(key) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, holder
}

// take deletes pfm's keys from document and names them, sorted.
func (edit claudeRecordEdit) take(document map[string]any) []string {
	taken, holder := edit.held(document)
	for _, key := range taken {
		delete(holder, key)
	}
	if edit.dropEmpty && holder != nil && len(holder) == 0 {
		delete(document, edit.member)
	}
	return taken
}

// removeClaudeCopy removes path, a copy a record names, when it lies under one of
// within, plugins' subdirectories; any other path is not Claude's copy and stays, named.
func (installer *engine) removeClaudeCopy(plugins string, within []string, path string, seen map[string]bool) error {
	if path == "" {
		return nil
	}
	physical := paths.PhysicalPath(path)
	if seen[physical] {
		return nil
	}
	seen[physical] = true
	if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
		return nil
	} else if err != nil {
		return err // the *PathError names path
	}
	roots := make([]string, len(within))
	for index, sub := range within {
		roots[index] = filepath.Join(plugins, sub)
	}
	if !slices.ContainsFunc(roots, func(root string) bool {
		return strings.HasPrefix(physical, root+string(filepath.Separator))
	}) {
		installer.skip(fmt.Sprintf(
			"claude plugin copy %s lies outside %s: not Claude's copy, kept", path, strings.Join(roots, " and "),
		))
		return nil
	}
	installer.markRemoved(path)
	return installer.change("remove "+path, func() error { return os.RemoveAll(path) })
}

// retiredPluginCopies lists each installPath of dir's records of ids, then per
// id Claude's cache dir {plugins}/cache/{marketplace}/{name} and its data dir
// {plugins}/data/{name}-{marketplace} (retiredPluginDataName), unless an id
// dir's install record keeps has that data dir's name too.
func retiredPluginCopies(dir string, ids []string) ([]string, error) {
	installs, err := claudeplugin.ReadInstalls(dir)
	if err != nil {
		return nil, err
	}
	var copies []string
	for _, id := range ids {
		for _, record := range installs[id] {
			copies = append(copies, record.InstallPath)
		}
	}
	sort.Strings(copies)
	for _, id := range ids {
		name, marketplace, _ := strings.Cut(id, "@") // validated by retired.ParseLists
		copies = append(copies, filepath.Join(dir, claudePlugins, claudePluginCopies, marketplace, name))
		// a-b@c and a@b-c share the data dir a-b-c: one an install record keeps holds it.
		if !slices.ContainsFunc(slices.Collect(maps.Keys(installs)), func(key string) bool {
			return !slices.Contains(ids, key) && retiredPluginDataName(key) == retiredPluginDataName(id)
		}) {
			copies = append(copies, filepath.Join(dir, claudePlugins, "data", retiredPluginDataName(id)))
		}
	}
	return copies, nil
}

// retiredPluginDataName is the name of id's data dir as Claude Code names it:
// id with every character outside [a-zA-Z0-9_-] a "-", so {name}-{marketplace}.
func retiredPluginDataName(id string) string {
	return claudePluginDataUnsafe.ReplaceAllString(id, "-")
}

// claudePluginDataUnsafe is what Claude Code (2.1.330, its plugin data dir
// helper) replaces in a plugin id to name its data dir.
var claudePluginDataUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)
