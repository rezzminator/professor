package harvestmcp

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestReadAnswerStaysWithinTheTokenBudget: three pages each over the inline
// character cap, read in one call beside a small page, answer within
// answerTokenBudget — the bound under which Claude Code takes an MCP result
// inline instead of refusing it — each large page cut with a truncated: line
// naming its path, the small page whole.
func TestReadAnswerStaysWithinTheTokenBudget(t *testing.T) {
	cacheDir := filepath.Join(t.TempDir(), "cache")
	large := []string{"https://example.test/a", "https://example.test/b", "https://example.test/c"}
	for _, source := range large {
		seedArtifact(t, cacheDir, source, "", "# Long page\n\n"+strings.Repeat("Every reading is logged twice. ", 2200))
	}
	small := "# Small page\n\n" + strings.Repeat("The stilling well agrees with the gauge. ", 6)
	seedArtifact(t, cacheDir, "https://example.test/small", "", small)
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{CacheDir: cacheDir}))
	result, text := callText(
		t,
		session,
		"read",
		map[string]any{"urls": append(slices.Clone(large), "https://example.test/small")},
	)
	if result.IsError {
		t.Fatalf("read is an error: %s", text)
	}
	if tokens := harvest.EstimateTokens(text); tokens > answerTokenBudget {
		t.Fatalf("read answer estimates %d tokens, over the %d budget", tokens, answerTokenBudget)
	}
	blocks := strings.Split(text, "\n\n=== ")
	if len(blocks) != 4 {
		t.Fatalf("read answered %d blocks, want 4:\n%.400s", len(blocks), text)
	}
	for index := range large {
		if !strings.Contains(blocks[index], "\ntruncated: ") ||
			!strings.Contains(blocks[index], "full text at the path") {
			t.Errorf("block %d has no truncated: line naming the path:\n%.300s", index+1, blocks[index])
		}
	}
	if !strings.HasSuffix(blocks[3], strings.TrimSpace(small)) || strings.Contains(blocks[3], "truncated: ") {
		t.Errorf("the small page is not whole:\n%s", blocks[3])
	}
}

// TestContentTokenCapsSharesTheBudgetSmallestFirst: within budget nothing is
// capped; over it, items under their fair share keep everything and the rest
// split what is left; a budget spent elsewhere still caps at one token.
func TestContentTokenCapsSharesTheBudgetSmallestFirst(t *testing.T) {
	for _, test := range []struct {
		name   string
		needs  []int
		budget int
		want   []int
	}{
		{"within budget", []int{100, 200}, 300, []int{0, 0}},
		{"equal large", []int{900, 900, 900}, 900, []int{300, 300, 300}},
		{"small keeps all", []int{50, 1000, 1000}, 650, []int{0, 300, 300}},
		{"failed items need nothing", []int{0, 1000, 0}, 400, []int{0, 400, 0}},
		{"budget spent", []int{10, 10}, -5, []int{1, 1}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := contentTokenCaps(test.needs, test.budget); !slices.Equal(got, test.want) {
				t.Fatalf("contentTokenCaps(%v, %d) = %v, want %v", test.needs, test.budget, got, test.want)
			}
		})
	}
}

// TestReadAnswerBoundsEveryPath: an answer stays within answerTokenBudget
// however it runs over — a failure echoing a long source, with content or
// size-only, and pages whose joined estimate exceeds their blocks' sum (one
// prose, one all symbols) — a cut block saying so, a failed batch still an
// error.
func TestReadAnswerBoundsEveryPath(t *testing.T) {
	long := strings.Repeat("a", 60000)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	seedArtifact(t, cacheDir, "https://example.test/prose", "", strings.Repeat("a", 40000))
	seedArtifact(t, cacheDir, "https://example.test/symbols", "", strings.Repeat("{", 40000))
	session := connectHarvesterInProcess(t, newTestService(t, Runtime{CacheDir: cacheDir}))
	for _, test := range []struct {
		name      string
		arguments map[string]any
		wantError bool
		wantCut   string
	}{
		{"long failed source", map[string]any{"urls": []string{long}}, true, "[cut: "},
		{"long failed source, size only", map[string]any{"urls": []string{long}, "include_content": false}, true, "[cut: "},
		{
			"prose beside symbols",
			map[string]any{"urls": []string{"https://example.test/prose", "https://example.test/symbols"}},
			false, "\ntruncated: ",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, text := callText(t, session, "read", test.arguments)
			if result.IsError != test.wantError {
				t.Fatalf("read error = %v, want %v: %.300s", result.IsError, test.wantError, text)
			}
			if tokens := harvest.EstimateTokens(text); tokens > answerTokenBudget {
				t.Fatalf("read answer estimates %d tokens, over the %d budget", tokens, answerTokenBudget)
			}
			if !strings.Contains(text, test.wantCut) {
				t.Fatalf("the answer does not name its cut %q:\n%.300s", test.wantCut, text)
			}
		})
	}
}

// TestFitAnswerBudgetCutsALongAskAnswer: an ask answer over the budget is cut,
// naming its cut, and the small item beside it stays whole.
func TestFitAnswerBudgetCutsALongAskAnswer(t *testing.T) {
	result := harvest.Result{Source: "https://example.test/small", Content: "A short page.", Chars: 13, Tokens: 4}
	jobs := []readJob{{field: fieldURLs, source: result.Source}}
	item, failed := RenderReadItem(1, 1, result.Source, result, ReadView{})
	if failed {
		t.Fatalf("the small item failed: %s", item)
	}
	ask := "=== answer (engine model)\n" + strings.Repeat("The gauge agrees with the well. ", 6000)
	blocks := fitAnswerBudget([]string{item, ask}, jobs, []harvest.Result{result}, []bool{false}, ReadView{})
	text := strings.Join(blocks, "\n\n")
	if tokens := harvest.EstimateTokens(text); tokens > answerTokenBudget {
		t.Fatalf("answer estimates %d tokens, over the %d budget", tokens, answerTokenBudget)
	}
	if len(blocks) != 2 || blocks[0] != item {
		t.Fatalf("the small item was not kept whole:\n%.300s", text)
	}
	if !strings.HasPrefix(blocks[1], "=== answer (engine model)") || !strings.Contains(blocks[1], "\n[cut: ") {
		t.Fatalf("the ask answer is not cut with its cut line:\n%.300s", blocks[1])
	}
}
