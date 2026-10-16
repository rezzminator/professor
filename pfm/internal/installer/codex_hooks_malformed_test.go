package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/codexappendix"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestMalformedCodexHooksSkipsLoudlyAndFinishesTheRun is a REGRESSION test
// for a hand-broken ~/.codex/hooks.json aborting the whole install: the Codex
// hook path hard-returned on any parse failure, so wireMCP, wireLogDefault,
// wireShell, wireVSCode and writeUpdateMetadata never ran and the machine was
// left half-wired. Install writes no Claude account settings, so this is the
// one hooks writer that can meet a hand-broken file: it skips loudly and
// continues unless uninstalling with owned hooks.
func TestMalformedCodexHooksSkipsLoudlyAndFinishesTheRun(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	hooks := filepath.Join(codexHome, "hooks.json")
	writeFixture(t, hooks, "{ this is not JSON\n")

	var transcript bytes.Buffer
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{},
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
	if _, err := os.Stat(paths.SourceRepoPath(home)); err == nil {
		t.Fatal("unexpected source-repo marker: this fixture records no clone")
	}
	for _, path := range []string{
		filepath.Join(home, ".local", "share", "pfm", "install", "binary-ownership.json"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("install stopped before writing %s: %v\n%s", path, err, transcript.String())
		}
	}
	if !strings.Contains(transcript.String(), "skip    zshrc: no source repo recorded") {
		t.Fatalf("missing marker was not reported as a shell skip:\n%s", transcript.String())
	}
}

// TestMalformedCodexHooksStillRefusesToStrandOwnedHooksOnUninstall pins the
// one case that must NOT degrade to a skip: uninstalling a seat whose
// hooks.json is unparseable while the ownership ledger says pfm hooks live in
// it. Skipping there would leave the operator with pfm hook entries nothing
// will ever remove, so the refusal stays an error naming the file.
func TestMalformedCodexHooksStillRefusesToStrandOwnedHooksOnUninstall(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	hooks := filepath.Join(codexHome, "hooks.json")

	// pfm writes no Codex hook any more — the appendix hook is retired — so
	// the ownership an uninstall must not strand is one an OLDER install
	// recorded, which is exactly what this ledger says.
	ledger, err := encodeSettingsHookOwnership(map[string]settingsHookCounts{
		physicalSettingsPath(hooks): {
			settingsHookKey{
				Event:   "SessionStart",
				Matcher: codexappendix.Matcher,
				Command: codexappendix.Command(home),
			}: 1,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, settingsHookOwnershipPath(managedRootForHome(home)), string(ledger))
	writeFixture(t, hooks, "{ broken after the install that owns it\n")

	_, err = Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeUninstall, Home: home, Runner: &fakeRunner{}, CodexHomes: []string{codexHome},
	})
	if err == nil {
		t.Fatal("uninstall silently stranded pfm-owned hooks in an unparseable Codex hooks file")
	}
	if !strings.Contains(err.Error(), "refuse to strand owned hooks") || !strings.Contains(err.Error(), hooks) {
		t.Fatalf("uninstall error did not name the stranded ownership at %s: %v", hooks, err)
	}
}

// TestRecordedClaudeHooksRetirementKeepsAWriteMadeAfterItsRead: retiring the
// hooks pfm recorded in a Claude settings file re-reads it before the rename,
// so a chat's write after pfm's read stays, the recorded handler still goes,
// and the file as pfm found it at the write is backed up; a file the chat
// removed meanwhile has nothing left to retire and is not created again.
func TestRecordedClaudeHooksRetirementKeepsAWriteMadeAfterItsRead(t *testing.T) {
	const recorded, kept = "/pfm/hook stop", "/user/hook stop"
	given := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + recorded + `"},` +
		`{"type":"command","command":"` + kept + `"}]}]}}`
	for _, test := range []struct {
		name, chat, want string // chat "" = the file removed; want "" = no file
	}{
		{
			name: "a chat's write after the read",
			chat: strings.Replace(given, `{"hooks":{`, `{"theme":"dark","hooks":{`, 1),
			want: `{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"` + kept + `"}]}]}}`,
		},
		{name: "a file removed after the read"},
	} {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			writeFixture(t, path, given)
			edit, err := planRecordedClaudeHooksRetirement(
				path,
				settingsHookCounts{{Event: "Stop", Command: recorded}: 1},
				"s1",
			)
			if err != nil || edit == nil {
				t.Fatalf("plan: edit=%v err=%v, want an edit", edit != nil, err)
			}
			// The chat's write, or its removal of the file, lands after pfm's read.
			if test.chat == "" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				writeFixture(t, path, test.chat)
			}
			backup := availableBackup(path, "s1")
			if err := edit(); err != nil {
				t.Fatalf("edit: %v", err)
			}
			if test.want == "" {
				for _, gone := range []string{path, backup} {
					if _, err := os.Lstat(gone); !os.IsNotExist(err) {
						t.Fatalf("%s after the edit of a removed file: %v, want none", gone, err)
					}
				}
				return
			}
			var got, want any
			if err := json.Unmarshal([]byte(readFixture(t, path)), &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("settings %v, want %v", got, want)
			}
			if got := readFixture(t, backup); got != test.chat {
				t.Fatalf("backup %q, want the file before pfm's write %q", got, test.chat)
			}
		})
	}
}
