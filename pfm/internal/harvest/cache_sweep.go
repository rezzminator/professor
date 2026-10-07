package harvest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// The cache's expiry: at most once per cacheSweepInterval, on a cache write,
// every file older than cacheMaxAge since its last write goes (every kind,
// public/ included), then the oldest go until the cache fits cacheMaxBytes; a
// file younger than cacheWriteGrace (a write in flight) never goes.
const (
	cacheSweepStampName = ".cache-sweep"
	cacheSweepInterval  = 24 * time.Hour
	cacheMaxAge         = 30 * 24 * time.Hour
	cacheMaxBytes       = int64(2048) << 20
	cacheWriteGrace     = time.Hour
)

// cacheSweepRemove removes one expired cache file; a test swaps it to fail.
var cacheSweepRemove = os.Remove

// cacheSweepGate keeps one sweep at a time in this process.
var cacheSweepGate sync.Mutex

type cacheSweepResult struct {
	removed, failed int
	freed           int64
}

// afterCacheWrite runs the cache's sweep after a write landed, when the
// stamp in the cache root is absent or a day old. Another sweep running in
// this process skips it; a failure is logged and the write it follows stands.
func (h *Harvester) afterCacheWrite() {
	if !cacheSweepGate.TryLock() {
		return
	}
	defer cacheSweepGate.Unlock()
	logger := obs.Logger(context.Background())
	root, err := h.resolvedCacheRoot()
	if err != nil {
		logger.Warn("harvest.cache_sweep", "path", h.options.CacheDir, obs.FieldErr, err.Error())
		return
	}
	now := h.nowClock().Now()
	stamp := filepath.Join(root, cacheSweepStampName)
	info, err := os.Stat(stamp)
	if err == nil {
		if age := now.Sub(info.ModTime()); age >= 0 && age < cacheSweepInterval {
			return
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		logger.Warn("harvest.cache_sweep", "path", stamp, obs.FieldErr, err.Error())
		return
	}
	if err := touchCacheSweepStamp(stamp, now); err != nil {
		logger.Warn("harvest.cache_sweep", "path", stamp, obs.FieldErr, err.Error())
		return
	}
	result := sweepCacheRoot(root, now, cacheMaxAge, cacheMaxBytes)
	logger.Info("harvest.cache_sweep", "path", root, "removed", result.removed, "freed_bytes", result.freed,
		"failed", result.failed)
}

func touchCacheSweepStamp(path string, now time.Time) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create cache sweep stamp: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close cache sweep stamp: %w", err)
	}
	if err := os.Chtimes(path, now, now); err != nil {
		return fmt.Errorf("stamp cache sweep: %w", err)
	}
	return nil
}

type sweptCacheFile struct {
	path    string
	size    int64
	written time.Time
}

// sweepCacheRoot removes every regular file under root last written more than
// maxAge before now, then the oldest until the rest fit maxBytes, never one
// written within cacheWriteGrace. Each removal is a debug record with its
// bytes, each failure a warn record. A root that is a symlink (the cache root
// may be one) is swept through it; links below the root are never followed.
func sweepCacheRoot(root string, now time.Time, maxAge time.Duration, maxBytes int64) cacheSweepResult {
	logger := obs.Logger(context.Background())
	var result cacheSweepResult
	var files []sweptCacheFile
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		logger.Warn("harvest.cache_sweep", "path", root, obs.FieldErr, err.Error())
		result.failed++
		return result
	}
	root = resolved
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			logger.Warn("harvest.cache_sweep", "path", path, obs.FieldErr, err.Error())
			result.failed++
			return nil
		}
		if !entry.Type().IsRegular() || path == filepath.Join(root, cacheSweepStampName) {
			return nil
		}
		info, err := entry.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			logger.Warn("harvest.cache_sweep", "path", path, obs.FieldErr, err.Error())
			result.failed++
			return nil
		}
		files = append(files, sweptCacheFile{path: path, size: info.Size(), written: info.ModTime()})
		return nil
	})
	if walkErr != nil {
		logger.Warn("harvest.cache_sweep", "path", root, obs.FieldErr, walkErr.Error())
		result.failed++
	}
	remove := func(file sweptCacheFile) bool {
		err := cacheSweepRemove(file.path)
		if errors.Is(err, fs.ErrNotExist) {
			return true
		}
		if err != nil {
			logger.Warn("harvest.cache_sweep", "path", file.path, obs.FieldErr, err.Error())
			result.failed++
			return false
		}
		logger.Debug("harvest.cache_sweep", "path", file.path, "bytes", file.size)
		result.removed++
		result.freed += file.size
		return true
	}
	var total int64
	kept := files[:0]
	for _, file := range files {
		if now.Sub(file.written) > maxAge && remove(file) {
			continue
		}
		kept = append(kept, file)
		total += file.size
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].written.Before(kept[j].written) })
	for _, file := range kept {
		if total <= maxBytes || now.Sub(file.written) < cacheWriteGrace {
			break
		}
		if remove(file) {
			total -= file.size
		}
	}
	return result
}
