package compose

import (
	"strings"
	"testing"

	pfmengine "hostops/pfm/internal/engine"
)

func TestEngineForKindCheckedRejectsUnknown(t *testing.T) {
	_, err := EngineForKindChecked(Kind(255))
	if err == nil || !strings.Contains(err.Error(), "unknown compose kind 255") {
		t.Fatalf("EngineForKindChecked(255) error=%v", err)
	}
}

func TestEngineForKindUnknownIsExplicit(t *testing.T) {
	if got := EngineForKind(Kind(255)); got != "unknown" {
		t.Fatalf("EngineForKind(255)=%q, want explicit unknown label", got)
	}
}

func TestLiveOpenCodeIsAnAddressableOpenCodeSeat(t *testing.T) {
	if got := LiveOpenCode.String(); got != "live-opencode" {
		t.Fatalf("LiveOpenCode.String()=%q, want live-opencode", got)
	}
	if !LiveOpenCode.IsLiveSeat() {
		t.Fatal("LiveOpenCode.IsLiveSeat()=false, want true")
	}
	if !LiveOpenCode.IsAddressable() {
		t.Fatal("LiveOpenCode.IsAddressable()=false, want true")
	}
	if got := EngineForKind(LiveOpenCode); got != pfmengine.OpenCode {
		t.Fatalf("EngineForKind(LiveOpenCode)=%q, want %q", got, pfmengine.OpenCode)
	}
}

// The Kind enum's values are persisted in golden fixtures, so a new member
// must be APPENDED. This pins the existing numbering against an insertion.
func TestKindNumberingIsAppendOnly(t *testing.T) {
	for kind, want := range map[Kind]string{
		1: "live-claude", 2: "live-codex", 3: "live-split", 4: "agent",
		5: "resume-claude", 6: "resume-codex", 7: "new-claude", 8: "new-codex",
		9: "booting", 10: "resume-opencode", 11: "new-opencode",
		12: "professor-update", 13: "professor-update-failed", 14: "live-opencode",
	} {
		if got := kind.String(); got != want {
			t.Fatalf("Kind(%d).String()=%q, want %q", kind, got, want)
		}
	}
}
