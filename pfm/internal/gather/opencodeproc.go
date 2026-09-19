package gather

import (
	"fmt"
	"sort"
	"strings"

	pfmengine "hostops/pfm/internal/engine"
)

// OpenCodePaneTitlePrefix is what OpenCode's own terminal-title escape puts in
// front of a session title, which tmux's automatic-rename then copies into the
// pane title ("OC | P:OPENCODE"). No pfm code writes it — it is read here and
// nowhere else.
const OpenCodePaneTitlePrefix = "OC | "

// openCodeBirthSlackMS is how far BEFORE its socket's birth second a session
// may have been created and still be the one that socket's TUI opened: the
// session row is written by the engine as it starts, and pfm mints the socket
// name a moment earlier — but clocks, the launch itself, and the second-
// granularity socket stamp all blur the boundary.
const openCodeBirthSlackMS = 5_000

// OpenCodeSession is the subset of an indexed OpenCode session the live-pane
// detector needs. It is declared HERE rather than imported from store so the
// probe layer keeps no dependency on the SQLite layer; fleet maps the store
// rows onto it at the one call site.
type OpenCodeSession struct {
	ID            string
	Title         string
	Directory     string
	TimeCreatedMS int64
}

// DetectOpenCode maps running OpenCode processes onto the ox- panes that host
// them — the OpenCode twin of DetectCodex, and the only way a running OpenCode
// TUI becomes a live seat instead of its own resume row.
//
// A pane qualifies only when BOTH halves hold: its socket carries OpenCode's
// registered prefix, AND the pane's own process tree runs the OpenCode binary.
// A pane on an ox- socket whose engine process has exited is a shell sitting
// in a chat's corpse, not a chat, and is deliberately absent from the result.
func DetectOpenCode(
	proc ProcFS,
	panes []ProbePane,
	sessions []OpenCodeSession,
	binaries ...string,
) ([]LiveOpenCode, error) {
	cmdlines, err := processCmdlines(proc)
	if err != nil {
		return nil, fmt.Errorf("list processes for OpenCode scan: %w", err)
	}
	return detectOpenCodeFrom(cmdlines, proc, panes, sessions, binaries...)
}

// detectOpenCodeFrom is DetectOpenCode over an already-fetched pid->cmdline
// snapshot — see processCmdlines.
func detectOpenCodeFrom(
	cmdlines map[int][]string,
	proc ProcFS,
	panes []ProbePane,
	sessions []OpenCodeSession,
	binaries ...string,
) ([]LiveOpenCode, error) {
	paneByPID := panesByPID(openCodePanes(panes))
	seats := make([]LiveOpenCode, 0)
	claimedPane := make(map[string]struct{}, len(paneByPID))
	for _, pid := range sortedPIDs(cmdlines) {
		if !IsOpenCodeCommand(cmdlines[pid], binaries...) {
			continue
		}
		pane, found := paneForProcess(proc, pid, paneByPID)
		if !found {
			continue
		}
		key := pane.Socket + "\x00" + pane.PaneID
		if _, duplicate := claimedPane[key]; duplicate {
			// One pane hosts one chat. A second OpenCode process in the same
			// tree (a spawned child, a wrapper) must not earn a second row.
			continue
		}
		claimedPane[key] = struct{}{}
		seats = append(seats, LiveOpenCode{
			Socket:      pane.Socket,
			SessionName: pane.SessionName,
			PaneID:      pane.PaneID,
			PID:         pid,
			PanePID:     pane.PID,
			CWD:         pane.CurrentPath,
			PaneTitle:   pane.PaneTitle,
		})
	}
	// Stable socket-name order is the claim order: identification hands each
	// session to at most one pane, so the order has to be a property of the
	// fleet, never of the /proc walk.
	sort.Slice(seats, func(left, right int) bool {
		if seats[left].Socket != seats[right].Socket {
			return seats[left].Socket < seats[right].Socket
		}
		return seats[left].PaneID < seats[right].PaneID
	})
	identifyOpenCodeSeats(seats, sessions, cmdlines)
	return seats, nil
}

// openCodePanes keeps only the panes whose socket belongs to OpenCode.
func openCodePanes(panes []ProbePane) []ProbePane {
	kept := make([]ProbePane, 0, len(panes))
	for index := range panes {
		pane := panes[index]
		if id, known := pfmengine.FromSocket(pane.Socket); known && id == pfmengine.OpenCode {
			kept = append(kept, pane)
		}
	}
	return kept
}

// IsOpenCodeCommand reports whether an argv belongs to an OpenCode process.
func IsOpenCodeCommand(cmdline []string, binaries ...string) bool {
	return pfmengine.MatchCommand(pfmengine.OpenCode, cmdline, false, binaries...)
}

// OpenCodePaneName is the session name a live OpenCode pane title carries:
// the title minus OpenCode's own "OC | " prefix. A title without the prefix is
// returned whole — the prefix is the engine's convention, not a guarantee.
func OpenCodePaneName(title string) string {
	return strings.TrimPrefix(title, OpenCodePaneTitlePrefix)
}

// identifyOpenCodeSeats walks the identification ladder over seats in place.
//
// OpenCode exports no session variable and holds no session file descriptor,
// so there is no single authoritative signal — only three weaker ones, tried
// strongest first. Each rung answers ONLY when it names exactly one unclaimed
// session: two candidates is an ambiguity, and an ambiguity is neither an
// error nor a pick, it is a reason to ask the next rung. A seat that reaches
// the end unidentified is STILL a live seat; it simply answers to its socket
// rather than to a session id.
func identifyOpenCodeSeats(seats []LiveOpenCode, sessions []OpenCodeSession, cmdlines map[int][]string) {
	claimed := make(map[string]struct{}, len(seats))
	byID := make(map[string]struct{}, len(sessions))
	for _, session := range sessions {
		byID[session.ID] = struct{}{}
	}
	for index := range seats {
		seat := &seats[index]
		for _, rung := range []func() string{
			func() string { return openCodeTitleMatch(*seat, sessions, claimed) },
			func() string { return openCodeArgvMatch(cmdlines[seat.PID], byID, claimed) },
			func() string { return openCodeBirthMatch(*seat, sessions, claimed) },
		} {
			if id := rung(); id != "" {
				seat.SessionID = id
				claimed[id] = struct{}{}
				break
			}
		}
	}
}

// openCodeTitleMatch is rung 1: the pane title IS the session title, and the
// pane's directory is the session's directory.
func openCodeTitleMatch(
	seat LiveOpenCode,
	sessions []OpenCodeSession,
	claimed map[string]struct{},
) string {
	title := OpenCodePaneName(seat.PaneTitle)
	if title == "" || seat.CWD == "" {
		return ""
	}
	return theOneUnclaimed(sessions, claimed, func(session OpenCodeSession) bool {
		return session.Title == title && session.Directory == seat.CWD
	})
}

// openCodeArgvMatch is rung 2: the process says outright which session it
// resumed (`opencode --session <id>`).
func openCodeArgvMatch(cmdline []string, byID, claimed map[string]struct{}) string {
	id := openCodeSessionArgv(cmdline)
	if id == "" {
		return ""
	}
	if _, known := byID[id]; !known {
		return ""
	}
	if _, taken := claimed[id]; taken {
		return ""
	}
	return id
}

// openCodeBirthMatch is rung 3: the socket name carries the second it was
// minted, and the session this TUI created was written within a breath of it.
func openCodeBirthMatch(
	seat LiveOpenCode,
	sessions []OpenCodeSession,
	claimed map[string]struct{},
) string {
	birth := pfmengine.SocketBirth(seat.Socket)
	if birth <= 0 || seat.CWD == "" {
		return ""
	}
	floor := birth*1_000 - openCodeBirthSlackMS
	return theOneUnclaimed(sessions, claimed, func(session OpenCodeSession) bool {
		return session.Directory == seat.CWD && session.TimeCreatedMS >= floor
	})
}

// theOneUnclaimed returns the single unclaimed session matching keep, or "" —
// for "nothing matched" AND for "more than one did". Both are the same answer
// to the caller: this rung cannot name the seat, ask the next one.
func theOneUnclaimed(
	sessions []OpenCodeSession,
	claimed map[string]struct{},
	keep func(OpenCodeSession) bool,
) string {
	found := ""
	for _, session := range sessions {
		if session.ID == "" || !keep(session) {
			continue
		}
		if _, taken := claimed[session.ID]; taken {
			continue
		}
		if found != "" {
			return ""
		}
		found = session.ID
	}
	return found
}

// openCodeSessionArgv reads the session an OpenCode process was launched to
// resume. It mirrors action.Synthesize's own resume line (`--session <id>`)
// and accepts the short and joined spellings a human may have typed.
func openCodeSessionArgv(cmdline []string) string {
	for index := 1; index < len(cmdline); index++ {
		argument := cmdline[index]
		switch {
		case argument == "--session" || argument == "-s":
			if index+1 < len(cmdline) {
				return cmdline[index+1]
			}
		case strings.HasPrefix(argument, "--session="):
			return strings.TrimPrefix(argument, "--session=")
		}
	}
	return ""
}
