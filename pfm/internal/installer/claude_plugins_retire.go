package installer

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/retired"
)

// retireClaudePlugins removes every trace of each plugin id the retired lists
// retired with no successor (retired.Lists ClaudePluginsRemoved) from the Claude
// store and every config dir install covers, each physical file once: its
// enabledPlugins key in settings.json and settings.local.json, enabled or not
// (an emptied member goes too; a settings.local.json that cannot be read is a
// skip row, retiredPluginLocalUnread), its installed_plugins.json entries, Claude's cache copy and data dir, then its
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

// retiredPluginLocalUnread is install's skip row and doctor's gap for a
// settings.local.json that cannot be read or parsed: pfm's only business in
// that file is retiring a plugin, so it never fails an install (or rolls an
// update back) over it, as an unread Herdr plugin registry does not.
const retiredPluginLocalUnread = "pfm only retires plugins there, so the file stays as it is; " +
	"a retired plugin it enables stays enabled until the file reads and pfm install --yes runs again"

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

// retiredPluginTake is the take-back of ids: their keys in the enabledPlugins
// of settings.json and settings.local.json and in installed_plugins.json, then
// their copies (retiredPluginCopies). Doctor's retiredPluginTraces reads the same.
// A settings.local.json that cannot be read stays, named (retiredPluginLocalUnread).
func retiredPluginTake(ids []string) claudePluginTake {
	retiredID := func(key string) bool { return slices.Contains(ids, key) }
	return claudePluginTake{
		edits: []claudeRecordEdit{
			{file: claudeSettingsName, member: claudeEnabledPlugins, ours: retiredID, dropEmpty: true},
			{
				file: claudeLocalSettingsName, member: claudeEnabledPlugins, ours: retiredID, dropEmpty: true,
				unread: retiredPluginLocalUnread,
			},
			{file: filepath.Join(claudePlugins, "installed_plugins.json"), member: claudePlugins, ours: retiredID},
		},
		copies: func(dir string) ([]string, error) { return retiredPluginCopies(dir, ids) },
		within: []string{claudePluginCopies, "data"},
	}
}

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
	folders, _, failed := retiredPluginLogFolders(home, ManagedRoot(home), dirs, ids)
	var unreadGaps []ClaudePluginGap
	for _, id := range ids {
		traces, unread, err := retiredPluginTraces(dirs, retiredPluginTake([]string{id}))
		failed = errors.Join(failed, err)
		for _, cause := range unread {
			gap := ClaudePluginGap{
				Problem: cause.Error() + ", so pfm cannot tell whether it still enables a plugin retired with no successor",
				Fix:     "repair or remove that file, then " + ClaudePluginRepair,
			}
			if !slices.Contains(unreadGaps, gap) {
				unreadGaps = append(unreadGaps, gap)
			}
		}
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
	return append(gaps, unreadGaps...), failed
}

// retiredPluginTraces names each record of dirs holding one of take's keys
// ("{member} in {path}") and each of its copies on disk, each physical path
// once; a record an edit with unread set cannot read is returned in unread,
// as install skips it, and leaves that dir's copies judged.
func retiredPluginTraces(dirs []string, take claudePluginTake) (traces []string, unread []error, failed error) {
	seen := map[string]bool{}
	for _, dir := range dirs {
		unjudged := false // a record that cannot be read leaves its copies unjudged
		for _, edit := range take.edits {
			path := filepath.Join(dir, edit.file)
			physical := paths.PhysicalPath(path)
			if seen[physical] {
				continue
			}
			seen[physical] = true
			_, document, err := readSettingsForEdit(path)
			if err != nil && edit.unread != "" {
				unread = append(unread, err)
				continue
			}
			if err != nil {
				failed, unjudged = errors.Join(failed, err), true
				continue
			}
			if keys, _ := edit.held(document); len(keys) != 0 {
				traces = append(traces, edit.member+" in "+path)
			}
		}
		if unjudged {
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
	return traces, unread, failed
}
