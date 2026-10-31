package fleet

import (
	"path/filepath"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/store"
)

// followContinuations makes one chat of every Claude conversation that moved
// sessions. Claude Code sends a running chat to a background job under a new
// session id and appends a continued-in record to the old transcript, while
// the host keeps the id it launched with: the pane's crumb, a --resume argv,
// every live pointer pfm reads still names the old segment. Composed as they
// stand, the pane is labeled with the old name while it shows the successor,
// and the successor — held by nothing pfm can see — is offered for resume,
// which starts a second process on a session already running.
//
// So every crumb and agent is re-pointed at the segment that carries its chat
// now, and the superseded segments leave the data compose is given. This is
// the one place both halves are in hand: ComposeFleet is compose's only door.
// The store's transcriptSupersededSQL drops the same segments from the
// cached first frame, so the two frames agree.
//
// A native seat keeps its launch id as its identity, so it is not re-pointed:
// it is marked Continued with the segment it carries, and compose names it
// from that segment and claims it, so its own re-pointed process is not
// offered again as a detached agent. A seat whose host is gone carries the
// chat only while no live crumb or agent holds that segment.
func followContinuations(
	data Data,
	live gather.Snapshot,
	native []gather.HerdrSeat,
) (Data, gather.Snapshot, []gather.HerdrSeat) {
	byID := make(map[string]store.Transcript, len(data.Transcripts))
	superseded := 0
	for index := range data.Transcripts {
		transcript := data.Transcripts[index]
		byID[transcript.UUID] = transcript
		if transcript.Superseded {
			superseded++
		}
	}
	if superseded == 0 {
		return data, live, native
	}

	if len(live.Crumbs) > 0 {
		crumbs := make([]gather.Crumb, len(live.Crumbs))
		for index, crumb := range live.Crumbs {
			id := strings.TrimSuffix(filepath.Base(crumb.TranscriptPath), filepath.Ext(crumb.TranscriptPath))
			if tail := continuationTail(byID, id); tail != id {
				crumb.TranscriptPath = byID[tail].Path
			}
			crumbs[index] = crumb
		}
		live.Crumbs = crumbs
	}
	if len(live.Agents) > 0 {
		agents := make([]gather.Agent, len(live.Agents))
		for index, agent := range live.Agents {
			agent.SessionID = continuationTail(byID, agent.SessionID)
			agents[index] = agent
		}
		live.Agents = agents
	}

	if len(native) > 0 {
		held := make(map[string]struct{}, len(live.Crumbs)+len(live.Agents))
		for _, crumb := range live.Crumbs {
			held[strings.TrimSuffix(filepath.Base(crumb.TranscriptPath), filepath.Ext(crumb.TranscriptPath))] = struct{}{}
		}
		for _, agent := range live.Agents {
			held[agent.SessionID] = struct{}{}
		}
		seats := append([]gather.HerdrSeat(nil), native...)
		for index := range seats {
			seat := &seats[index]
			if pfmengine.ID(seat.Seat.Engine) == pfmengine.Claude {
				tail := continuationTail(byID, seat.Seat.SessionID)
				_, taken := held[tail]
				if tail != seat.Seat.SessionID && (seat.Seat.State == liveSeat || !taken) {
					seat.Continued = tail
				}
			}
		}
		native = seats
	}

	transcripts := make([]store.Transcript, 0, len(data.Transcripts)-superseded)
	for index := range data.Transcripts {
		if !data.Transcripts[index].Superseded {
			transcripts = append(transcripts, data.Transcripts[index])
		}
	}
	data.Transcripts = transcripts
	return data, live, native
}

// continuationTail follows continued-in handoffs from id to the newest segment
// the data holds. It stops at a successor that was never loaded — the store
// only marks a segment superseded when its successor is indexed, and
// EnrichLive loads every live chat's chain — and at any cycle.
func continuationTail(byID map[string]store.Transcript, id string) string {
	seen := map[string]struct{}{id: {}}
	for {
		next := byID[id].ContinuedIn
		if next == "" {
			return id
		}
		if _, loaded := byID[next]; !loaded {
			return id
		}
		if _, cycle := seen[next]; cycle {
			return id
		}
		seen[next] = struct{}{}
		id = next
	}
}
