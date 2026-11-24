package compose

import (
	"cmp"
	"fmt"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/store"
)

const herdrBackend = "herdr"

// ContinuedSegment is the newest segment a native Claude seat's row carries
// once Claude Code continued its chat under a new session id: the row keeps
// the seat's launch id as ID and is named from that segment's transcript
// (Path). Schedules carried onto that segment, Claude Code and a resume know
// the chat by it. Empty for every other row.
func ContinuedSegment(row *Row) string {
	if row.Backend != herdrBackend || (row.Kind != LiveClaude && row.Kind != ResumeClaude) || row.Path == "" {
		return ""
	}
	if session := transcriptIDFromPath(row.Path); session != row.ID {
		return session
	}
	return ""
}

// seatLive is the state of a native seat an engine runs in.
const seatLive = "live"

// seatLost and seatFailed are the states of a native seat whose engine is gone: its chat died with no end, or
// its birth failed. Only its record (transcript, rollout) leaves anything to resume.
const (
	seatLost   = "lost"
	seatFailed = "failed"
)

// seatResumable reports whether native's seat leaves a chat to list: a live seat always does; a lost or failed
// one only while its transcript or rollout is indexed, or while the read of that record failed
// (gather.HerdrSeat.TranscriptUnread), since a lookup that could not run never reads as absence. An engine with
// no indexed record here (OpenCode, an unknown one) keeps its row.
func (current *composer) seatResumable(native *gather.HerdrSeat) bool {
	seat := &native.Seat
	if seat.State != seatLost && seat.State != seatFailed || native.TranscriptUnread {
		return true
	}
	switch pfmengine.ID(seat.Engine) {
	case pfmengine.Claude:
		_, found := current.transcriptByID[cmp.Or(native.Continued, seat.SessionID)]
		return found
	case pfmengine.Codex:
		_, found := current.rolloutByID[seat.SessionID]
		return found
	}
	return true
}

// herdrRows joins native seats before the shared kill and view eligibility pass.
// Seats are distinct addresses even when they share one transcript; they must
// not participate in tmux's newest-server collapse.
func (current *composer) herdrRows() []Row {
	rows := make([]Row, 0, len(current.input.HerdrSeats))
	for index := range current.input.HerdrSeats {
		native := &current.input.HerdrSeats[index]
		seat := &native.Seat
		if seat.Backend != herdrBackend || !current.seatResumable(native) {
			continue
		}
		row := Row{ID: seat.SessionID}
		switch pfmengine.ID(seat.Engine) {
		case pfmengine.Claude:
			// A continued chat is named from, and claims, the segment it carries now.
			session := cmp.Or(native.Continued, seat.SessionID)
			row = current.transcriptRow(current.transcriptByID[session], LiveClaude)
			current.liveTranscripts[session] = struct{}{}
		case pfmengine.Codex:
			row = current.rolloutRow(current.rolloutByID[seat.SessionID], LiveCodex)
			// A seat makes its thread's whole lineage live, as a live Codex
			// process does: a fork's lineage keeps no resume row beside it.
			current.liveRollouts[current.lineageRoot(store.Rollout{ID: seat.SessionID})] = struct{}{}
		case pfmengine.OpenCode:
			row.Kind = LiveOpenCode
			for index := range current.input.OpenCodeSessions {
				session := &current.input.OpenCodeSessions[index]
				if session.ID == seat.SessionID {
					row = current.openCodeSessionRow(*session)
					row.Kind = LiveOpenCode
					break
				}
			}
			current.liveOpenCode[seat.SessionID] = struct{}{}
		default:
			row.Kind = Booting
			row.BackendError = fmt.Sprintf("HERDR ERROR %s: unknown engine %q", seat.HerdrSocket, seat.Engine)
		}
		row.Backend = herdrBackend
		row.SeatID = seat.SeatID
		row.LostNote = seat.LostNote
		row.HerdrWorkspace = seat.HerdrWorkspace
		row.ID = seat.SessionID
		switch {
		case row.UserName != "" && pfmengine.ID(seat.Engine) != pfmengine.Codex && !naming.LabelKilled(seat.Name):
			// A Claude /rename's custom title is the newest a user chose:
			// pfm chat name delivers its /rename before it renames the seat.
			// Codex's indexed names also include automatic engine titles, so
			// they cannot override an explicit seat name. A "_KILL…" seat
			// label names the row over any engine title.
		case seat.Name != "":
			// A seat's name is the user's (launch --name, pfm chat name) and
			// names the row over the transcript's automatic one. Keep the
			// engine title in its indexed source, not in an address field.
			row.Name, row.UserName, row.UserNameKnown = seat.Name, seat.Name, true
		}
		row.Socket = seat.HerdrSocket
		row.PaneID = seat.HerdrPane
		row.SessionName = seat.Name
		// The chat's own record (transcript, rollout) names where it runs; the
		// pane's directory is its supervisor's, used only when the chat names none.
		if row.CWD == "" && native.CWD != "" {
			row.CWD = native.CWD
			row.Project = current.projects.of(native.CWD)
		}
		row.Kind = SeatKindFor(row.Kind, seat.State)
		row.Blocked = native.Blocked()
		if native.Err != nil {
			row.BackendError = native.Err.Error()
		} else if seat.State == seatLive && seat.SeatHostPID > 0 {
			// The probe proved this supervisor alive inside the pane; the
			// engine runs beneath it. The shared Herdr server is no seat's.
			row.PanePIDs = []int{seat.SeatHostPID}
		}
		rows = append(rows, row)
	}
	return rows
}
