package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVSCodeCanonicalProfileCarriesIconAndColourAndUpgradesTheIconlessShape
// pins issue #24 9c: the canonical PFM profile carried no icon/color while
// the extension shipped beside it defines 15 cycling icons and 6 colours for
// its own contributed profile. A settings file already holding today's
// icon-less shape (owned) is upgraded to carry both on the next apply, and
// an operator-edited profile still relinquishes exactly as any other legacy
// upgrade would.
func TestVSCodeCanonicalProfileCarriesIconAndColourAndUpgradesTheIconlessShape(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	writeFixture(t, settings, `{
  "terminal.integrated.profiles.linux": {
    "PFM": {"path":"/bin/zsh","args":["-l"],"env":{"PFM_AUTO_OPEN":"pfm","CLAUDECODE":null,"CLAUDE_CODE_SESSION_ID":null,"CLAUDE_CODE_CHILD_SESSION":null,"TMUX":null,"TMUX_PANE":null}}
  },
  "terminal.integrated.defaultProfile.linux": "PFM"
}`)
	writeVSCodeOwnershipFixture(t, home, vscodeOwnershipRecord{
		Path: settings, Platform: "linux", ProfileOwned: true, DefaultOwned: true,
	})

	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &bytes.Buffer{},
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}); err != nil {
		t.Fatal(err)
	}
	got := readFixture(t, settings)
	if !strings.Contains(got, `"icon": "mortar-board"`) || !strings.Contains(got, `"color": "terminal.ansiMagenta"`) {
		t.Fatalf("owned icon-less canonical profile was not upgraded with icon/colour:\n%s", got)
	}

	ownershipPath := filepath.Join(home, ".local", "share", "pfm", "install", vscodeOwnershipName)
	owned, err := os.ReadFile(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	var document vscodeOwnershipDocument
	if err := json.Unmarshal(owned, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Files) != 1 || !document.Files[0].ProfileOwned {
		t.Fatalf("ownership was not kept through the icon/colour upgrade: %#v", document)
	}

	// An operator edit after this upgrade still relinquishes, same as every
	// other profile field.
	edited := strings.Replace(
		readFixture(t, settings),
		`"color": "terminal.ansiMagenta"`,
		`"color": "terminal.ansiGreen"`,
		1,
	)
	if err := os.WriteFile(settings, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), Options{
		Mode: ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &bytes.Buffer{},
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}); err != nil {
		t.Fatal(err)
	}
	afterEdit := readFixture(t, settings)
	if !strings.Contains(afterEdit, `"color": "terminal.ansiGreen"`) {
		t.Fatalf("operator-edited profile was overwritten instead of relinquished:\n%s", afterEdit)
	}
}
