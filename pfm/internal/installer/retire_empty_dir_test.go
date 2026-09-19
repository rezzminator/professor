package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPreflightReportsTheNonEmptyRetirementBeforeAnyMutation is a REGRESSION
// test for a preflight that could not see the one conflict that wedges an
// apply mid-run: retireEmptyDir's non-empty refusal only ran when
// installer.apply, and preflight plans with apply=false — so a stray file
// under the managed chat/ tree let preflight report clean, and the real pass
// then staged assets, launchers and overlays before refusing, leaving the
// machine half-converged with no named remedy.
func TestPreflightReportsTheNonEmptyRetirementBeforeAnyMutation(t *testing.T) {
	home := t.TempDir()
	managed := managedRootForHome(home)
	stray := filepath.Join(managed, "chat", "stray-note.md")
	writeFixture(t, stray, "an operator note nobody asked pfm to remove\n")

	_, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, CodexHomes: []string{},
	})
	if err == nil {
		t.Fatal("apply succeeded with a stray file under the managed chat directory, want a preflight refusal")
	}
	if !strings.Contains(err.Error(), "preflight apply plan") {
		t.Fatalf("the refusal did not come from preflight, so it came after mutation began: %v", err)
	}
	if !strings.Contains(err.Error(), stray) {
		t.Fatalf("the refusal did not name the stray file %s as the remedy: %v", stray, err)
	}
	if _, statErr := os.Lstat(filepath.Join(managed, "shim", "pfm.zsh")); !os.IsNotExist(statErr) {
		t.Fatalf("the apply pass mutated the machine before the conflict was reported: %v", statErr)
	}
}

// TestDryRunReportsTheNonEmptyRetirementAsAPlanConflict pins the same
// conflict on the operator-facing preview: `pfm install` with no --yes is the
// preview of the apply, so a conflict that would stop the apply has to be
// visible in it — named in the transcript and returned as a plan error, never
// a clean-looking preview of a run that cannot finish.
func TestDryRunReportsTheNonEmptyRetirementAsAPlanConflict(t *testing.T) {
	home := t.TempDir()
	stray := filepath.Join(managedRootForHome(home), "codex-skills", "bb", "keepme.txt")
	writeFixture(t, stray, "operator file\n")

	var transcript bytes.Buffer
	_, err := Run(context.Background(), Options{
		Mode: ModeDryRun, Home: home, Runner: &fakeRunner{}, CodexHomes: []string{}, Stdout: &transcript,
	})
	if err == nil {
		t.Fatal("dry run reported no error for a directory it cannot retire, want the plan conflict")
	}
	if !strings.Contains(err.Error(), stray) {
		t.Fatalf("dry-run plan error did not name the stray file %s: %v", stray, err)
	}
	if !strings.Contains(transcript.String(), stray) {
		t.Fatalf("dry-run transcript hid the conflict:\n%s", transcript.String())
	}
}

// TestRetirementAccountsForTheFilesTheSamePassRemoves is the other half of
// the conflict rule: a managed directory holding ONLY the files this very
// pass retires is empty by the time the retirement runs, so the dry pass —
// which has removed nothing yet — must not report it as a conflict. Without
// that accounting, every install with a legacy /chat: command card on disk
// would refuse to run.
func TestRetirementAccountsForTheFilesTheSamePassRemoves(t *testing.T) {
	home := t.TempDir()
	managed := managedRootForHome(home)
	writeFixture(t, filepath.Join(managed, "chat", "ls.command.md"), "# retired chat card\n")
	writeFixture(t, filepath.Join(managed, "chat", "self", "compact.command.md"), "# retired chat card\n")

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, CodexHomes: []string{},
	}); err != nil {
		t.Fatalf("apply refused a managed directory holding only the cards it retires itself: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(managed, "chat")); !os.IsNotExist(err) {
		t.Fatalf("the retired chat directory survived the apply: %v", err)
	}
}
