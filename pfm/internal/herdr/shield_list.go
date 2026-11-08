package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/deps"
)

// shieldList is one `herdr-shield list --json` run against one Herdr socket.
type shieldList struct {
	at      time.Time
	holders []ShieldHolder
	panes   map[string]string
	err     error
}

// Hold keeps every listing the shield has or takes until release, so one pass
// (a fleet scan, a painter tick) runs herdr-shield once per socket however long
// it takes. A pass starts from the listings still fresh, the older ones
// dropped; holds nest, release is idempotent, and a nil Shield holds nothing.
func (shield *Shield) Hold() (release func()) {
	if shield == nil {
		return func() {}
	}
	shield.mu.Lock()
	defer shield.mu.Unlock()
	if shield.holds == 0 {
		now := shield.now()
		for socket, listed := range shield.listings {
			if now.Sub(listed.at) >= shieldListFresh {
				delete(shield.listings, socket)
			}
		}
	}
	shield.holds++
	var once sync.Once
	return func() {
		once.Do(func() {
			shield.mu.Lock()
			defer shield.mu.Unlock()
			shield.holds--
		})
	}
}

func (shield *Shield) now() time.Time {
	if shield.Clock == nil {
		return clock.Real.Now()
	}
	return shield.Clock.Now()
}

// list runs `herdr-shield list --json` against the Herdr server at socket, or
// answers from a listing of it still fresh or held. herdr-shield enumerates the server
// HERDR_SOCKET_PATH names and refuses to run under a held shell's
// HERDR_SHIELD_INNER, which pfm inherits when it runs in one.
// A nil Shield reads no holder: every herdr-shield question to it is an error.
func (shield *Shield) list(ctx context.Context, socket string) ([]ShieldHolder, map[string]string, error) {
	if shield == nil {
		return nil, nil, errors.New("no herdr-shield reader is wired here")
	}
	shield.mu.Lock()
	defer shield.mu.Unlock()
	now := shield.now()
	// A held pass keeps a listing past its freshness only when it succeeded: a
	// failed run is retried once stale, so one bad run never fails a whole pass.
	if listed, found := shield.listings[socket]; found &&
		(now.Sub(listed.at) < shieldListFresh || shield.holds > 0 && listed.err == nil) {
		return listed.holders, listed.panes, listed.err
	}
	runner := shield.Runner
	if runner == nil {
		runner = deps.RealRunner{}
	}
	bounded, cancel := context.WithTimeout(ctx, shieldListBound)
	defer cancel()
	listed := shieldList{at: now}
	shieldPath, resolveErr := deps.Resolve(ShieldBinary)
	var result deps.RunResult
	var err error
	if resolveErr == nil {
		result, err = runner.Run(bounded, []string{shieldPath, "list", "--json"}, deps.RunOptions{
			Env:          append(without(os.Environ(), []string{EnvShieldInner, EnvSocket}), EnvSocket+"="+socket),
			WaitDelay:    2 * time.Second,
			ProcessGroup: true,
		})
	}
	switch {
	case resolveErr != nil:
		listed.err = fmt.Errorf("resolve %s: %w", ShieldBinary, resolveErr)
	case err != nil:
		listed.err = fmt.Errorf("run %s list --json against %s: %w", ShieldBinary, socket, err)
	case result.ExitCode != 0:
		listed.err = fmt.Errorf(
			"%s list --json against %s: exit %d: %s",
			ShieldBinary,
			socket,
			result.ExitCode,
			deps.FirstLine(string(result.Stderr)),
		)
	default:
		listed.holders, listed.panes, listed.err = parseShieldList(result.Stdout)
	}
	// A run its caller cut short says nothing about herdr-shield: never serve it on.
	if ctx.Err() == nil {
		if shield.listings == nil {
			shield.listings = map[string]shieldList{}
		}
		shield.listings[socket] = listed
	}
	return listed.holders, listed.panes, listed.err
}

func parseShieldList(stdout []byte) ([]ShieldHolder, map[string]string, error) {
	var listing struct {
		Enumeration string         `json:"enumeration"`
		Error       *string        `json:"error"`
		Holders     []ShieldHolder `json:"holders"`
		Panes       []struct {
			ID     string `json:"pane_id"`
			Status string `json:"status"`
		} `json:"panes"`
	}
	if err := json.Unmarshal(stdout, &listing); err != nil {
		return nil, nil, fmt.Errorf("decode %s list --json: %w", ShieldBinary, err)
	}
	if listing.Enumeration != shieldListed {
		reason := "no error given"
		if listing.Error != nil {
			reason = *listing.Error
		}
		return nil, nil, fmt.Errorf("%s list --json: enumeration %q: %s", ShieldBinary, listing.Enumeration, reason)
	}
	panes := make(map[string]string, len(listing.Panes))
	for _, pane := range listing.Panes {
		panes[pane.ID] = pane.Status
	}
	return listing.Holders, panes, nil
}
