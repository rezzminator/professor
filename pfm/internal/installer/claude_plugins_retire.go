package installer

import (
	"errors"
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
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// retireClaudePlugins removes every trace of each plugin id the retired lists
// retired with no successor (retired.Lists ClaudePluginsRemoved) from the Claude
// store and every config dir install covers, each physical file once: its
// enabledPlugins key, enabled or not (an emptied member goes too), its
// installed_plugins.json entries, Claude's cache copy and data dir, then its
// folder under each plugin log folder (retirePluginLogDirs). Another id of
// the same bare name is the owner's and stays. A dir that fails keeps its copies
// and costs no other dir; the log folders wait until every dir is clean.
//
// An id the install still plans (RetiredClaudePluginsPlanned) is a plugin in
// use: it stays, named, and so does every id when the plan cannot be read.
func (installer *engine) retireClaudePlugins() error {
	lists, _ := installer.retiredLists() // A failed read is deferred once by Run.
	if len(lists.ClaudePluginsRemoved) == 0 {
		return nil
	}
	build, err := installer.claudePluginBuild()
	if err != nil {
		installer.skip("claude plugins: no retired plugin removed — the planned plugin list is unread, " +
			"so a retired id may still be in use: " + err.Error())
		return nil
	}
	ids, planned := RetiredClaudePluginsPlanned(lists.ClaudePluginsRemoved, build)
	for _, id := range planned {
		installer.skip("claude plugins: retired plugin " + id + " kept — " + retiredPluginPlanned + "; " +
			retiredPluginPlannedFix)
	}
	if len(ids) == 0 {
		return nil
	}
	dirs := retiredPluginDirs(installer.options.Home, installer.claudePluginDirs())
	take := retiredPluginTake(ids)
	seen := map[string]bool{}
	var failed error
	for _, dir := range dirs {
		if err := installer.takeClaudePluginDir(dir, seen, take); err != nil {
			failed = errors.Join(failed, installer.pluginStepFailure(dir, err))
		}
	}
	if failed != nil {
		return failed
	}
	return installer.retirePluginLogDirs(dirs, ids)
}

// retiredPluginPlanned and retiredPluginPlannedFix are why install keeps a
// retired id it still plans and the repair, its skip line's and doctor's.
const (
	retiredPluginPlanned    = "claude.plugins or the source clone's marketplace still plans it"
	retiredPluginPlannedFix = "drop it there or from claudePlugins[\"" + retired.ClaudePluginsNoSuccessor +
		"\"] in the retired lists"
)

// RetiredClaudePluginsPlanned splits ids (retired.Lists ClaudePluginsRemoved)
// into those to retire and those build still plans (ClaudePluginTargets:
// claude.plugins and, on a checkout host, the source clone's marketplace).
func RetiredClaudePluginsPlanned(ids []string, build ClaudePluginBuild) (retire, planned []string) {
	targets := ClaudePluginTargets(build)
	for _, id := range ids {
		if slices.ContainsFunc(targets, func(target ClaudePluginTarget) bool { return target.ID == id }) {
			planned = append(planned, id)
		} else {
			retire = append(retire, id)
		}
	}
	return retire, planned
}

// retiredPluginDirs is dirs with the Claude store of home added last, where
// doctor looks first: a retired plugin's records there are traces too.
func retiredPluginDirs(home string, dirs []string) []string {
	if home == "" {
		return dirs
	}
	if store := ClaudeStore(home); !slices.Contains(dirs, store) {
		dirs = append(slices.Clone(dirs), store)
	}
	return dirs
}

// retiredPluginTake is the take-back of ids: their keys in settings.json's
// enabledPlugins and installed_plugins.json, then their copies (retiredPluginCopies).
func retiredPluginTake(ids []string) claudePluginTake {
	retiredID := func(key string) bool { return slices.Contains(ids, key) }
	return claudePluginTake{
		edits: []claudeRecordEdit{
			{file: claudeSettingsName, member: "enabledPlugins", ours: retiredID, dropEmpty: true},
			{file: filepath.Join(claudePlugins, "installed_plugins.json"), member: claudePlugins, ours: retiredID},
		},
		copies: func(dir string) ([]string, error) { return retiredPluginCopies(dir, ids) },
		within: []string{claudePluginCopies, "data"},
	}
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

// RetiredClaudePluginGaps names, per id of ids (retired.Lists
// ClaudePluginsRemoved), every trace retireClaudePlugins removes that is still
// in the Claude store of home or one of dirs, each physical path once: a record
// holding its key, a copy on disk, and under the first id of its name its
// plugin log folder. A record that cannot be read is an error beside the gaps
// found elsewhere, never an answer.
//
// An id build still plans (RetiredClaudePluginsPlanned) is one gap naming
// where, never judged by its traces: install keeps it.
func RetiredClaudePluginGaps(home string, dirs, ids []string, build ClaudePluginBuild) ([]ClaudePluginGap, error) {
	ids, planned := RetiredClaudePluginsPlanned(ids, build)
	var gaps []ClaudePluginGap
	for _, id := range planned {
		gaps = append(gaps, ClaudePluginGap{
			Problem: "plugin " + id + " is retired with no successor but " + retiredPluginPlanned,
			Fix:     retiredPluginPlannedFix,
		})
	}
	dirs = retiredPluginDirs(home, dirs)
	// a kept folder is no trace: install leaves it
	folders, _, failed := retiredPluginLogFolders(ManagedRoot(home), dirs, ids)
	for _, id := range ids {
		traces, err := retiredPluginTraces(dirs, retiredPluginTake([]string{id}))
		failed = errors.Join(failed, err)
		if found, ok := folders[retiredPluginName(id)]; ok {
			traces = append(traces, found...)
			delete(folders, retiredPluginName(id))
		}
		if len(traces) != 0 {
			gaps = append(gaps, ClaudePluginGap{
				Problem: "plugin " + id + " is retired with no successor but still on this host: " +
					strings.Join(traces, ", "),
				Fix: ClaudePluginRepair + " removes every trace of it",
			})
		}
	}
	return gaps, failed
}

// retiredPluginTraces names each record of dirs holding one of take's keys
// ("{member} in {path}") and each of its copies on disk, each physical path once.
func retiredPluginTraces(dirs []string, take claudePluginTake) ([]string, error) {
	seen := map[string]bool{}
	var traces []string
	var failed error
	for _, dir := range dirs {
		unread := false // a record that cannot be read leaves its copies unjudged
		for _, edit := range take.edits {
			path := filepath.Join(dir, edit.file)
			physical := paths.PhysicalPath(path)
			if seen[physical] {
				continue
			}
			seen[physical] = true
			_, document, err := readSettingsForEdit(path)
			if err != nil {
				failed, unread = errors.Join(failed, err), true
				continue
			}
			if keys, _ := edit.held(document); len(keys) != 0 {
				traces = append(traces, edit.member+" in "+path)
			}
		}
		if unread {
			continue
		}
		copies, err := take.copies(dir)
		if err != nil {
			failed = errors.Join(failed, err)
			continue
		}
		for _, path := range copies {
			physical := paths.PhysicalPath(path)
			if path == "" || seen[physical] {
				continue
			}
			seen[physical] = true
			if _, err := os.Lstat(path); err == nil {
				traces = append(traces, path)
			} else if !errors.Is(err, fs.ErrNotExist) {
				failed = errors.Join(failed, err) // the *PathError names path
			}
		}
	}
	return traces, failed
}
