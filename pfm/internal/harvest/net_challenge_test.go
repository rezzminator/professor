package harvest

import (
	"net/http"
	"strings"
	"testing"
)

// TestIsChallengeUsesOneShortDefinitionAtTheBoundary pins F10: the
// Cloudflare-marker gate and the forum-phrase gate must agree on what
// "short" means. A body of exactly 4000 content chars used to read short
// (<=4000) for the first gate but not (<4000) for the second, so a
// forum-wall phrase at exactly that length went unflagged.
func TestIsChallengeUsesOneShortDefinitionAtTheBoundary(t *testing.T) {
	phrase := "prove your humanity"
	body := []byte(strings.Repeat("a", 4000-len(phrase)) + phrase)
	if got := contentChars(string(body)); got != 4000 {
		t.Fatalf("fixture body is %d content chars, want exactly 4000 to sit on the isChallenge boundary", got)
	}
	if !isChallenge(body, http.StatusOK) {
		t.Fatalf("a 4000-char, HTTP 200 body containing a forum wall phrase was not flagged a challenge: "+
			"the Cloudflare-marker gate (<=4000) and the forum-phrase gate disagreed at the boundary: %.80q", body)
	}
}
