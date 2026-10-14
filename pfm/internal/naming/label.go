package naming

import (
	"strings"
	"unicode"
)

// BookmarkLabel scrapes a chat's own 🔖 label off a captured pane. It is the
// single implementation of that read (K3): the label resolver, the call
// sender signature, and the claude half of window-name convergence all ask the
// same question of the same screen text, and three spellings of it drifted
// apart once already.
//
// The anchor is 🔖 PLUS an account medal, never 🌿: a labelled chat outside a
// repository renders no 🌿 and would go unaddressable. The medal set is
// whatever the STATUSLINE can emit, which outlives the fleet's account roster —
// a chat born under a retired account still renders its old medal until it
// exits, so dropping that medal here would silently stop resolving it.
//
// The LAST matching line wins: the statusline is at the bottom of the capture,
// and a transcript quoting an older label appears above it.
func BookmarkLabel(capture string) string {
	return BookmarkLabelFor(capture, nil)
}

// DeliveredFooterMarker opens the reply hint call stamps onto every
// message it delivers (internal/call signatureParts). It lives here, beside
// the scraper, because this package owns the one question both sides of it
// ask: which lines on a pane are the pane's OWN identity, and which are text
// somebody else put there. A line carrying this marker is a message this
// fleet delivered INTO the pane — never the pane's own statusline — and
// BookmarkLabelFor skips it.
const DeliveredFooterMarker = "to reply: call "

// BookmarkLabelFor is BookmarkLabel with the configured account emoji set
// supplied by the caller. Legacy medals remain accepted for old live labels.
//
// Delivered footer lines do not represent the recipient's identity.
//
// This defends against the footer THIS fleet writes, which it can recognise
// exactly. It is not a defence against arbitrary screen text: a capture is
// untrusted input, and the medal gate below tells a statusline apart from
// prose only by coincidence, not by construction. See the note on
// ContainsMedalFor.
func BookmarkLabelFor(capture string, configured []string) string {
	label := ""
	for _, line := range strings.Split(capture, "\n") {
		if strings.Contains(line, DeliveredFooterMarker) {
			continue
		}
		if !strings.Contains(line, "🔖") || !ContainsMedalFor(line, configured) {
			continue
		}
		index := strings.LastIndex(line, "🔖")
		candidate := strings.TrimLeftFunc(
			line[index+len("🔖"):],
			unicode.IsSpace,
		)
		if separator := strings.Index(candidate, "│"); separator >= 0 {
			candidate = candidate[:separator]
		}
		label = strings.TrimRightFunc(candidate, unicode.IsSpace)
	}
	return label
}

// ContainsMedal reports whether a captured line carries an account medal, the
// marker that tells a 🔖 label line apart from ordinary transcript text.
//
// It is a heuristic over untrusted text, not a proof of provenance: any line
// that happens to render a medal beside a 🔖 passes it. That is tolerable
// only because the one systematic source of foreign 🔖 lines — this fleet's
// own delivered footer — is now excluded above by its own marker. A pane's
// identity ultimately has authoritative sources (its SID crumb, its socket,
// its window name); screen text is not one of them. 🍀 is
// the retired account-4 medal: the account is gone, but chats labelled while it
// was live still render it, and without it here those chats are unresolvable.
func ContainsMedal(value string) bool {
	return ContainsMedalFor(value, nil)
}

// ContainsMedalFor recognizes configured account emoji plus the historical
// medals so labels from retired rosters remain resolvable.
func ContainsMedalFor(value string, configured []string) bool {
	for _, emoji := range configured {
		if emoji != "" && emoji != "·" && strings.Contains(value, emoji) {
			return true
		}
	}
	return strings.Contains(value, "🥇") ||
		strings.Contains(value, "🥈") ||
		strings.Contains(value, "🥉") ||
		strings.Contains(value, "🍀")
}
