package reload

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

// TestNativeReloadLabelsOnlyANamedClaudeConversationItLeftBehind: a native
// Herdr `--new` reboot of a named Claude chat relabels the conversation it
// left "<name> (before --new)", as the tmux path does; an in-place reload, a
// Codex chat, an untitled conversation and an unknown session keep their
// transcript as it was.
func TestNativeReloadLabelsOnlyANamedClaudeConversationItLeftBehind(t *testing.T) {
	const relabelled = `{"type":"custom-title","customTitle":"Wave lead (before --new)","sessionId":"` +
		leftBehindSID + `"}` + "\n"
	for _, testCase := range []struct {
		name, title, session string
		fresh                bool
		engine               pfmengine.ID
		wantAppended         string
	}{
		{
			name: "named --new", title: "Wave lead", session: leftBehindSID, fresh: true, engine: pfmengine.Claude,
			wantAppended: relabelled,
		},
		{name: "in place", title: "Wave lead", session: leftBehindSID, engine: pfmengine.Claude},
		{name: "Codex", title: "Wave lead", session: leftBehindSID, fresh: true, engine: pfmengine.Codex},
		{name: "untitled", session: leftBehindSID, fresh: true, engine: pfmengine.Claude},
		{
			name: "unknown session", title: "Wave lead", session: "33333333-3333-4333-8333-333333333333",
			fresh: true, engine: pfmengine.Claude,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			claudeRoot := filepath.Join(home, "projects")
			transcript := writeLeftBehindTranscript(t, claudeRoot, testCase.title)
			before, err := os.ReadFile(transcript)
			if err != nil {
				t.Fatal(err)
			}
			native := &nativeReload{
				request: Request{fresh: testCase.fresh, Engine: testCase.engine},
				options: Options{Home: home, ClaudeRoots: []string{claudeRoot}, Clock: clock.Real},
			}
			var stderr bytes.Buffer
			native.labelLeftBehind(testCase.session, &stderr)
			after, err := os.ReadFile(transcript)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before)+testCase.wantAppended {
				t.Fatalf("transcript %s after the reload:\n%s\nwant the original plus %q; stderr %q",
					transcript, after, testCase.wantAppended, stderr.String())
			}
		})
	}
}
