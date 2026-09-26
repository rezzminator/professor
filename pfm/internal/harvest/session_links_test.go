package harvest

import (
	"strings"
	"testing"
)

// TestStoredLinksLoseTheirSessionID: a phpBB thread served to a cookieless
// client appends sid=<32 hex> to every link. The stored artifact keeps each
// link and its other parameters but no session id — wherever the sid sits in
// the query, before a fragment or a link title. A sid that is not a 32-hex
// session id is kept: Slashdot's comments.pl?sid=<digits> names a story.
func TestStoredLinksLoseTheirSessionID(t *testing.T) {
	const sid = "0123456789abcdef0123456789abcdef"
	// the lines of a stored Linux Mint forums thread (phpBB 3), usernames and ids replaced
	stored := strings.Join([]string{
		"[Advanced search](./search.php?sid=" + sid + " \"Advanced search\")",
		"  + + [Unanswered topics](./search.php?search_id=unanswered&sid=" + sid + ")",
		"* [FAQ](/app.php/help/faq?sid=" + sid + " \"Frequently Asked Questions\")",
		"* [Login](./ucp.php?mode=login&redirect=viewtopic.php%3Ft%3D394972&sid=" + sid + " \"Login\")",
		"[Post](./viewtopic.php?p=2317362&sid=" + sid + "#p2317362 \"Post\")",
		"by **[example-user](./memberlist.php?mode=viewprofile&u=1001&sid=" + sid + ")** » Sun Apr 09, 2023 6:52 pm",
		":   **Posts:** [4100](./search.php?author_id=1001&sr=posts&sid=" + sid + ")",
		"* [Board index](./index.php?sid=" + sid + ")",
		"[Jump](./viewtopic.php?sid=" + sid + "&t=394972&start=20)",
		"[Comment](https://news.slashdot.org/comments.pl?sid=24000001&cid=66000001)",
	}, "\n")
	want := strings.Join([]string{
		"[Advanced search](./search.php \"Advanced search\")",
		"  + + [Unanswered topics](./search.php?search_id=unanswered)",
		"* [FAQ](/app.php/help/faq \"Frequently Asked Questions\")",
		"* [Login](./ucp.php?mode=login&redirect=viewtopic.php%3Ft%3D394972 \"Login\")",
		"[Post](./viewtopic.php?p=2317362#p2317362 \"Post\")",
		"by **[example-user](./memberlist.php?mode=viewprofile&u=1001)** » Sun Apr 09, 2023 6:52 pm",
		":   **Posts:** [4100](./search.php?author_id=1001&sr=posts)",
		"* [Board index](./index.php)",
		"[Jump](./viewtopic.php?t=394972&start=20)",
		"[Comment](https://news.slashdot.org/comments.pl?sid=24000001&cid=66000001)",
	}, "\n")
	// the stored page as a rung finalizes it, complete and partial both
	for _, content := range []string{stored, withPartial(stored, "the page links its next page, which was not followed")} {
		got := convertedPage{}.withGaps(content, carriedGaps{}, nil)
		if strings.Contains(got, sid) {
			t.Fatalf("a stored link keeps its session id:\n%s", got)
		}
		if !strings.Contains(got, want) {
			t.Fatalf("stored links lost more than their session id:\n got %s\nwant %s", got, want)
		}
	}
}
