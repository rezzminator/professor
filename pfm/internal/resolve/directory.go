package resolve

import (
	"path/filepath"
	"strings"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// This file is the fleet's chat DNS: you DIAL a chat by an address and you
// SPEAK about it by a name, and neither half is any caller's business to
// re-derive. The tmux socket name stays exactly what it is — tmux is
// addressed by it and by nothing else — but it stops being something a
// caller, a recorded event, or a human has to hold.
//
// The Resolver above answers "which live pane is this?" by asking tmux.
// Directory below answers the other question, over a snapshot the caller
// already has and with no I/O at all: "which chat is this identity?" Both
// belong here because they share one address space, and that address space
// having lived in neither package is what let the cosmos graph grow two
// divergent lookup ladders, one per side of a conversation.

// DisplayName is the only string any surface may present to a human as a
// chat's name.
//
// It is a struct with an unexported field on purpose. Outside this package
// there is no literal that produces one, so a tmux session name, a socket
// path, or a chat UUID cannot be assigned into a name position by accident —
// which is precisely how a live chat came to be rendered as
// "cc-1787705979-3980493-30867". There are exactly two doors: Named, for text
// that IS a human name, and Directory.Lookup, which decides between a chat's
// own label and a visibly-unresolved rendering.
//
// The zero value renders as the empty string, which every caller already
// treats as "no node here".
type DisplayName struct{ text string }

// String is how a renderer reads the name. It is the only accessor, so a
// grep for ".Label.String()" enumerates every surface that shows one.
func (name DisplayName) String() string { return name.text }

// Named wraps text that IS a human name — a 🔖 label an operator typed, a
// chat-group name they created. It is deliberately named so that handing it
// a session name or a socket path reads as the lie it would be, and it
// refuses that case outright rather than trusting the caller: see
// isMachineAddress.
func Named(text string) DisplayName {
	return nameFor(Address{Label: text}, true)
}

// Address is every alias one chat answers to. A caller fills in the fields it
// actually knows the flavour of, and drops anything it does not into Text.
//
// The session name is the STORED key throughout the fleet — the comms ledger
// is append-only, chat_name renames a chat at will, and a freed label is
// reused by a later chat — so labels here are input and display ONLY, never
// an identity anything persists.
type Address struct {
	// ID is the chat's transcript / thread UUID.
	ID string
	// Session is a tmux session name. A full socket PATH is accepted and
	// reduced to its bare name, because the fleet records both shapes: a
	// live row carries the bare name tmux is addressed by (-L), while
	// inject and spawn both record the full -S path.
	Session string
	// Pane is a tmux pane id ("%0"). It identifies a chat only together
	// with Session: pane ids are unique per tmux SERVER, not globally.
	Pane string
	// Label is the human 🔖 name. It MAY contain ':', which is tmux's own
	// session:window separator, so a label is never safe to interpolate
	// into a tmux target.
	Label string
	// Text is an identity a caller supplied without saying which namespace
	// it belongs to — the `chat_inject <target>` argument, the comms
	// ledger's target column. Resolution tries it in every namespace.
	Text string
}

// SessionName reduces a tmux socket PATH or a bare session name to the bare
// session name. It is the single implementation of that reduction; a
// filepath.Base scattered across call sites is how the two halves of one
// conversation ended up keyed differently.
func SessionName(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || !strings.ContainsRune(trimmed, filepath.Separator) {
		return trimmed
	}
	base := filepath.Base(trimmed)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}

// Canonical normalises an address into the form the index is keyed by.
func (address Address) Canonical() Address {
	address.ID = strings.TrimSpace(address.ID)
	address.Session = SessionName(address.Session)
	address.Pane = strings.TrimSpace(address.Pane)
	address.Label = strings.TrimSpace(address.Label)
	address.Text = strings.TrimSpace(address.Text)
	return address
}

// Answer is one lookup's complete verdict.
type Answer[T any] struct {
	// Chat is the matching entry, valid only when Found.
	Chat T
	// Found separates "a chat answers to this" from "nothing does". It is
	// never inferred from Display being non-empty: an unresolved identity
	// still renders, precisely so its failure is visible.
	Found bool
	// Address is the canonical identity this answer is ABOUT: the found
	// chat's own address when Found, otherwise the query's.
	Address Address
	// Display is the human-facing name. It is the only field a renderer
	// may show.
	Display DisplayName
}

// Directory is an alias index over one snapshot of chats. It performs no I/O,
// remembers nothing between calls, and is the single entry point from any
// identity shape to the chat that holds it.
type Directory[T any] struct {
	entries   []T
	addresses []Address
	byAlias   map[string]int
}

// NewDirectory indexes entries by every alias each of them answers to.
// addressOf projects one entry into the address space; it is the only place a
// caller's own record shape is known, which is why the directory is generic
// rather than copying fields into a parallel struct that could drift.
//
// On a collision the first entry wins, matching the pre-existing cosmos
// behaviour: rows arrive newest-first, and a stale duplicate must not
// displace the live chat.
func NewDirectory[T any](entries []T, addressOf func(T) Address) *Directory[T] {
	directory := &Directory[T]{
		entries:   entries,
		addresses: make([]Address, len(entries)),
		byAlias:   make(map[string]int, len(entries)*4),
	}
	for index, entry := range entries {
		address := addressOf(entry).Canonical()
		directory.addresses[index] = address
		for _, alias := range registrationAliases(address) {
			if _, taken := directory.byAlias[alias]; !taken {
				directory.byAlias[alias] = index
			}
		}
	}
	return directory
}

// Len reports how many chats this directory indexes. A caller distinguishing
// "looked and found nothing" from "there was nothing to look in" reads it;
// the two are different findings and must not report the same way.
func (directory *Directory[T]) Len() int {
	if directory == nil {
		return 0
	}
	return len(directory.entries)
}

// Lookup answers with the chat this identity belongs to, in two passes.
//
// The first pass is the TYPED ladder: each field is tried in the namespace it
// declares itself to be in, most specific first — id, then session+pane, then
// session, then label. The second is the UNTYPED sweep: every non-empty
// string in the query, tried in every namespace. The sweep is what makes
// resolution total, and it exists because callers genuinely do not always
// know which flavour they hold: `chat_inject <target>` takes whatever the
// operator typed, and the ledger's sender_session column has historically
// been handed a chat UUID. Namespaces are separate key spaces, so a sweep hit
// means the two strings are literally equal — a match, not a coincidence.
//
// A miss is an ANSWER, not an absence: Found is false and Display renders as
// visibly unresolved, so a failure to resolve can never be mistaken at the
// surface for a chat that happens to be called that.
func (directory *Directory[T]) Lookup(query Address) Answer[T] {
	canonical := query.Canonical()
	if directory != nil {
		for _, alias := range lookupAliases(canonical) {
			index, found := directory.byAlias[alias]
			if !found {
				continue
			}
			address := directory.addresses[index]
			return Answer[T]{
				Chat:    directory.entries[index],
				Found:   true,
				Address: address,
				Display: nameFor(address, true),
			}
		}
	}
	return Answer[T]{Address: canonical, Display: nameFor(canonical, false)}
}

// The four alias namespaces. They are prefixed, and that is load-bearing: an
// unprefixed map let a session name be looked up among UUIDs, a lookup that
// could never hit while reading exactly like session resolution.
const (
	aliasID      = "id\x00"
	aliasSession = "session\x00"
	aliasPane    = "pane\x00"
	aliasLabel   = "label\x00"
)

// registrationAliases lists every alias a KNOWN chat answers to.
func registrationAliases(address Address) []string {
	aliases := make([]string, 0, 4)
	if address.ID != "" {
		aliases = append(aliases, aliasID+address.ID)
	}
	if address.Session != "" {
		if address.Pane != "" {
			aliases = append(aliases, aliasPane+address.Session+"\x00"+address.Pane)
		}
		aliases = append(aliases, aliasSession+address.Session)
	}
	if address.Label != "" {
		aliases = append(aliases, aliasLabel+address.Label)
	}
	return aliases
}

// lookupAliases lists what to try for a QUERY, typed ladder first, then the
// untyped sweep.
func lookupAliases(address Address) []string {
	aliases := registrationAliases(address)
	if address.Text != "" {
		aliases = append(aliases, aliasSession+address.Text, aliasLabel+address.Text)
	}
	// The sweep. A bare pane id is deliberately absent: it identifies a
	// chat only per tmux server, so sweeping it would resolve one chat's
	// "%0" to a different chat's pane on another socket.
	for _, value := range []string{address.ID, address.Session, address.Label, address.Text} {
		if value == "" {
			continue
		}
		aliases = append(aliases,
			aliasID+value,
			aliasSession+value,
			aliasLabel+value,
		)
	}
	return aliases
}

// nameFor is the single implementation of the display rule.
//
// A chat is shown by its label, and by nothing else. When there is no label —
// or when what sits in the label slot is a machine address wearing a name's
// costume — the identity is rendered in a form no chat could be called, so a
// human reads "this did not resolve" instead of reading a socket id as a
// name. found chooses the word: a chat we located but cannot name is UNNAMED,
// an identity nothing answers to is UNRESOLVED. Those are different findings
// and they must not print the same.
//
// The raw id inside the brackets is deliberate and is the one place it
// belongs: the operator found this defect by correlating a ghost node against
// `pfm chat ls`, and truncating the id would take that away. The brackets and
// the leading word are what make it diagnostic output rather than a name.
func nameFor(address Address, found bool) DisplayName {
	if name := strings.TrimSpace(firstNonBlank(address.Label, address.Text)); name != "" &&
		!isMachineAddress(name) {
		return DisplayName{text: name}
	}
	raw := firstNonBlank(SessionName(address.Session), address.ID, address.Label, address.Text)
	if raw == "" {
		return DisplayName{}
	}
	word := "unresolved"
	if found {
		word = "unnamed"
	}
	return DisplayName{text: word + " <" + raw + ">"}
}

// isMachineAddress reports text that is a raw tmux socket/session name rather
// than something a human named.
//
// It composes the engine registry's own prefix question — pfmengine.FromSocket,
// the single implementation of "which engine owns this socket" — with the
// shape a spawned session name actually has (spawn.FreshSocket:
// prefix, then unix seconds, pid and a random suffix). The shape half is not
// optional: FromSocket answers a question about FILENAMES in the socket
// directory, and applied to free human text on its own it would demote a chat
// an operator deliberately called "cc-migration" into diagnostic brackets.
func isMachineAddress(text string) bool {
	id, known := pfmengine.FromSocket(text)
	if !known {
		return false
	}
	groups := strings.Split(
		strings.TrimPrefix(text, pfmengine.MustLookup(id).SocketPrefix),
		"-",
	)
	if len(groups) < 3 {
		return false
	}
	for _, group := range groups {
		if group == "" {
			return false
		}
		if strings.TrimLeft(group, "0123456789") != "" {
			return false
		}
	}
	return true
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
