package resolve

import (
	"strings"
	"testing"
)

// chat is a minimal caller record. Directory is generic precisely so a caller
// keeps its OWN row type and no field is copied into a parallel struct that
// could drift out of sync with it.
type chat struct {
	id      string
	session string
	pane    string
	label   string
}

func chatAddress(entry chat) Address {
	return Address{ID: entry.id, Session: entry.session, Pane: entry.pane, Label: entry.label}
}

// fleet is the fixture every case below resolves against: one Claude chat
// whose label carries a colon (the operator's real shape — P:DO,
// W:ORCHESTRATOR), one Codex chat, and one unlabelled chat.
func fleet() []chat {
	return []chat{
		{id: "01a0434f-c6e6-4a11", session: "cc-1787705979-3980493-30867", pane: "%0", label: "P:DO"},
		{id: "019ffd1e-300f-7872", session: "cx-1787757492-3196324-4837", pane: "%1", label: "W5_TESTER"},
		{id: "boot-id", session: "cc-1787705979-3980493-1", pane: "%0"},
	}
}

// TestDirectoryResolvesEveryIdentityShapeTheFleetRecords is the table this
// whole layer exists for: every shape a caller or a recorded comms event can
// carry has to reach the same chat. The three wild event shapes are all here
// — a spawn writes a full socket PATH with an EMPTY pane, an inject-as-
// receiver writes a full path plus a pane id, an inject-as-sender writes only
// its session name — plus the two a human supplies: a label, and (because the
// old reply footer taught them to) a raw session id typed as the target.
func TestDirectoryResolvesEveryIdentityShapeTheFleetRecords(t *testing.T) {
	directory := NewDirectory(fleet(), chatAddress)
	tests := []struct {
		name  string
		query Address
		want  string
	}{
		{"chat uuid", Address{ID: "01a0434f-c6e6-4a11"}, "P:DO"},
		{"bare session name", Address{Session: "cc-1787705979-3980493-30867"}, "P:DO"},
		{"full socket path", Address{Session: "/tmp/tmux-1000/cc-1787705979-3980493-30867"}, "P:DO"},
		{
			"socket path plus pane (inject receiver)",
			Address{Session: "/tmp/tmux-1000/cc-1787705979-3980493-30867", Pane: "%0"},
			"P:DO",
		},
		{
			"socket path, empty pane (spawn receiver)",
			Address{Session: "/tmp/tmux-1000/cc-1787705979-3980493-30867", Pane: ""},
			"P:DO",
		},
		{"label with a colon", Address{Label: "P:DO"}, "P:DO"},
		{"untyped target holding a label", Address{Text: "P:DO"}, "P:DO"},
		{"untyped target holding a raw session id", Address{Text: "cc-1787705979-3980493-30867"}, "P:DO"},
		{"untyped target holding a chat uuid", Address{Text: "01a0434f-c6e6-4a11"}, "P:DO"},
		{"session name handed to the uuid field", Address{ID: "cc-1787705979-3980493-30867"}, "P:DO"},
		{"uuid handed to the session field", Address{Session: "019ffd1e-300f-7872"}, "W5_TESTER"},
		{
			"codex chat by socket path",
			Address{Session: "/tmp/tmux-1000/cx-1787757492-3196324-4837", Pane: "%1"},
			"W5_TESTER",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			answer := directory.Lookup(test.query)
			if !answer.Found {
				t.Fatalf("Lookup(%+v) resolved nothing; display was %q", test.query, answer.Display)
			}
			if answer.Chat.label != test.want {
				t.Fatalf("Lookup(%+v) = %q, want %q", test.query, answer.Chat.label, test.want)
			}
			if got := answer.Display.String(); got != test.want {
				t.Fatalf("Display for %+v = %q, want the chat's own label %q", test.query, got, test.want)
			}
		})
	}
}

// TestDirectoryStaleIdentitiesStillResolveAcrossARename pins why the session
// is the stored key and the label is not: chat_name replaces a label at will,
// and the comms ledger is append-only, so an event recorded under the old
// name must still find the chat. Addressing by the label the chat has SINCE
// been given must not reach it through a stale event either.
func TestDirectoryStaleIdentitiesStillResolveAcrossARename(t *testing.T) {
	renamed := []chat{{
		id: "01a0434f-c6e6-4a11", session: "cc-1787705979-3980493-30867",
		pane: "%0", label: "P:DONE",
	}}
	directory := NewDirectory(renamed, chatAddress)

	stale := directory.Lookup(Address{
		Session: "/tmp/tmux-1000/cc-1787705979-3980493-30867",
		Text:    "P:DO", // the label it was spawned with
	})
	if !stale.Found || stale.Chat.label != "P:DONE" {
		t.Fatalf("an event recorded before a rename lost its chat: %+v", stale)
	}
	if got := stale.Display.String(); got != "P:DONE" {
		t.Fatalf("display = %q, want the chat's CURRENT label", got)
	}

	freed := directory.Lookup(Address{Label: "P:DO"})
	if freed.Found {
		t.Fatalf("the freed label still resolves to the renamed chat: %+v", freed)
	}
}

// TestDirectoryUnresolvedNeverRendersAsAName is the honesty rule. An
// identity nothing answers to must render as something a human reads as "this
// did not resolve" — never as a bare id sitting in the name position, which
// is indistinguishable from a chat actually called that.
func TestDirectoryUnresolvedNeverRendersAsAName(t *testing.T) {
	directory := NewDirectory(fleet(), chatAddress)
	tests := []struct {
		name  string
		query Address
		raw   string
	}{
		{"dead chat's session", Address{Session: "cc-1787827285-1466858-781"}, "cc-1787827285-1466858-781"},
		{
			"dead chat's socket path",
			Address{Session: "/tmp/tmux-1000/cx-1787827285-1466858-782"},
			"cx-1787827285-1466858-782",
		},
		{"unknown uuid", Address{ID: "deadbeef-0000-0000"}, "deadbeef-0000-0000"},
		{"raw session typed as a target", Address{Text: "cc-1787827285-1466858-783"}, "cc-1787827285-1466858-783"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			answer := directory.Lookup(test.query)
			if answer.Found {
				t.Fatalf("Lookup(%+v) claimed a chat: %+v", test.query, answer.Chat)
			}
			display := answer.Display.String()
			if display == test.raw {
				t.Fatalf("unresolved identity rendered as a plausible name: %q", display)
			}
			if !strings.HasPrefix(display, "unresolved <") {
				t.Fatalf("display %q does not say it failed to resolve", display)
			}
			if !strings.Contains(display, test.raw) {
				t.Fatalf("display %q dropped the id an operator needs to correlate it", display)
			}
		})
	}
}

// TestDirectoryUnresolvedKeepsAHumanNameWhenItHasOne separates the two
// failures that must not print alike. An identity carrying a LABEL that
// matched nothing is still a name a human gave something — a chat that has
// since died, a group member who left — and rendering it is truthful. Only an
// identity with no name at all falls back to the bracketed diagnostic.
func TestDirectoryUnresolvedKeepsAHumanNameWhenItHasOne(t *testing.T) {
	directory := NewDirectory(fleet(), chatAddress)
	answer := directory.Lookup(Address{Label: "Departed"})
	if answer.Found {
		t.Fatalf("Lookup resolved a chat that is not in the fleet: %+v", answer)
	}
	if got := answer.Display.String(); got != "Departed" {
		t.Fatalf("display = %q, want the human name the identity carried", got)
	}
}

// TestDirectoryResolvedButUnnamedIsNotTheSameAsUnresolved keeps the third
// state distinct: a chat we FOUND but have no label for is UNNAMED, and
// saying "unresolved" about it would claim we failed to look when we looked
// and succeeded.
func TestDirectoryResolvedButUnnamedIsNotTheSameAsUnresolved(t *testing.T) {
	directory := NewDirectory(fleet(), chatAddress)
	answer := directory.Lookup(Address{Session: "cc-1787705979-3980493-1"})
	if !answer.Found {
		t.Fatalf("the unlabelled chat did not resolve: %+v", answer)
	}
	display := answer.Display.String()
	if display != "unnamed <cc-1787705979-3980493-1>" {
		t.Fatalf("display = %q, want the unnamed rendering", display)
	}
	if strings.Contains(display, "unresolved") {
		t.Fatalf("a chat we found reported as unresolved: %q", display)
	}
}

// TestNamedRefusesAMachineAddressWearingANameSCostume is the wall. Named is
// the only door from an arbitrary string into a DisplayName, and it must not
// be usable to smuggle a tmux session name into the name position — which is
// the exact defect that put "cc-1787705979-3980493-30867" on the cosmos
// canvas as though a chat were called that.
func TestNamedRefusesAMachineAddressWearingANameSCostume(t *testing.T) {
	tests := []struct {
		text string
		want string
	}{
		{"crew", "crew"},
		{"P:DO", "P:DO"},
		{"W5_TESTER", "W5_TESTER"},
		// A human name that merely starts like a socket prefix is a NAME:
		// the shape check is what keeps a chat an operator called
		// "cc-migration" out of diagnostic brackets.
		{"cc-migration", "cc-migration"},
		{"cx-notes-today", "cx-notes-today"},
		{"cc-1787705979-3980493-30867", "unnamed <cc-1787705979-3980493-30867>"},
		{"cx-1787757492-3196324-4837", "unnamed <cx-1787757492-3196324-4837>"},
		{"", ""},
	}
	for _, test := range tests {
		if got := Named(test.text).String(); got != test.want {
			t.Fatalf("Named(%q) = %q, want %q", test.text, got, test.want)
		}
	}
}

// TestSessionNameNormalisesBothRecordedShapes pins the single reduction every
// caller now shares. A live row carries the BARE name tmux is addressed by
// (-L); inject and spawn both record the full -S path. Keying one side by
// each is what made the pane index unmatchable.
func TestSessionNameNormalisesBothRecordedShapes(t *testing.T) {
	tests := []struct{ in, want string }{
		{"cc-1787705979-3980493-30867", "cc-1787705979-3980493-30867"},
		{"/tmp/tmux-1000/cc-1787705979-3980493-30867", "cc-1787705979-3980493-30867"},
		{"  /tmp/tmux-1000/cx-1-2-3  ", "cx-1-2-3"},
		{"", ""},
		{"/", ""},
	}
	for _, test := range tests {
		if got := SessionName(test.in); got != test.want {
			t.Fatalf("SessionName(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

// TestDirectoryFirstEntryWinsAnAliasCollision pins the tie-break the cosmos
// graph already depended on: rows arrive newest-first, so a stale duplicate
// must never displace the live chat that shares its label.
func TestDirectoryFirstEntryWinsAnAliasCollision(t *testing.T) {
	directory := NewDirectory([]chat{
		{id: "live-id", session: "cc-9-9-9", pane: "%0", label: "Twin"},
		{id: "stale-id", session: "cc-1-1-1", pane: "%0", label: "Twin"},
	}, chatAddress)
	answer := directory.Lookup(Address{Label: "Twin"})
	if !answer.Found || answer.Chat.id != "live-id" {
		t.Fatalf("collision resolved to %+v, want the first (newest) entry", answer)
	}
}

// TestDirectoryDoesNotSweepABarePaneID guards the one alias deliberately left
// out of the untyped sweep. Pane ids are unique per tmux SERVER, not
// globally, so sweeping "%0" would resolve one chat's pane to a different
// chat's on another socket — a wrong answer, which is worse than none.
func TestDirectoryDoesNotSweepABarePaneID(t *testing.T) {
	directory := NewDirectory(fleet(), chatAddress)
	if answer := directory.Lookup(Address{Text: "%0"}); answer.Found {
		t.Fatalf("a bare pane id resolved to %+v", answer.Chat)
	}
	if answer := directory.Lookup(Address{Pane: "%0"}); answer.Found {
		t.Fatalf("a pane id with no session resolved to %+v", answer.Chat)
	}
}

// TestDirectoryEmptyIdentityResolvesToNothingAndRendersAsNothing separates
// "there was nothing to look up" from "I looked and found nothing". An
// address naming nothing at all produces an empty display, which every
// caller already reads as "no node here" — it must not become a bracketed
// diagnostic implying a lookup failed.
func TestDirectoryEmptyIdentityResolvesToNothingAndRendersAsNothing(t *testing.T) {
	directory := NewDirectory(fleet(), chatAddress)
	answer := directory.Lookup(Address{})
	if answer.Found {
		t.Fatalf("an empty identity resolved: %+v", answer)
	}
	if got := answer.Display.String(); got != "" {
		t.Fatalf("empty identity rendered %q, want the empty string", got)
	}
}

// TestDirectoryWithNothingToLookInStillAnswers keeps the empty and absent
// cases from becoming a panic at the one moment a caller most needs an
// answer. An empty roster is a legitimate live state — every chat killed —
// and it must produce the same shape of verdict as a populated one.
func TestDirectoryWithNothingToLookInStillAnswers(t *testing.T) {
	empty := NewDirectory(nil, chatAddress)
	if answer := empty.Lookup(Address{Session: "cc-1-2-3"}); answer.Found ||
		answer.Display.String() != "unresolved <cc-1-2-3>" {
		t.Fatalf("empty directory lookup = %+v", answer)
	}
	var absent *Directory[chat]
	if answer := absent.Lookup(Address{Label: "P:DO"}); answer.Found ||
		answer.Display.String() != "P:DO" {
		t.Fatalf("nil directory lookup = %+v", answer)
	}
}
