package harvest

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The fixture (testdata/quora/question.md) is Jina Reader's Markdown of a
// Quora question page as the reader served it, trimmed to three answer cards
// and scrubbed: authors, upvoters and advertiser placeholders, bodies
// placeholder text. Its shapes are the captured ones: a card folded to a
// preview ("(more)"), a card the reader carried in full after "Continue
// Reading", an answer to a related question under a "Related" label with an
// "Upvoted by" credential spanning profile links, an ad and "Related
// questions" lists between them. Every rung pfm runs itself is served
// Cloudflare's challenge, HTTP 403, as Quora serves anonymous clients.
const quoraURL = "https://www.quora.com/What-is-the-meaning-of-life"

const quoraChallenge = `<!DOCTYPE html><html lang="en-US"><head><title>Just a moment...</title></head>` +
	`<body><div class="main-content"><noscript>Enable JavaScript and cookies to continue</noscript></div>` +
	`<script src="/cdn-cgi/challenge-platform/h/g/orchestrate/chl_page/v1"></script></body></html>`

func quoraHarvester(t *testing.T, reader string) *Harvester {
	t.Helper()
	walled := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return response(r, http.StatusForbidden, "text/html", quoraChallenge), nil
	})}
	return mustNew(t, Options{
		ContactEmail: "test@example.org",
		CacheDir:     t.TempDir(),
		Client:       walled,
		Chrome:       walled,
		Jina: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return response(r, http.StatusOK, "text/plain", reader), nil
		})},
		OA: walled,
	})
}

func TestQuoraReaderRendersServedAnswersAndNamesTheRest(t *testing.T) {
	result := quoraHarvester(t, socialFixture(t, "quora/question.md")).
		Fetch(context.Background(), quoraURL)
	if result.Error != "" || result.Method != "jina" {
		t.Fatalf("want the reader's page: method=%q err=%q", result.Method, result.Error)
	}
	content := result.Content
	for _, want := range []string{
		"# What is the meaning of life?",
		"### Placeholder Author One",
		"Lives in Placeholder City · Author has 157 answers and 304.6K answer views",
		"Updated 6y · [answer](https://www.quora.com/What-is-the-meaning-of-life-66/answer/Placeholder-Author-1)",
		"Placeholder preview text of the first answer",
		"![Image 4](https://qph.cf2.quoracdn.net/main-qimg-00000000000000000000000000000001-lq)",
		"### Placeholder Author Two",
		"Placeholder closing paragraph of the second answer.",
		"### Placeholder Author Three",
		"Lives in Placeholder Town · Upvoted by [Placeholder Upvoter One](https://www.quora.com/profile/Placeholder-Upvoter-1)" +
			", M.Sc Placeholder Studies and [Placeholder Upvoter Two]",
		"answer to a related question: What is life?",
		"Placeholder body of the third answer, **with emphasis**.",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("content lacks %q:\n%s", want, content)
		}
	}
	for _, unwanted := range []string{
		"Placeholder Advertiser", "Related questions", "What-is-the-purpose-of-life-56",
		"What-is-the-meaning-of-live-3", "Upvote ·", "Sign In", "(more)", "Continue Reading",
		"Profile photo for", "quora.com/careers", "### Placeholder Upvoter",
	} {
		if strings.Contains(content, unwanted) {
			t.Errorf("content keeps %q:\n%s", unwanted, content)
		}
	}
	if strings.Count(content, "cut where the card folds it") != 1 {
		t.Errorf("the folded opening of the second answer must render once, as the full answer:\n%s", content)
	}
	for _, want := range []string{
		"Quora answers: 100+ stated · 3 loaded",
		"not served signed-out",
		"/graphql/gql_para_POST",
		"1 of the loaded answers is a preview",
	} {
		if !strings.Contains(result.Partial, want) {
			t.Errorf("partial %q lacks %q", result.Partial, want)
		}
	}
}

func TestQuoraReaderStatedCountMet(t *testing.T) {
	page := strings.Replace(socialFixture(t, "quora/question.md"), "All related (100+)", "All related (3)", 1)
	page = strings.Replace(page, "(more)\n", "", 1)
	rendered := quoraReaderPage(quoraURL, page)
	if reason := partialReason(rendered); reason != "" {
		t.Fatalf("all 3 stated answers loaded in full, yet partial %q", reason)
	}
	if !strings.Contains(rendered, "Quora answers: 3 stated · 3 loaded") {
		t.Fatalf("the reconciliation line is missing:\n%s", rendered)
	}
}

func TestQuoraReaderNoStatedCountIsNamed(t *testing.T) {
	page := strings.Replace(socialFixture(t, "quora/question.md"), "All related (100+)", "", 1)
	reason := partialReason(quoraReaderPage(quoraURL, page))
	if !strings.Contains(reason, "the page states no answer count · 3 loaded") {
		t.Fatalf("an unread count must be a named gap, got %q", reason)
	}
}

func TestQuoraReaderUnknownShapeFallsThrough(t *testing.T) {
	for _, tc := range []struct{ source, page string }{
		{"https://www.quora.com/profile/Placeholder-Author-1", socialFixture(t, "quora/question.md")},
		{quoraURL, "# What is the meaning of life?\n\nSomething went wrong. Wait a moment and try again.\n"},
		{"https://example.org/What-is-the-meaning-of-life", socialFixture(t, "quora/question.md")},
	} {
		if got := quoraReaderPage(tc.source, tc.page); got != tc.page {
			t.Errorf("%s: a page the extractor cannot read must pass unchanged, got:\n%s", tc.source, got)
		}
	}
}
