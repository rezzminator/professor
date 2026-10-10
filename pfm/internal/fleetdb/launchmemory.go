package fleetdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// LaunchMemoryKey is the meta row holding the choices of the last new-chat
// launch the picker made.
const LaunchMemoryKey = "ls_launch_memory"

// LaunchMemory is what the picker's new-chat steps default to: the engine the
// last launch started, the account each engine last ran on, and the prompt
// cache each Claude account last launched with. A key absent from a map was
// never launched; the caller falls back to the configured value.
type LaunchMemory struct {
	Engine   string         `json:"engine,omitempty"`
	Accounts map[string]int `json:"accounts,omitempty"`
	Cache1H  map[int]bool   `json:"cache1h,omitempty"`
}

// LaunchMemory reads the remembered launch choices; a store with no row yet
// reads as an empty memory, and a store that could not open, or a row that
// does not parse, is an error — never an empty memory.
func (s *Store) LaunchMemory(ctx context.Context) (LaunchMemory, error) {
	if s.degraded != nil {
		return LaunchMemory{}, fmt.Errorf("read launch memory: %w", s.degraded)
	}
	raw, found, err := s.Meta(ctx, LaunchMemoryKey)
	if err != nil {
		return LaunchMemory{}, err
	}
	if !found {
		return LaunchMemory{}, nil
	}
	var memory LaunchMemory
	if err := json.Unmarshal([]byte(raw), &memory); err != nil {
		return LaunchMemory{}, fmt.Errorf("%w %q: %w", errLaunchMemoryCorrupt, raw, err)
	}
	return memory, nil
}

var errLaunchMemoryCorrupt = errors.New("parse launch memory")

// RememberLaunch folds one launch into the stored memory: the engine, the
// account it ran on and, for an engine with a cache choice, that choice for
// that account (cache nil leaves the cache memory alone).
func (s *Store) RememberLaunch(
	ctx context.Context,
	engine string,
	account int,
	cache *bool,
	updatedAt int64,
) error {
	if engine == "" {
		return errors.New("remember launch requires an engine")
	}
	memory, err := s.LaunchMemory(ctx)
	if errors.Is(err, errLaunchMemoryCorrupt) {
		// A row that does not parse holds nothing to keep, and the picker already
		// named it when it opened: the launch being recorded replaces it.
		memory, err = LaunchMemory{}, nil
	}
	if err != nil {
		return err
	}
	memory.Engine = engine
	if account > 0 {
		if memory.Accounts == nil {
			memory.Accounts = make(map[string]int)
		}
		memory.Accounts[engine] = account
		if cache != nil {
			if memory.Cache1H == nil {
				memory.Cache1H = make(map[int]bool)
			}
			memory.Cache1H[account] = *cache
		}
	}
	encoded, err := json.Marshal(memory)
	if err != nil {
		return fmt.Errorf("encode launch memory: %w", err)
	}
	return s.SetMeta(ctx, LaunchMemoryKey, string(encoded), updatedAt)
}
