package compose

import (
	"time"

	"hostops/pfm/internal/gather"
	"hostops/pfm/internal/naming"
	"hostops/pfm/internal/store"
)

// openCodeSessionRow renders one OpenCode session. The title is authoritative —
// OpenCode names its sessions itself — with the first prompt as fallback for
// sessions it never titled.
func (current *composer) openCodeSessionRow(session store.OpenCodeSession) Row {
	name := session.Title
	if name == "" {
		name = naming.DisplayName("", "", session.FirstPrompt)
	}
	return Row{
		Kind:           ResumeOpenCode,
		ID:             session.ID,
		Name:           name,
		Project:        projectName(session.ProjectDir),
		CWD:            firstNonEmpty(session.Directory, session.ProjectDir),
		PromptCount:    session.PromptCount,
		AssistantCount: session.AssistantCount,
		ActivityNS:     session.TimeUpdatedMS * int64(time.Millisecond),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// liveOpenCodeRows builds one row per OpenCode pane gather found running an
// OpenCode process — the OpenCode twin of liveCodexRows.
//
// An IDENTIFIED seat starts from its indexed session row so the live row
// carries the same id, name and counters the resumable row would have, and
// marks that session live so the resume pass skips it: one chat, one row.
// An UNIDENTIFIED seat is still a live seat — it answers to its socket name
// and wears the name OpenCode wrote into its own terminal title. Refusing to
// list it would be the original defect in a new costume: a chat that plainly
// exists, reported as absent.
func (current *composer) liveOpenCodeRows() []Row {
	seats := current.input.Snapshot.OpenCode
	if len(seats) == 0 {
		return nil
	}
	sessionByID := make(map[string]store.OpenCodeSession, len(current.input.OpenCodeSessions))
	for index := range current.input.OpenCodeSessions {
		session := current.input.OpenCodeSessions[index]
		sessionByID[session.ID] = session
	}
	rows := make([]Row, 0, len(seats))
	for _, seat := range seats {
		pane, paneFound := current.paneByTarget[targetKey(seat.Socket, seat.PaneID)]
		if !paneFound {
			// A responsive ox-* server is not a live OpenCode chat: the
			// process must resolve to a pane in THIS gather snapshot, or its
			// indexed session stays resumable (liveCodexRows, same rule).
			continue
		}
		rows = append(rows, current.liveOpenCodeRow(seat, pane, sessionByID))
	}
	return rows
}

func (current *composer) liveOpenCodeRow(
	seat gather.LiveOpenCode,
	pane gather.ProbePane,
	sessionByID map[string]store.OpenCodeSession,
) Row {
	row := Row{Kind: LiveOpenCode, ID: seat.Socket}
	if session, found := sessionByID[seat.SessionID]; found {
		row = current.openCodeSessionRow(session)
		row.Kind = LiveOpenCode
		current.liveOpenCode[session.ID] = struct{}{}
	}
	row.Socket = seat.Socket
	row.PaneID = seat.PaneID
	row.PanePIDs = []int{seat.PanePID}
	row.SessionName = pane.SessionName
	row.WindowName = pane.WindowName
	row.ServerCount = 1
	row.Attached = pane.Attached
	row.Here = seat.Socket == current.input.Options.CurrentSocket
	_, row.C1H = current.cacheSockets[seat.Socket]
	if row.CWD == "" {
		row.CWD = firstNonEmpty(seat.CWD, pane.CurrentPath)
		row.Project = projectName(row.CWD)
	}
	if row.Name == "" {
		row.Name = firstNonEmpty(
			gather.OpenCodePaneName(seat.PaneTitle),
			pane.SessionName,
			seat.Socket,
		)
	}
	if row.ActivityNS == 0 {
		row.ActivityNS = socketEpochNS(seat.Socket)
	}
	return row
}
