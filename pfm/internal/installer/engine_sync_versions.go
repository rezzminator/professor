package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// ReportEngineSyncVersions diagnoses competing plugin and stable-link writers without changing them. The
// two-versions row names the Claude seats (the store and accountDirs) that run the plugin unpinned; with every
// such seat pinned to the stable binary the plugin's cached version writes nothing, and the row stays silent.
func ReportEngineSyncVersions(w io.Writer, home string, env paths.Env, accountDirs []string) (warnings, failures int) {
	stable := enginesync.StablePath(env, home)
	if _, err := os.Stat(stable); errors.Is(err, fs.ErrNotExist) {
		return 0, 0
	} else if err != nil {
		fmt.Fprintf(w, "doctor: engine-sync state=FAIL inspect stable %s: %v\n", stable, err)
		return 0, 1
	}
	probe := func(binary string) (string, error) {
		selected := &paths.MapEnv{HomeDir: home, Values: map[string]string{enginesync.EnvBin: binary}}
		return (enginesync.Runner{Env: selected, Home: home}).Version(context.Background())
	}
	stableVersion, err := probe(stable)
	if err != nil {
		fmt.Fprintf(w, "doctor: engine-sync state=FAIL %v\n", err)
		return 0, 1
	}
	versions := filepath.Join(enginesync.DataHome(env, home), "versions")
	entries, err := os.ReadDir(versions)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, 0
	}
	if err != nil {
		fmt.Fprintf(w, "doctor: engine-sync state=FAIL inspect cache %s: %v\n", versions, err)
		return 0, 1
	}
	cachedVersions := map[string]string{}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := enginesync.CompareVersions(entry.Name(), entry.Name()); err != nil {
			fmt.Fprintf(w, "doctor: engine-sync state=WARN cache %s not probed: %v\n",
				filepath.Join(versions, entry.Name()), err)
			warnings++
			continue
		}
		binary := filepath.Join(versions, entry.Name(), "engine-sync")
		info, err := os.Lstat(binary)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			fmt.Fprintf(w, "doctor: engine-sync state=FAIL inspect cache %s: %v\n", binary, err)
			failures++
			continue
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		version, err := probe(binary)
		if err != nil {
			fmt.Fprintf(w, "doctor: engine-sync state=FAIL cache %s: %v\n", binary, err)
			failures++
			continue
		}
		cachedVersions[entry.Name()] = version
		order, err := enginesync.CompareVersions(stableVersion, version)
		if err != nil {
			fmt.Fprintf(w, "doctor: engine-sync state=FAIL %v\n", err)
			failures++
			continue
		}
		if order < 0 {
			fmt.Fprintf(
				w,
				"doctor: engine-sync state=WARN stable link moved backwards: stable %s at %s, cached %s at %s — start a new Claude session so the engine-sync plugin wrapper moves the stable link forward; inspect any FORCE=1 downgrade\n",
				stableVersion,
				stable,
				version,
				binary,
			)
			warnings++
		}
	}
	repo, cloned, err := RecordedGlobalClone(home)
	if err != nil {
		fmt.Fprintf(w, "doctor: engine-sync state=FAIL resolve plugin clone: %v\n", err)
		return warnings, failures + 1
	}
	if !cloned {
		return warnings, failures
	}
	manifest := filepath.Join(repo, "plugins", "engine-sync", "plugins", "engine-sync", ".claude-plugin", "plugin.json")
	data, err := os.ReadFile(manifest)
	if errors.Is(err, fs.ErrNotExist) {
		return warnings, failures
	}
	if err != nil {
		fmt.Fprintf(w, "doctor: engine-sync state=FAIL read plugin manifest %s: %v\n", manifest, err)
		return warnings, failures + 1
	}
	var plugin struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &plugin); err != nil {
		fmt.Fprintf(w, "doctor: engine-sync state=FAIL parse plugin manifest %s: %v\n", manifest, err)
		return warnings, failures + 1
	}
	cached, exists := cachedVersions[plugin.Version]
	if !exists || cached == stableVersion {
		return warnings, failures
	}
	unpinned, seatFailures := unpinnedEngineSyncSeats(w, stable, append([]string{ClaudeStore(home)}, accountDirs...))
	failures += seatFailures
	if len(unpinned) != 0 {
		fmt.Fprintf(
			w,
			"doctor: engine-sync state=WARN two versions can write the same roots: stable %s at %s; plugin cache %s at %s; seats running the plugin without ENGINE_SYNC_BIN on the stable binary: %s — set ENGINE_SYNC_BIN to the stable binary for the plugin seat or disable that seat on this development host\n",
			stableVersion,
			stable,
			cached,
			filepath.Join(versions, plugin.Version, "engine-sync"),
			strings.Join(unpinned, ", "),
		)
		warnings++
	}
	return warnings, failures
}

// unpinnedEngineSyncSeats names each seat whose settings.json enables an
// engine-sync@* plugin while its env.ENGINE_SYNC_BIN is not the stable binary:
// the cleaned stable path, or any spelling that resolves to the same target
// (the wrapper runs whatever the pin resolves to). A stable path that cannot be
// resolved leaves only its cleaned spelling, and the cause rides each unpinned
// seat. Seats sharing one settings.json (an account links the store's) are
// judged once; a missing file enables nothing; one that is not a regular file,
// or cannot be read or parsed, is a FAIL row naming it, and the other seats are
// still judged.
func unpinnedEngineSyncSeats(w io.Writer, stable string, seats []string) (unpinned []string, failures int) {
	resolved, resolveErr := filepath.EvalSymlinks(stable)
	isStable := func(bin string) bool {
		if filepath.Clean(bin) == filepath.Clean(stable) {
			return true
		}
		// A pin that does not resolve is unusable to the wrapper too: unpinned.
		target, err := filepath.EvalSymlinks(bin)
		return resolveErr == nil && err == nil && target == resolved
	}
	unresolved := ""
	if resolveErr != nil {
		unresolved = "; stable unresolved: " + resolveErr.Error()
	}
	seen := map[string]bool{}
	for _, seat := range seats {
		path := filepath.Join(seat, claudeSettingsName)
		physical := paths.PhysicalPath(path)
		if seen[physical] {
			continue
		}
		seen[physical] = true
		// A FIFO or device would block the read below; a stat error other than
		// absence is the reader's to report.
		if info, err := os.Stat(path); err == nil && !info.Mode().IsRegular() {
			fmt.Fprintf(
				w,
				"doctor: engine-sync state=FAIL seat settings %s: not a regular file (%s)\n",
				path,
				info.Mode().Type(),
			)
			failures++
			continue
		}
		document, err := readClaudeSettingsDocument(path)
		if err != nil {
			fmt.Fprintf(w, "doctor: engine-sync state=FAIL seat settings: %v\n", err)
			failures++
			continue
		}
		if !engineSyncPluginEnabled(document) {
			continue
		}
		env, _ := document["env"].(map[string]any)
		value, set := env[enginesync.EnvBin]
		bin, isString := value.(string)
		switch {
		case !set:
			unpinned = append(unpinned, path+" ("+enginesync.EnvBin+" unset"+unresolved+")")
		case !isString || !isStable(bin):
			unpinned = append(unpinned, fmt.Sprintf("%s (%s=%v%s)", path, enginesync.EnvBin, value, unresolved))
		}
	}
	return unpinned, failures
}

// engineSyncPluginEnabled reports whether document's enabledPlugins turns on
// an engine-sync plugin from any marketplace.
func engineSyncPluginEnabled(document map[string]any) bool {
	enabled, _ := document["enabledPlugins"].(map[string]any)
	for id, value := range enabled {
		if strings.HasPrefix(id, "engine-sync@") && value == true {
			return true
		}
	}
	return false
}
