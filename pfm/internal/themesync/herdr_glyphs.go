package themesync

import (
	"fmt"
	"os"
	"slices"
	"sync"

	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// One state file is painted per process. Bound the cache even when callers use
// multiple state directories, and never expose the cached frame slices.
var tabGlyphCache struct {
	sync.Mutex
	path   string
	info   os.FileInfo
	glyphs herdr.TabGlyphs
	err    error
}

// HerdrTabGlyphs is the applied theme's tab glyphs, or defaults when off/absent.
// Every paint stats the file; only a changed identity, size, mode or modification
// time reads and decodes its body after a successful load. Failed loads retry on
// the next tick, so a transient read error cannot pin an unchanged file forever.
func HerdrTabGlyphs(stateDir string) (herdr.TabGlyphs, error) {
	store := stateStore{dir: stateDir}
	path := store.statePath()
	tabGlyphCache.Lock()
	defer tabGlyphCache.Unlock()
	info, err := os.Stat(path)
	if err != nil {
		tabGlyphCache.info = nil
		if os.IsNotExist(err) {
			return herdr.TabGlyphs{}, nil
		}
		return herdr.TabGlyphs{}, fmt.Errorf("themesync: stat %s: %w", path, err)
	}
	// Never read a FIFO/device on the painter's half-second tick.
	if !info.Mode().IsRegular() {
		tabGlyphCache.info = nil
		return herdr.TabGlyphs{}, fmt.Errorf("themesync: %s is not a regular file (%s)", path, info.Mode().Type())
	}
	prior := tabGlyphCache.info
	if tabGlyphCache.err != nil || tabGlyphCache.path != path || prior == nil || !os.SameFile(prior, info) ||
		prior.Size() != info.Size() || prior.Mode() != info.Mode() || !prior.ModTime().Equal(info.ModTime()) {
		tabGlyphCache.path, tabGlyphCache.info = path, info
		tabGlyphCache.glyphs, tabGlyphCache.err = loadHerdrTabGlyphs(store)
	}
	return cloneTabGlyphs(tabGlyphCache.glyphs), tabGlyphCache.err
}

func loadHerdrTabGlyphs(store stateStore) (herdr.TabGlyphs, error) {
	state, found, err := store.load()
	if err != nil {
		return herdr.TabGlyphs{}, err
	}
	if !found || !state.Enabled || state.Applied == nil || state.Applied.Pins == nil ||
		state.Applied.Pins.Herdr == nil {
		return herdr.TabGlyphs{}, nil
	}
	glyphs, err := herdr.ParseTabGlyphs(state.Applied.Pins.Herdr.TabGlyphs)
	if err != nil {
		return herdr.TabGlyphs{}, fmt.Errorf("themesync: %s: %w", store.statePath(), err)
	}
	return glyphs, nil
}

func cloneTabGlyphs(glyphs herdr.TabGlyphs) herdr.TabGlyphs {
	return herdr.TabGlyphs{
		Working: slices.Clone(glyphs.Working), Idle: slices.Clone(glyphs.Idle),
		Blocked: slices.Clone(glyphs.Blocked), Done: slices.Clone(glyphs.Done),
		Error: slices.Clone(glyphs.Error), Dead: slices.Clone(glyphs.Dead), Unknown: slices.Clone(glyphs.Unknown),
	}
}
