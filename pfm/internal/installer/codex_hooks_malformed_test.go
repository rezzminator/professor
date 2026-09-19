package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMalformedCodexHooksSkipsLoudlyAndFinishesTheRun is a REGRESSION test
// for a hand-broken ~/.codex/hooks.json aborting the whole install: the Codex
// hook path hard-returned on any parse failure, so wireMCP, wireLogDefault,
// wireShell, wireVSCode and writeUpdateMetadata never ran and the machine was
// left half-wired. The Claude sibling wireSettings has always skipped loudly
// and continued unless uninstalling with owned hooks; this is the same
// contract on the Codex side.
func TestMalformedCodexHooksSkipsLoudlyAndFinishesTheRun(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	hooks := filepath.Join(codexHome, "hooks.json")
	writeFixture(t, hooks, "{ this is not JSON\n")

	var transcript bytes.Buffer
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{},
		CodexHomes: []string{codexHome}, Stdout: &transcript,
	}); err != nil {
		t.Fatalf("one malformed Codex hooks file aborted the install: %v\n%s", err, transcript.String())
	}
	if !strings.Contains(transcript.String(), "invalid Codex hooks JSON at "+hooks) {
		t.Fatalf("the malformed Codex hooks file was not named as a skip:\n%s", transcript.String())
	}
	if got := readFixture(t, hooks); got != "{ this is not JSON\n" {
		t.Fatalf("the installer rewrote a Codex hooks file it could not parse: %q", got)
	}
	// The steps that used to be stranded behind the abort all ran.
	if _, err := os.Stat(SourceRepoPath(home)); err == nil {
		t.Fatal("unexpected source-repo marker: this fixture records no clone")
	}
	for _, path := range []string{
		filepath.Join(home, ".local", "share", "pfm", "install", "binary-ownership.json"),
		filepath.Join(home, ".zshrc"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("install stopped before writing %s: %v\n%s", path, err, transcript.String())
		}
	}
}

// TestMalformedCodexHooksStillRefusesToStrandOwnedHooksOnUninstall pins the
// one case that must NOT degrade to a skip: uninstalling a seat whose
// hooks.json is unparseable while the ownership ledger says pfm hooks live in
// it. Skipping there would leave the operator with pfm hook entries nothing
// will ever remove, so the refusal stays an error naming the file.
func TestMalformedCodexHooksStillRefusesToStrandOwnedHooksOnUninstall(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	hooks := filepath.Join(codexHome, "hooks.json")

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, CodexHomes: []string{codexHome},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(hooks); err != nil {
		t.Fatalf("install wrote no Codex hooks file to own: %v", err)
	}
	writeFixture(t, hooks, "{ broken after the install that owns it\n")

	_, err := Run(context.Background(), Options{
		Mode: ModeUninstall, Home: home, Runner: &fakeRunner{}, CodexHomes: []string{codexHome},
	})
	if err == nil {
		t.Fatal("uninstall silently stranded pfm-owned hooks in an unparseable Codex hooks file")
	}
	if !strings.Contains(err.Error(), "refuse to strand owned hooks") || !strings.Contains(err.Error(), hooks) {
		t.Fatalf("uninstall error did not name the stranded ownership at %s: %v", hooks, err)
	}
}
