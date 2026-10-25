package compose

import (
	"fmt"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/naming"
)

const herdrBackend = "herdr"

// herdrRows joins native seats before the shared kill and view eligibility pass.
// Seats are distinct addresses even when they share one transcript; they must
// not participate in tmux's newest-server collapse.
func (current *composer) herdrRows() []Row {
	rows := make([]Row, 0, len(current.input.HerdrSeats))
	for index := range current.input.HerdrSeats {
		native := &current.input.HerdrSeats[index]
		seat := &native.Seat
		if seat.Backend != herdrBackend {
			continue
		}
		row := Row{ID: seat.SessionID}
		switch pfmengine.ID(seat.Engine) {
		case pfmengine.Claude:
			row = current.transcriptRow(current.transcriptByID[seat.SessionID], LiveClaude)
			current.liveTranscripts[seat.SessionID] = struct{}{}
		case pfmengine.Codex:
			row = current.rolloutRow(current.rolloutByID[seat.SessionID], LiveCodex)
			current.liveRollouts[seat.SessionID] = struct{}{}
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
		if native.CWD != "" {
			row.CWD = native.CWD
			row.Project = current.projects.of(native.CWD)
		}
		if seat.State != "live" {
			row.Kind = ResumeKindFor(row.Kind)
		}
		if native.Err != nil {
			row.BackendError = native.Err.Error()
		} else if seat.State == "live" && seat.SeatHostPID > 0 {
			// The probe proved this supervisor alive inside the pane; the
			// engine runs beneath it. The shared Herdr server is no seat's.
			row.PanePIDs = []int{seat.SeatHostPID}
		}
		rows = append(rows, row)
	}
	return rows
}
