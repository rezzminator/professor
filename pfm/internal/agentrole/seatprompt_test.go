package agentrole

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestSeatPromptLifecyclePreservesRoleAndPrompt(t *testing.T) {
	sidDir := filepath.Join(t.TempDir(), "sid")
	path := mustSeatPromptPath(t, sidDir, "cc-10", "")
	if want := filepath.Join(sidDir, "role-prompt-cc-10.md"); path != want {
		t.Fatalf("SeatPromptPath() = %q, want %q", path, want)
	}

	prompt, err := ComposeSeatPrompt(pfmengine.Claude, "reviewer", "CONSTITUTION", "FLEET PROMPT")
	if err != nil {
		t.Fatalf("ComposeSeatPrompt() error = %v", err)
	}
	wantPrompt := "FLEET PROMPT\n\n---\n\nCONSTITUTION"
	if err := WriteSeatPrompt(sidDir, "cc-10", "", prompt); err != nil {
		t.Fatalf("WriteSeatPrompt() error = %v", err)
	}
	role, gotPrompt, matched, found, err := ReadSeatPrompt(sidDir, "cc-10", "")
	if err != nil {
		t.Fatalf("ReadSeatPrompt() error = %v", err)
	}
	if !found || role != "reviewer" || gotPrompt != wantPrompt {
		t.Fatalf("ReadSeatPrompt() = role %q, prompt %q, found %v", role, gotPrompt, found)
	}
	if matched != path {
		t.Fatalf("ReadSeatPrompt() matched %q, want %q", matched, path)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "<!-- pfm agent-role: reviewer -->\n" + wantPrompt; string(raw) != want {
		t.Fatalf("seat prompt body = %q, want %q", raw, want)
	}
	if err := RemoveSeatPrompt(sidDir, "cc-10", ""); err != nil {
		t.Fatalf("RemoveSeatPrompt() error = %v", err)
	}
	if _, _, _, found, err := ReadSeatPrompt(sidDir, "cc-10", ""); err != nil || found {
		t.Fatalf("removed ReadSeatPrompt() = found %v, error %v", found, err)
	}
}

func TestSeatPromptPathUsesStableSocketIdentity(t *testing.T) {
	sidDir := t.TempDir()
	if got, want := mustSeatPromptPath(
		t, sidDir, "/tmp/tmux/cc-10", "",
	), filepath.Join(sidDir, "role-prompt-cc-10.md"); got != want {
		t.Fatalf("SeatPromptPath() = %q, want %q", got, want)
	}
	if got, want := mustSeatPromptPath(
		t, sidDir, "cc-10", "%2",
	), filepath.Join(sidDir, "role-prompt-cc-10-%2.md"); got != want {
		t.Fatalf("pane SeatPromptPath() = %q, want %q", got, want)
	}
	if _, err := SeatPromptPath(sidDir, "", ""); err == nil || !strings.Contains(err.Error(), "agent role") {
		t.Fatalf("empty socket error = %v", err)
	}
}

func TestComposeSeatPromptUsesEngineSpecificPromptValue(t *testing.T) {
	claude, err := ComposeSeatPrompt(pfmengine.Claude, "worker", "ROLE", "FLEET")
	if err != nil {
		t.Fatal(err)
	}
	if claude != "<!-- pfm agent-role: worker -->\nFLEET\n\n---\n\nROLE" {
		t.Fatalf("Claude prompt = %q", claude)
	}
	codex, err := ComposeSeatPrompt(pfmengine.Codex, "worker", "FLEET + ROLE", "IGNORED")
	if err != nil {
		t.Fatal(err)
	}
	if codex != "<!-- pfm agent-role: worker -->\nFLEET + ROLE" {
		t.Fatalf("Codex prompt = %q", codex)
	}
	if _, err := ComposeSeatPrompt(pfmengine.OpenCode, "worker", "ROLE", "FLEET"); err == nil ||
		!strings.Contains(err.Error(), "opencode") {
		t.Fatalf("OpenCode error = %v", err)
	}
}

func TestReadSeatPromptDistinguishesMissingUnreadableAndMalformed(t *testing.T) {
	sidDir := t.TempDir()
	if _, _, _, found, err := ReadSeatPrompt(sidDir, "cc-missing", ""); err != nil || found {
		t.Fatalf("missing = found %v, error %v", found, err)
	}

	unreadable := mustSeatPromptPath(t, sidDir, "cc-unreadable", "")
	if err := os.MkdirAll(unreadable, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, _, found, err := ReadSeatPrompt(sidDir, "cc-unreadable", ""); err == nil || !found {
		t.Fatalf("unreadable = found %v, error %v", found, err)
	}

	malformed := mustSeatPromptPath(t, sidDir, "cc-malformed", "")
	if err := os.WriteFile(malformed, []byte("not a marker\nbody"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, _, found, err := ReadSeatPrompt(sidDir, "cc-malformed", ""); err == nil || !found {
		t.Fatalf("malformed = found %v, error %v", found, err)
	}
	if err := RemoveSeatPrompt(sidDir, "cc-missing", ""); err != nil {
		t.Fatalf("removing a missing seat prompt: %v", err)
	}
}

func TestReadSeatPromptPrefersPaneAndFallsBackToSocket(t *testing.T) {
	sidDir := t.TempDir()
	bare := "<!-- pfm agent-role: worker -->\nBARE"
	specific := "<!-- pfm agent-role: reviewer -->\nPANE"
	if err := WriteSeatPrompt(sidDir, "cc-10", "", bare); err != nil {
		t.Fatal(err)
	}
	role, prompt, matched, found, err := ReadSeatPrompt(sidDir, "cc-10", "%2")
	if err != nil || !found || role != "worker" || prompt != "BARE" {
		t.Fatalf("fallback read = role %q prompt %q found %v err %v", role, prompt, found, err)
	}
	if matched != mustSeatPromptPath(t, sidDir, "cc-10", "") {
		t.Fatalf("fallback matched %q", matched)
	}
	if err := WriteSeatPrompt(sidDir, "cc-10", "%2", specific); err != nil {
		t.Fatal(err)
	}
	role, prompt, matched, found, err = ReadSeatPrompt(sidDir, "cc-10", "%2")
	if err != nil || !found || role != "reviewer" || prompt != "PANE" {
		t.Fatalf("specific read = role %q prompt %q found %v err %v", role, prompt, found, err)
	}
	if matched != mustSeatPromptPath(t, sidDir, "cc-10", "%2") {
		t.Fatalf("specific matched %q", matched)
	}
	if err := os.Remove(matched); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(matched, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, gotPath, found, err := ReadSeatPrompt(sidDir, "cc-10", "%2"); err == nil || !found || gotPath != matched {
		t.Fatalf("unreadable specific = path %q found %v err %v", gotPath, found, err)
	}
	if err := RemoveSeatPrompt(sidDir, "cc-10", "%2"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		mustSeatPromptPath(t, sidDir, "cc-10", "%2"),
		mustSeatPromptPath(t, sidDir, "cc-10", ""),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("cleanup left %s: %v", path, err)
		}
	}
}

func TestSeatPromptSocketIdentitySeparatesDuplicateDisplayNames(t *testing.T) {
	sidDir := t.TempDir()
	for _, socket := range []string{"cc-10", "cc-11"} {
		if err := WriteSeatPrompt(sidDir, socket, "", "<!-- pfm agent-role: worker -->\n"+socket); err != nil {
			t.Fatal(err)
		}
	}
	if err := RemoveSeatPrompt(sidDir, "cc-10", ""); err != nil {
		t.Fatal(err)
	}
	_, prompt, _, found, err := ReadSeatPrompt(sidDir, "cc-11", "")
	if err != nil || !found || prompt != "cc-11" {
		t.Fatalf("other same-name seat = prompt %q found %v err %v", prompt, found, err)
	}
}

func TestSeatPromptTraversalRefusesBeforeWriting(t *testing.T) {
	root := t.TempDir()
	sidDir := filepath.Join(root, "sid")
	outside := filepath.Join(root, "x.md")
	if err := WriteSeatPrompt(sidDir, "cc-10", "../../x", "secret"); err == nil ||
		!strings.Contains(err.Error(), "agent role: seat prompt path escapes SIDDir") {
		t.Fatalf("traversal write error = %v", err)
	}
	if _, err := os.Stat(outside); !os.IsNotExist(err) {
		t.Fatalf("traversal wrote %s: %v", outside, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("traversal left files under %s: entries=%v err=%v", root, entries, err)
	}
}

func mustSeatPromptPath(t *testing.T, sidDir, socket, pane string) string {
	t.Helper()
	path, err := SeatPromptPath(sidDir, socket, pane)
	if err != nil {
		t.Fatal(err)
	}
	return path
}
