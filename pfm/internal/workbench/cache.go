package workbench

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

type cachedWorkbench struct {
	Dir  string `json:"dir"`
	Root string `json:"root"`
}

type cachedWalkError struct {
	Root  string `json:"root"`
	Path  string `json:"path"`
	Error string `json:"error"`
}

type workbenchCache struct {
	Version     int               `json:"version"`
	Workbenches []cachedWorkbench `json:"workbenches"`
	Errors      []cachedWalkError `json:"errors"`
}

// ReadCache reloads manifests from the last discovery without walking roots.
func ReadCache(path string) ([]Bench, []WalkError, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, workbenchCacheError(path, fmt.Errorf("read: %w", err))
	}
	var cache workbenchCache
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cache); err != nil {
		return nil, nil, workbenchCacheError(path, fmt.Errorf("decode: %w", err))
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}
		return nil, nil, workbenchCacheError(path, fmt.Errorf("decode: %w", err))
	}
	if cache.Version != 1 {
		return nil, nil, workbenchCacheError(path, fmt.Errorf("unsupported version %d", cache.Version))
	}
	var benches []Bench
	seen := make(map[string]bool)
	for _, entry := range cache.Workbenches {
		if !filepath.IsAbs(entry.Dir) || !filepath.IsAbs(entry.Root) || !eligibleBench(entry.Root, entry.Dir) {
			return nil, nil, workbenchCacheError(
				path,
				fmt.Errorf("invalid workbench directory %q under %q", entry.Dir, entry.Root),
			)
		}
		found, err := paths.HasWorkbenchManifest(entry.Dir)
		if err != nil {
			return nil, nil, workbenchCacheError(path, err)
		}
		if found && !seen[entry.Dir] {
			benches = append(benches, LoadBench(entry.Dir, entry.Root))
			seen[entry.Dir] = true
		}
	}
	sort.Slice(benches, func(i, j int) bool { return benches[i].Dir < benches[j].Dir })
	assignKeys(benches)
	var walkErrors []WalkError
	for _, entry := range cache.Errors {
		walkErrors = append(walkErrors, WalkError{Root: entry.Root, Path: entry.Path, Err: errors.New(entry.Error)})
	}
	return benches, walkErrors, nil
}

// WriteCache atomically publishes discovered directories and walk failures.
func WriteCache(path string, benches []Bench, walkErrors []WalkError) error {
	cache := workbenchCache{
		Version:     1,
		Workbenches: make([]cachedWorkbench, 0, len(benches)),
		Errors:      make([]cachedWalkError, 0, len(walkErrors)),
	}
	for i := range benches {
		bench := &benches[i]
		cache.Workbenches = append(cache.Workbenches, cachedWorkbench{Dir: bench.Dir, Root: bench.Root})
	}
	for _, fault := range walkErrors {
		cache.Errors = append(
			cache.Errors,
			cachedWalkError{Root: fault.Root, Path: fault.Path, Error: fault.Err.Error()},
		)
	}
	raw, err := json.Marshal(cache)
	if err != nil {
		return workbenchCacheError(path, fmt.Errorf("encode: %w", err))
	}
	if err := atomicfile.Write(path, raw, 0o600); err != nil {
		return workbenchCacheError(path, err)
	}
	return nil
}

func workbenchCacheError(path string, err error) error {
	wrapped := fmt.Errorf("workbench cache %s: %w", path, err)
	obs.Logger(context.Background()).Error("workbench cache", "path", path, obs.FieldErr, wrapped)
	return wrapped
}
