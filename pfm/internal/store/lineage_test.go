package store

import (
	"testing"
)

func TestResolveCodexLineageUsesNewestAndMonotonicPromptMaximum(t *testing.T) {
	rollouts := lineageFixtureRollouts()
	lineages, roots := ResolveCodexLineages(rollouts)
	if len(lineages) != 1 {
		t.Fatalf("lineages = %#v, want one", lineages)
	}
	lineage := lineages[0]
	if lineage.RootID != "root" ||
		lineage.Newest.ID != "child-newest" ||
		lineage.PromptCount != 11 {
		t.Fatalf("lineage = %#v", lineage)
	}
	for _, id := range []string{"root", "child-old", "child-newest", "subagent"} {
		if roots[id] != "root" {
			t.Fatalf("root[%q] = %q, want root", id, roots[id])
		}
	}
}

func lineageFixtureRollouts() []Rollout {
	return []Rollout{
		{
			ID:          "root",
			Path:        "/codex/root.jsonl",
			Size:        100,
			MTimeNS:     100,
			CWD:         "/work/web",
			UserThread:  true,
			SessionID:   "root",
			FirstPrompt: "WEB root",
			PromptCount: 10,
		},
		{
			ID:           "child-old",
			Path:         "/codex/child-old.jsonl",
			Size:         110,
			MTimeNS:      200,
			CWD:          "/work/web",
			UserThread:   true,
			SessionID:    "root",
			ParentThread: "root",
			FirstPrompt:  "WEB root",
			PromptCount:  10,
		},
		{
			ID:           "child-newest",
			Path:         "/codex/child-newest.jsonl",
			Size:         120,
			MTimeNS:      300,
			CWD:          "/work/web",
			UserThread:   true,
			SessionID:    "root",
			ParentThread: "child-old",
			FirstPrompt:  "WEB root",
			PromptCount:  11,
		},
		{
			ID:           "subagent",
			Path:         "/codex/subagent.jsonl",
			Size:         90,
			MTimeNS:      400,
			CWD:          "/work/web",
			SessionID:    "root",
			ParentThread: "child-newest",
			FirstPrompt:  "delegated",
			PromptCount:  99,
		},
	}
}
