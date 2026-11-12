//go:build journey && linux

package journey

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// The holder door, Linux side: herdr-shield holds every Herdr pane in a holder of its own, and every
// read or command on holders goes through this file (shield_darwin_test.go is the Mac's side).

// holder is one live herdr-shield holder.
type holder struct {
	ID, Pane, Socket string
	Pid              int
}

// liveHolders is herdr-shield's list of live holders bound to the Herdr socket sock.
func liveHolders(c *harness.Ctx, sock string) []holder {
	var live []holder
	for _, h := range shieldHolders(c, sock) {
		if h.Socket == sock {
			live = append(live, h)
		}
	}
	return live
}

// shieldHolders is every live herdr-shield holder on the machine, whatever socket binds it, as
// herdr-shield reports them through the running Herdr server on via (it refuses to list without one).
func shieldHolders(c *harness.Ctx, via string) []holder {
	var list struct {
		Holders []struct {
			ID      string `json:"id"`
			Live    bool   `json:"holder_live"`
			Binding struct {
				Pane   string `json:"pane"`
				Socket string `json:"socket"`
			} `json:"binding"`
			Holder struct {
				Pid int `json:"pid"`
			} `json:"holder"`
		} `json:"holders"`
	}
	out := read(c, "env", "HERDR_SOCKET_PATH="+via, "herdr-shield", "list", "--json")
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		c.Failf("herdr-shield list --json: %v:\n%s", err, out)
	}
	var live []holder
	for _, h := range list.Holders {
		if h.Live {
			live = append(live, holder{ID: h.ID, Pane: h.Binding.Pane, Socket: h.Binding.Socket, Pid: h.Holder.Pid})
		}
	}
	return live
}

// paneHolder waits for the live holder herdr-shield binds to the branch's pane and returns its id.
func paneHolder(c *harness.Ctx, s State) string {
	id := ""
	c.Poll("a live herdr-shield holder on pane "+s.Pane, stepBound, func() (bool, string) {
		holders := liveHolders(c, herdrSocket(s))
		for _, h := range holders {
			if h.Pane == s.Pane {
				id = h.ID
				return true, ""
			}
		}
		return false, fmt.Sprintf("%+v", holders)
	})
	return id
}

// heldByPane reports whether pid runs under the branch's pane holder, and what was seen.
func heldByPane(c *harness.Ctx, s State, table map[int]process, pid int) (bool, string) {
	holderPid := 0
	for _, h := range liveHolders(c, herdrSocket(s)) {
		if h.ID == s.Holder {
			holderPid = h.Pid
		}
	}
	return holderPid != 0 && under(table, pid, holderPid), fmt.Sprintf("holder %s pid %d", s.Holder, holderPid)
}

// settleHolders waits until every pane of the branch's Herdr session is held and every live holder on
// its socket holds one of them: an ended chat's pane comes back on a new holder a moment later, and a
// holder killed before it registers would outlive the session.
func settleHolders(c *harness.Ctx, s State) {
	sock := herdrSocket(s)
	c.Poll("every pane of "+sock+" held, every holder on it on a pane", 20*time.Second, func() (bool, string) {
		panes, holders := herdrPanes(c, s), liveHolders(c, sock)
		held := map[string]bool{}
		for _, h := range holders {
			if _, ok := panes[h.Pane]; !ok {
				return false, fmt.Sprintf("holder %s on pane %s, gone from panes %v", h.ID, h.Pane, panes)
			}
			held[h.Pane] = true
		}
		return len(held) == len(panes), fmt.Sprintf("panes %v, holders %+v", panes, holders)
	})
}

// killHolders kills every live holder bound to the Herdr socket sock through run, with the owner's
// `herdr-shield kill`, and returns the holders it found.
func killHolders(c *harness.Ctx, sock string, run func(argv ...string)) []holder {
	holders := liveHolders(c, sock)
	for _, h := range holders {
		run("env", "HERDR_SOCKET_PATH="+sock, "herdr-shield", "kill", h.ID, "--confirm", h.ID)
	}
	return holders
}

// holderEvidence is the read a failed step's evidence keeps of herdr-shield's holders.
func holderEvidence() map[string][]string {
	return map[string][]string{
		"holders.txt": {
			"env",
			"HERDR_SOCKET_PATH=" + herdrSocket(State{Home: ownerHome}),
			"herdr-shield",
			"list",
			"--json",
		},
	}
}
