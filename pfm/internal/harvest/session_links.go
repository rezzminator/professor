package harvest

import (
	"regexp"
	"strings"
)

// Session ids in stored links. A phpBB board (and the forums built the same
// way) appends its visitor's session id to every link it serves a cookieless
// client — ?sid=<32 hex> or &sid=<32 hex>, an md5 — so a stored forum thread
// carries one per link: noise that changes on every fetch and identifies the
// session that fetched it. The pass is general, not phpBB-detected: the
// stored markdown no longer holds the generator markup, a reader rung's copy
// of the page never did, and the 32-hex shape is the session id's own mark
// wherever it appears. A sid of another shape is kept: Slashdot's
// comments.pl?sid=<digits> is the story the link names (slashdot.go).

// sessionIDParamRe is a sid query parameter holding a 32-hex session id, the
// separator before it, and the & after it when another parameter follows.
var sessionIDParamRe = regexp.MustCompile(`([?&])sid=[0-9A-Fa-f]{32}\b(&?)`)

// withoutSessionIDs is content with every sid=<32 hex> query parameter
// removed from its links; the link and its other parameters stay.
func withoutSessionIDs(content string) string {
	if !strings.Contains(content, "sid=") {
		return content
	}
	return sessionIDParamRe.ReplaceAllStringFunc(content, func(param string) string {
		separator, following := param[:1], strings.HasSuffix(param, "&")
		if separator == "?" && following {
			return "?" // ?sid=…&t=1 keeps ?t=1
		}
		if following {
			return "&" // &sid=…&t=1 keeps the & before t=1
		}
		return "" // the last parameter: its separator goes with it
	})
}
