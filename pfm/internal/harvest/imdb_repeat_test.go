package harvest

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// TestIMDbReviewsRepeatNamed: IMDb's cursor pages can serve one review twice
// (a live read of a title's 8 pages served 200 reviews, 199 of them
// distinct). The repeat renders once, and the gap text names it and states
// the loaded count exactly, so stated · loaded · the remainder reconcile —
// never a "first 200" beside 199 loaded.
func TestIMDbReviewsRepeatNamed(t *testing.T) {
	// Eight cursor pages of two reviews each, built from the captured first
	// page; the last page repeats the previous page's second review, and the
	// list names a further page the bound does not read.
	capped := newIMDbSite(t)
	first := strings.Replace(capped.answers[""], `"total":4`, `"total":12039`, 1)
	capped.answers = map[string]string{}
	for index := range imdbReviewPages {
		after := ""
		if index > 0 {
			after = fmt.Sprintf("placeholder-cursor-%d", index)
		}
		answer := strings.Replace(first, "g4x77placeholdercursorone", fmt.Sprintf("placeholder-cursor-%d", index+1), 1)
		answer = strings.Replace(answer, "rw9000001", fmt.Sprintf("rw9%d00001", index), 1)
		second := index
		if index == imdbReviewPages-1 {
			second = index - 1
		}
		capped.answers[after] = strings.Replace(answer, "rw9000002", fmt.Sprintf("rw9%d00002", second), 1)
	}
	result := capped.harvester(t).FetchWithOptions(context.Background(), imdbURL, FetchOptions{Refresh: true})
	if got := len(socialRendered(result.Content)); got != 15 {
		t.Fatalf("rendered %d reviews, want the 15 distinct ones\n%.2500s", got, result.Content)
	}
	for _, text := range []string{
		"**Replies:** 12039 stated · 15 loaded",
		"12024 stated repl(ies) not served: the harvester reads 8 pages of 25 from IMDb's GraphQL API " +
			"(16 reviews served, 1 repeated across pages and counted once: 15 loaded); the rest were not requested",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\npartial=%q\n%.2500s", text, result.Partial, result.Content)
		}
	}
	if !strings.Contains(result.Partial, "imdb reviews: 15 of 12039 stated replies loaded") ||
		!strings.Contains(result.Partial, "1 repeated across pages") {
		t.Fatalf("the partial marker does not name the repeat and the loaded count: %q", result.Partial)
	}

	// A list that ends before its total, its second page repeating the first
	// page's second review: the repeat is named beside the unserved rest.
	ended := newIMDbSite(t)
	ended.answers["g4x77placeholdercursorone"] = strings.Replace(ended.answers["g4x77placeholdercursorone"],
		"rw9000003", "rw9000002", 1)
	result = ended.harvester(t).FetchWithOptions(context.Background(), imdbURL, FetchOptions{Refresh: true})
	for _, text := range []string{
		"**Replies:** 4 stated · 3 loaded",
		"1 stated repl(ies) not served: IMDb's GraphQL API ended its review list (no further page) before its " +
			"stated total (4 reviews served, 1 repeated across pages and counted once: 3 loaded)",
	} {
		if !strings.Contains(result.Content, text) {
			t.Fatalf("the artifact lacks %q:\npartial=%q\n%.2500s", text, result.Partial, result.Content)
		}
	}
}
