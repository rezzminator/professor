package ui

import (
	"slices"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestValidAccountUsesRosterOnly(t *testing.T) {
	if got := validAccount(3, nil); got != 0 {
		t.Fatalf("validAccount(3, nil) = %d, want 0", got)
	}
	if got := validAccount(3, []int{1, 2}); got != 1 {
		t.Fatalf("validAccount(3, [1 2]) = %d, want 1", got)
	}
	if got := validAccount(2, []int{1, 2}); got != 2 {
		t.Fatalf("validAccount(2, [1 2]) = %d, want 2", got)
	}
}

func TestNormalizedAccountIDsDropsNonPositiveAndDuplicates(t *testing.T) {
	if got := normalizedAccountIDs(nil); got != nil {
		t.Errorf("no ids stay nil, got %v", got)
	}
	got := normalizedAccountIDs([]int{3, 0, 1, 3, -2, 1, 2})
	if !slices.Equal(got, []int{3, 1, 2}) {
		t.Errorf("ids keep their first-seen order, got %v", got)
	}
}

func TestNextAccountCyclesAndRecoversFromAnUnknownOne(t *testing.T) {
	ids := []int{1, 2, 3}
	cases := []struct{ current, want int }{{1, 2}, {2, 3}, {3, 1}, {9, 1}}
	for _, tc := range cases {
		if got := nextAccount(tc.current, ids); got != tc.want {
			t.Errorf("nextAccount(%d) = %d, want %d", tc.current, got, tc.want)
		}
	}
	if got := nextAccount(1, nil); got != 0 {
		t.Errorf("an empty roster has no next account, got %d", got)
	}
}

func TestPositiveOrFallsBackOnZeroAndNegative(t *testing.T) {
	if positiveOr(5, 9) != 5 || positiveOr(0, 9) != 9 || positiveOr(-3, 9) != 9 {
		t.Error("positiveOr keeps a positive value and falls back otherwise")
	}
}

func TestCopyEmojisDetachesTheMap(t *testing.T) {
	if got := copyEmojis(nil); got != nil {
		t.Errorf("no emojis stay nil, got %v", got)
	}
	source := map[int]string{1: "🥇"}
	copied := copyEmojis(source)
	source[1] = "x"
	source[2] = "y"
	if len(copied) != 1 || copied[1] != "🥇" {
		t.Errorf("the copy is unaffected by later edits, got %v", copied)
	}
}

func TestDefaultNewChatEnginePrefersClaudeThenCodexThenOpenCode(t *testing.T) {
	cases := []struct {
		name                    string
		claude, codex, openCode []int
		want                    pfmengine.ID
	}{
		{"claude wins", []int{1}, []int{1}, []int{1}, pfmengine.Claude},
		{"codex when no claude", nil, []int{2}, []int{1}, pfmengine.Codex},
		{"opencode alone", nil, nil, []int{1}, pfmengine.OpenCode},
		{"nothing configured is Claude", nil, nil, nil, pfmengine.Claude},
		{"non-positive ids do not count", []int{0}, []int{1}, nil, pfmengine.Codex},
	}
	for _, tc := range cases {
		if got := defaultNewChatEngine(tc.claude, tc.codex, tc.openCode); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
