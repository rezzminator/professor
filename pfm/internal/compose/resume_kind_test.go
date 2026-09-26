package compose

import "testing"

// Every live seat demotes to ITS OWN engine's resumable kind. The three call
// sites each spelled an if/else that mapped everything-not-Codex onto
// ResumeClaude, so a dead or rebooted OpenCode seat came back as a Claude
// chat: the wrong engine, the wrong binary, and an id nothing could resume.
func TestResumeKindForDemotesToTheSeatsOwnEngine(t *testing.T) {
	for kind, want := range map[Kind]Kind{
		LiveClaude:   ResumeClaude,
		LiveCodex:    ResumeCodex,
		LiveOpenCode: ResumeOpenCode,
		LiveSplit:    ResumeClaude,
		// Nothing to demote: returned unchanged rather than silently
		// rewritten into a Claude resume.
		ResumeOpenCode: ResumeOpenCode,
		Agent:          Agent,
		Booting:        Booting,
	} {
		got := ResumeKindFor(kind)
		if got != want {
			t.Fatalf("ResumeKindFor(%s) = %s, want %s", kind, got, want)
		}
		if kind.IsLiveSeat() && EngineForKind(got) != EngineForKind(kind) {
			t.Fatalf("ResumeKindFor(%s) changed engine: %s -> %s", kind, EngineForKind(kind), EngineForKind(got))
		}
	}
}
