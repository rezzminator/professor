package doctor

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// pluginLogFolderAccess is what a plugin needs of its log folder: list it,
// create files in it and enter it.
const pluginLogFolderAccess = unix.R_OK | unix.W_OK | unix.X_OK

// printManagedPluginLogsDoctor includes the exact files claude_env delegates,
// not merely their parent directories: a symlink target need not be settings.json.
func printManagedPluginLogsDoctor(stdout io.Writer, dirs []string, build installer.ClaudePluginBuild,
	env paths.Env, managedRoot, settingsPath string, tally *doctorTally,
) {
	var files []string
	if obs.AlphaBuild(build.Version) {
		var err error
		files, err = installer.ClaudeEnvSettingsPaths(managedRoot, settingsPath)
		if err != nil {
			tally.fail()
			fmt.Fprintf(
				stdout,
				"doctor: plugin_logs FAIL could not enumerate managed settings: %v — repair the ownership ledger, then rerun pfm doctor\n",
				err,
			)
		}
	}
	printPluginLogsDoctor(stdout, dirs, build, env, unix.Access, tally, files...)
}

// printPluginLogsDoctor checks the plugin-logs system on an -alpha build, the
// one that installs the marketplace plugins from their checkouts
// (obs.AlphaBuild); any other build prints one untallied line saying it does
// not apply. dirs are the Claude config dirs pfm manages, store first: each
// settings.json must set installer.PluginLogDirEnv, every one to the same
// absolute path — missing, differing, relative and unreadable are a line each.
// Each set folder must be a directory access grants read, write and search
// on (pluginLogFolderGap). While a value is set, no marketplace plugin may
// hold a row in its fallback (installer.PluginLogFallbackDir, resolved with
// env). A read that fails is a failure naming it, never a clean answer. Every
// line prints its text control-escaped (installer.EscapeControl): a value read
// from a file cannot forge a line.
func printPluginLogsDoctor(
	stdout io.Writer,
	dirs []string,
	build installer.ClaudePluginBuild,
	env paths.Env,
	access func(string, uint32) error,
	tally *doctorTally,
	extraSettings ...string,
) {
	if !obs.AlphaBuild(build.Version) {
		fmt.Fprintf(stdout, "doctor: plugin_logs does not apply: pfm %s is not an -alpha build\n",
			installer.EscapeControl(build.Version))
		return
	}
	clean := true
	note := func(format string, args ...any) {
		for index, arg := range args {
			switch value := arg.(type) {
			case string:
				args[index] = installer.EscapeControl(value)
			case error:
				args[index] = installer.EscapeControl(value.Error())
			}
		}
		fmt.Fprintf(stdout, "doctor: plugin_logs "+format+"\n", args...)
	}
	warn := func(format string, args ...any) {
		clean = false
		tally.warn()
		note(format, args...)
	}
	fail := func(format string, args ...any) {
		clean = false
		tally.fail()
		note(format, args...)
	}
	settings := installer.ReadPluginLogEnv(dirs, extraSettings...)
	folders := checkPluginLogEnv(settings, warn, fail)
	for _, folder := range folders {
		if gap, failure := pluginLogFolderGap(folder, access); gap != "" && failure {
			fail("%s", gap)
		} else if gap != "" {
			warn("%s", gap)
		}
	}
	set := slices.ContainsFunc(settings, func(setting installer.PluginLogEnvSetting) bool {
		return setting.Value != ""
	})
	if set {
		checkPluginLogFallbacks(build, env, warn, fail)
	}
	if clean {
		note("ok: %s=%s in every config dir", installer.PluginLogDirEnv, strings.Join(folders, ","))
	}
}

// checkPluginLogEnv judges the settings of every config dir and returns each
// distinct absolute value, cleaned and sorted: the folders to probe. Two
// spellings of one absolute path ("/logs" and "/logs/") are one value.
func checkPluginLogEnv(settings []installer.PluginLogEnvSetting, warn, fail func(string, ...any)) []string {
	type file struct {
		setting installer.PluginLogEnvSetting
		dirs    []string
	}
	var files []*file
	byPhysical := map[string]*file{}
	for _, setting := range settings {
		group, ok := byPhysical[setting.Physical]
		if !ok {
			group = &file{setting: setting}
			byPhysical[setting.Physical] = group
			files = append(files, group)
		}
		group.dirs = append(group.dirs, setting.Dir)
	}
	values := map[string][]string{}
	for _, group := range files {
		setting, named := group.setting, strings.Join(group.dirs, ", ")
		switch {
		case setting.Err != nil:
			fail("could not read %s for config dir %s: %v", installer.PluginLogDirEnv, named, setting.Err)
		case setting.Absent:
			if _, err := os.Lstat(setting.Settings); errors.Is(err, fs.ErrNotExist) {
				warn("%s is missing for config dir %s: no %s — set env.%s in it to the shared log folder",
					installer.PluginLogDirEnv, named, setting.Settings, installer.PluginLogDirEnv)
				continue
			}
			warn("%s is missing for config dir %s: %s sets no env.%s — set it to the shared log folder",
				installer.PluginLogDirEnv, named, setting.Settings, installer.PluginLogDirEnv)
		default:
			value := setting.Value
			if filepath.IsAbs(value) {
				value = filepath.Clean(value)
			}
			values[value] = append(values[value], group.dirs...)
		}
	}
	if len(values) > 1 {
		var pairs []string
		for _, setting := range settings {
			if setting.Err == nil && !setting.Absent {
				pairs = append(pairs, setting.Dir+"="+setting.Value)
			}
		}
		warn("%s differs across config dirs: %s — set one folder in all of them",
			installer.PluginLogDirEnv, strings.Join(pairs, " "))
	}
	folders := make([]string, 0, len(values))
	for _, value := range slices.Sorted(maps.Keys(values)) {
		owners := values[value]
		if !filepath.IsAbs(value) {
			warn("%s \"%s\" for config dir %s is not an absolute path — plugins resolve it from wherever they run",
				installer.PluginLogDirEnv, value, strings.Join(owners, ", "))
			continue
		}
		folders = append(folders, value)
	}
	return folders
}

// pluginLogFolderGap names what keeps plugins from writing under folder, ""
// when nothing does; failure says doctor could not look, or the folder is
// unusable, rather than not there yet. access is unix.Access in production:
// the check asks the kernel, not the mode bits, so a root run is judged as it
// writes.
func pluginLogFolderGap(folder string, access func(string, uint32) error) (string, bool) {
	info, err := os.Stat(folder)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if link, linkErr := os.Lstat(folder); linkErr == nil && link.Mode()&fs.ModeSymlink != 0 {
			return "the log folder " + folder + " is a link to a missing target — point it at a directory", true
		}
		return "no log folder at " + folder + " — create it: mkdir -p " + folder, false
	case err != nil:
		return fmt.Sprintf("could not inspect the log folder %s: %v", folder, err), true
	case !info.IsDir():
		return "the log folder " + folder + " is not a directory — replace it with one", true
	}
	if err := access(folder, pluginLogFolderAccess); err != nil {
		return fmt.Sprintf(
				"the log folder %s is unreadable or unwritable: %v — grant this user rwx on it",
				folder,
				err,
			),
			true
	}
	return "", false
}

// checkPluginLogFallbacks counts the rows each marketplace plugin left in its
// fallback (installer.PluginLogFallbackDir) while a log folder is set.
func checkPluginLogFallbacks(build installer.ClaudePluginBuild, env paths.Env, warn, fail func(string, ...any)) {
	switch {
	case build.SourceRepoErr != nil:
		fail("fallback logs not checked: source clone unresolved: %v", build.SourceRepoErr)
		return
	case build.SourceRepo == "":
		warn("fallback logs not checked: no source clone to read the marketplace plugin list from")
		return
	}
	_, names, err := installer.MarketplacePluginNames(build.SourceRepo)
	if err != nil {
		fail("fallback logs not checked: %v", err)
		return
	}
	for _, name := range names {
		dir, err := installer.PluginLogFallbackDir(env, build.Home, name)
		if err != nil {
			fail("plugin %s: fallback logs not checked: %v", name, err)
			continue
		}
		rows, err := fallbackLogRows(dir)
		switch {
		case err != nil:
			fail("plugin %s: could not count the rows in its fallback %s: %v", name, dir, err)
		case rows > 0:
			warn("plugin %s: %d rows stranded in its fallback %s while %s is set — move them to the log folder",
				name, rows, dir, installer.PluginLogDirEnv)
		}
	}
}

// fallbackLogRows is the non-blank lines over dir's *.jsonl files and their
// *.jsonl.1 rotations (a writer moves a full file aside to .1); none when dir
// does not exist. A matching name that is not a regular file is an error: a
// FIFO or a device would block or never end.
func fallbackLogRows(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", dir, err)
	}
	rows := 0
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") && !strings.HasSuffix(name, ".jsonl.1") {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		switch {
		case err != nil:
			return 0, fmt.Errorf("inspect %s: %w", path, err)
		case info.IsDir():
			continue
		case !info.Mode().IsRegular():
			return 0, fmt.Errorf("%s is not a regular file", path)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", path, err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(raw))
		scanner.Buffer(make([]byte, 0, 64*1024), len(raw)+1)
		for scanner.Scan() {
			if len(bytes.TrimSpace(scanner.Bytes())) > 0 {
				rows++
			}
		}
		if err := scanner.Err(); err != nil {
			return 0, fmt.Errorf("scan %s: %w", path, err)
		}
	}
	return rows, nil
}
