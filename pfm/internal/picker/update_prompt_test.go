package picker

import (
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
)

// The update chat opens on this text, so the whole message is pinned: the steps, the two AskUserQuestion
// asks (update now, then which projects) and the target tag written into every command that names it.
func TestProfessorUpdatePromptMatchesItsGolden(t *testing.T) {
	const golden = "testdata/update-prompt-v1.2.3.golden"
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read %s: %v", golden, err)
	}
	got := professorUpdatePrompt(compose.Row{ID: "pfm-update-v1.2.3"})
	if got != string(want) {
		t.Fatalf("professorUpdatePrompt(v1.2.3) differs from %s\n--- got ---\n%s\n--- want ---\n%s", golden, got, want)
	}
}
