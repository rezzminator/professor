//go:build journey && darwin

package journey

import (
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// The holder door, macOS side: herdr-shield holds panes only on Linux, so a Mac's owner has no
// herdr-shield and a pane has no holder. What is true here: the pane's shell and its engine run under
// Herdr's own process (shield_linux_test.go is the Linux side).

// holder is one live herdr-shield holder; a Mac has none.
type holder struct {
	ID, Pane, Socket string
	Pid              int
}

// liveHolders: no holder binds a socket on a Mac.
func liveHolders(*harness.Ctx, string) []holder { return nil }

// shieldHolders: no holder runs on a Mac.
func shieldHolders(*harness.Ctx, string) []holder { return nil }

// paneHolder: a Mac's pane has no holder, so its id is empty.
func paneHolder(*harness.Ctx, State) string { return "" }

// heldByPane reports whether pid runs under a Herdr process, the pane's owner on a Mac.
func heldByPane(_ *harness.Ctx, _ State, table map[int]process, pid int) (bool, string) {
	for seen := 0; pid > 1 && seen < 64; seen++ {
		pid = table[pid].Ppid
		if f := strings.Fields(table[pid].Args); len(f) > 0 && filepath.Base(f[0]) == "herdr" {
			return true, "herdr pid " + table[pid].Args
		}
	}
	return false, "no herdr process above it"
}

// settleHolders: no holder to wait for on a Mac.
func settleHolders(*harness.Ctx, State) {}

// killHolders: nothing to kill on a Mac.
func killHolders(*harness.Ctx, string, func(argv ...string)) []holder { return nil }

// holderEvidence: no holder read on a Mac.
func holderEvidence() map[string][]string { return nil }
