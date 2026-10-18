package ui

import (
	"fmt"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/spawn"
)

// deactivate stops a live chat and leaves the cursor on the conversation just
// deactivated: its row stays in place as the resumable row it becomes, so Enter
// resumes it rather than the neighbour that would slide under the cursor.
func (model *Model) deactivate(row compose.Row) {
	if model.applyDeactivate == nil {
		model.killStatus = "deactive refused — this picker cannot stop chats"
		return
	}
	if err := model.applyDeactivate(row); err != nil {
		model.killStatus = fmt.Sprintf("deactive failed — %s: %v", row.Name, err)
		return
	}
	model.deactivatedSockets[liveSeatKey(row)] = true
	model.killStatus = "deactivated — " + row.Name + " — ready to resume"
	key := compose.RowKey(row)
	fallback := model.cursor
	// A split window with no conversation id has nothing to resume and goes.
	resumable := row.ID != "" && row.Kind != compose.LiveSplit
	demoted := deactivatedRow(row)
	if resumable {
		model.deactivatedRows[liveSeatKey(row)] = demoted
		if row.Backend == spawn.BackendHerdr {
			model.nativeEndSeats[liveSeatKey(row)] = true
		}
	}
	kept := model.rows[:0]
	for index := range model.rows {
		candidate := &model.rows[index]
		if compose.RowKey(*candidate) != key {
			kept = append(kept, *candidate)
		} else if resumable {
			kept = append(kept, demoted)
		}
	}
	model.rows = kept
	model.actionIndex = 0
	if resumable {
		model.rebuild(key, fallback)
		return
	}
	model.rebuild("", fallback)
}

// deactivatedRow is the row of a conversation whose live seat was just
// stopped: the resumable kind of its own engine, with every live-seat address
// cleared — the shape a real roster gives it once the seat is gone.
func deactivatedRow(row compose.Row) compose.Row {
	row.Kind = compose.ResumeKindFor(row.Kind)
	row.Backend = ""
	row.SeatID = ""
	row.Socket = ""
	row.PaneID = ""
	row.SessionName = ""
	row.WindowName = ""
	return row
}

// nativeEndPending reports whether row is the resumable row of a native seat
// this picker deactivated while the roster still lists that seat live: the
// native stop only scheduled a detached end worker, so a resume now would
// start the conversation again beside its still-running engine.
func (model *Model) nativeEndPending(row compose.Row) bool {
	if row.Kind.IsLiveSeat() {
		return false
	}
	key := compose.RowKey(row)
	for seat := range model.nativeEndSeats {
		if model.deactivatedSockets[seat] && compose.RowKey(model.deactivatedRows[seat]) == key {
			return true
		}
	}
	return false
}

// settleDeactivated applies the picker's deactivations to a refresh's rows: a
// seat the roster no longer lists live is forgotten, and a stale live row of a
// deactivated seat is replaced by the resumable row it became (unless the
// snapshot already carries that conversation's row), so a refresh neither
// revives the stopped pane nor moves the cursor off its conversation. It
// reports whether it changed the rows.
func (model *Model) settleDeactivated(rows []compose.Row) ([]compose.Row, bool) {
	if len(model.deactivatedSockets) == 0 {
		return rows, false
	}
	live := trackedChatSockets(rows)
	for socket := range model.deactivatedSockets {
		if !live[socket] {
			delete(model.deactivatedSockets, socket)
			delete(model.deactivatedRows, socket)
			delete(model.nativeEndSeats, socket)
		}
	}
	present := make(map[string]bool, len(rows))
	for index := range rows {
		if !rows[index].Kind.IsLiveSeat() {
			present[compose.RowKey(rows[index])] = true
		}
	}
	adjusted := false
	settled := make([]compose.Row, 0, len(rows))
	for index := range rows {
		row := &rows[index]
		if row.Kind.IsLiveSeat() && model.deactivatedSockets[liveSeatKey(*row)] {
			adjusted = true
			demoted, kept := model.deactivatedRows[liveSeatKey(*row)]
			if kept && !present[compose.RowKey(demoted)] {
				present[compose.RowKey(demoted)] = true
				settled = append(settled, demoted)
			}
			continue
		}
		settled = append(settled, *row)
	}
	return settled, adjusted
}
