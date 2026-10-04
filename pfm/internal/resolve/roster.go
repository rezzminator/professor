package resolve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/naming"
)

// RosterCandidate is one composed fleet row projected onto the fields needed
// to resolve a human target name. Callers keep composition in their own layer;
// this package owns the matching judgment shared by CLI and MCP.
type RosterCandidate struct {
	Name       string
	ID         string
	Socket     string
	SocketPath string
	Session    string
	Pane       string
	Engine     string
	// Live is a seat that answers right now: addressable and not killed. A
	// killed row has given its name up, so it never outranks a live one.
	Live bool
	// ActivityNS is the row's last activity; among dead-only matches the
	// newest one is the chat a name means.
	ActivityNS int64
}

// RosterAmbiguityError is a refusal, not a probe failure. The candidates are
// complete enough for a caller to retry with the stable thread id.
type RosterAmbiguityError struct {
	Name       string
	Candidates []RosterCandidate
}

func (failure *RosterAmbiguityError) Error() string {
	lines := make([]string, 0, len(failure.Candidates))
	for _, candidate := range failure.Candidates {
		id := candidate.ID
		if id == "" {
			id = "<unknown>"
		}
		socket := candidate.Socket
		if socket == "" {
			socket = SessionName(candidate.SocketPath)
		}
		lines = append(lines, fmt.Sprintf(
			"thread id %s (socket %s, pane %s, name %q)",
			id, socket, candidate.Pane, candidate.Name,
		))
	}
	sort.Strings(lines)
	return fmt.Sprintf(
		"%q matches %d chats in the roster; address by thread id:\n  %s",
		failure.Name, len(failure.Candidates), strings.Join(lines, "\n  "),
	)
}

// ResolveRosterName applies the fleet's one name/id/socket matching rule.
// Exact matches outrank folded names and ID prefixes. Within the winning rung
// a live seat outranks every dead row: one live seat is the answer (a live
// row and its own resume row are one conversation, and a killed chat has given
// its name up), several live seats are a genuine collision refused as
// ambiguous. With no live seat the newest dead row is the answer — the caller
// sees Live false and reports it dead; only dead rows tied for newest stay
// ambiguous, every one of them listed.
func ResolveRosterName(
	candidates []RosterCandidate,
	name string,
) (RosterCandidate, bool, error) {
	exact := make([]RosterCandidate, 0, 2)
	folded := make([]RosterCandidate, 0, 2)
	session := SessionName(name)
	for _, candidate := range candidates {
		socket := candidate.Socket
		if socket == "" {
			socket = candidate.SocketPath
		}
		switch {
		case candidate.ID == name ||
			(session != "" && SessionName(socket) == session) ||
			candidate.Name == name:
			exact = append(exact, candidate)
		case strings.EqualFold(candidate.Name, name) ||
			(len(name) >= 8 && strings.HasPrefix(candidate.ID, name)):
			folded = append(folded, candidate)
		}
	}
	matches := exact
	if len(matches) == 0 {
		matches = folded
	}
	if len(matches) == 0 {
		return RosterCandidate{}, false, nil
	}
	if len(matches) > 1 {
		matches = preferredRosterMatches(matches)
	}
	if len(matches) > 1 {
		return RosterCandidate{}, false, &RosterAmbiguityError{
			Name: name, Candidates: append([]RosterCandidate(nil), matches...),
		}
	}
	return matches[0], true, nil
}

// preferredRosterMatches narrows one rung's matches to the rows a name means:
// the live seats when any exist, else the dead rows tied for the newest
// activity.
func preferredRosterMatches(matches []RosterCandidate) []RosterCandidate {
	live := make([]RosterCandidate, 0, len(matches))
	for index := range matches {
		if matches[index].Live {
			live = append(live, matches[index])
		}
	}
	if len(live) > 0 {
		return live
	}
	newest := make([]RosterCandidate, 0, 1)
	for index := range matches {
		activity := matches[index].ActivityNS
		switch {
		case len(newest) == 0 || activity > newest[0].ActivityNS:
			newest = append(newest[:0], matches[index])
		case activity == newest[0].ActivityNS:
			newest = append(newest, matches[index])
		}
	}
	return newest
}

// ResolveRosterSeat is ResolveRosterName read backwards: given a chat's own
// live seat, what does the roster call it? The answer is the one string a
// peer can hand straight back to ResolveRosterName's exact-name rung, which
// is why the footer inject stamps on a delivery asks here first and never
// advertises a tmux session — a chat knows names, and the roster turns names
// into panes.
//
// The thread id is authoritative when the identity carries one. The seat —
// socket plus pane — is the match otherwise: an ancestry-recovered or
// OpenCode sender knows where it sits before it knows its id. Only live rows
// answer, and several rows may host one chat (a label shown ⚠2srv); they
// answer as one while they agree on the name. A machine-shaped name or the
// unnamed sentinel is nothing a peer could reply to, so it is reported as no
// name at all rather than as a name.
func ResolveRosterSeat(candidates []RosterCandidate, identity Identity) (string, bool) {
	socket := identity.SocketName
	if socket == "" {
		socket = SessionName(identity.SocketPath)
	}
	name, found := "", false
	for _, candidate := range candidates {
		if !candidate.Live || !seatMatches(candidate, identity, socket) {
			continue
		}
		label := strings.TrimSpace(candidate.Name)
		if label == "" || label == naming.Unnamed || Named(label).String() != label {
			continue
		}
		if found && name != label {
			return "", false
		}
		name, found = label, true
	}
	return name, found
}

func seatMatches(candidate RosterCandidate, identity Identity, socket string) bool {
	if identity.ID != "" {
		return candidate.ID == identity.ID
	}
	candidateSocket := candidate.Socket
	if candidateSocket == "" {
		candidateSocket = candidate.SocketPath
	}
	if socket == "" || SessionName(candidateSocket) != socket {
		return false
	}
	if identity.Pane != "" {
		return candidate.Pane == identity.Pane
	}
	return candidate.Session == identity.Session
}
