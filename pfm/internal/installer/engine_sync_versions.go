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

	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// ReportEngineSyncVersions diagnoses competing plugin and stable-link writers without changing them.
func ReportEngineSyncVersions(w io.Writer, home string, env paths.Env) (warnings, failures int) {
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
	if cached, exists := cachedVersions[plugin.Version]; exists && cached != stableVersion {
		fmt.Fprintf(
			w,
			"doctor: engine-sync state=WARN two versions can write the same roots: stable %s at %s; plugin cache %s at %s — set ENGINE_SYNC_BIN to the stable binary for the plugin seat or disable that seat on this development host\n",
			stableVersion,
			stable,
			cached,
			filepath.Join(versions, plugin.Version, "engine-sync"),
		)
		warnings++
	}
	return warnings, failures
}
