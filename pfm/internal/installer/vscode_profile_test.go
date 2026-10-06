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

	"github.com/rezzminator/professor/pfm/internal/claudelaunch"
)

// TestVSCodeCanonicalProfileCarriesIconAndColourAndUpgradesTheIconlessShape
// pins issue #24 9c: the canonical PFM profile carried no icon/color while
// the extension shipped beside it defines 15 cycling icons and 6 colours for
// its own contributed profile. A settings file already holding today's
// icon-less shape (owned) is upgraded to carry both on the next apply, and
// an operator-edited profile still relinquishes exactly as any other legacy
// upgrade would.
func TestVSCodeCanonicalProfileCarriesIconAndColourAndUpgradesTheIconlessShape(t *testing.T) {
	t.Parallel()
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
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &bytes.Buffer{},
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
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &bytes.Buffer{},
		vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
	}); err != nil {
		t.Fatal(err)
	}
	afterEdit := readFixture(t, settings)
	if !strings.Contains(afterEdit, `"color": "terminal.ansiGreen"`) {
		t.Fatalf("operator-edited profile was overwritten instead of relinquished:\n%s", afterEdit)
	}
}

func TestVSCodeProfileShapesPinned(t *testing.T) {
	t.Parallel()
	t.Run("canonical", func(t *testing.T) {
		want := map[string]any{"PFM_AUTO_OPEN": "pfm", "TMUX": nil, "TMUX_PANE": nil}
		for _, name := range claudelaunch.IdentityHygiene() {
			want[name] = nil
		}
		if len(claudelaunch.IdentityHygiene()) != 6 || !reflect.DeepEqual(vscodeProfile()["env"], want) {
			t.Fatalf("canonical env=%#v want %#v with six identity names", vscodeProfile()["env"], want)
		}
	})
	t.Run("legacy", func(t *testing.T) {
		literals := []string{
			`{"path":"/bin/zsh","args":["-l"],"env":{"CC_AUTO_OPEN":"pfm"}}`,
			`{"path":"/bin/zsh","args":["-l"],"env":{"PFM_AUTO_OPEN":"pfm"}}`,
			`{"path":"/bin/zsh","args":["-l"],"env":{"PFM_AUTO_OPEN":"pfm","TMUX":null,"TMUX_PANE":null,"CLAUDE_CODE_SESSION_ID":null,"CLAUDECODE":null,"CLAUDE_CODE_CHILD_SESSION":null}}`,
			`{"path":"/bin/zsh","args":["-l"],"env":{"PFM_AUTO_OPEN":"pfm","TMUX":null,"TMUX_PANE":null,"CLAUDE_CODE_SESSION_ID":null,"CLAUDECODE":null,"CLAUDE_CODE_CHILD_SESSION":null},"icon":"mortar-board","color":"terminal.ansiMagenta"}`,
			`{"path":"/bin/zsh","args":["-l"],"env":{"PFM_AUTO_OPEN":"pfm","TMUX":null,"TMUX_PANE":null,"CLAUDE_CODE_SESSION_ID":null,"CLAUDECODE":null,"CLAUDE_CODE_CHILD_SESSION":null,"CLAUDE_CONFIG_DIR":null,"CODEX_THREAD_ID":null},"icon":"mortar-board","color":"terminal.ansiMagenta"}`,
		}
		if len(vscodeLegacyProfiles) != len(literals) {
			t.Fatalf("legacy profiles=%d want %d", len(vscodeLegacyProfiles), len(literals))
		}
		for i, literal := range literals {
			var want map[string]any
			if err := json.Unmarshal([]byte(literal), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(vscodeLegacyProfiles[i], want) {
				t.Errorf("legacy %d=%#v want %#v", i, vscodeLegacyProfiles[i], want)
			}
		}
	})
}

func TestVSCodeDevelopEraProfileUpgrade(t *testing.T) {
	for _, owned := range []bool{true, false} {
		name := "relinquished"
		if owned {
			name = "owned"
		}
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			settings := filepath.Join(home, "settings.json")
			writeFixture(
				t,
				settings,
				`{"terminal.integrated.profiles.linux":{"PFM":{"path":"/bin/zsh","args":["-l"],"env":{"PFM_AUTO_OPEN":"pfm","TMUX":null,"TMUX_PANE":null,"CLAUDE_CODE_SESSION_ID":null,"CLAUDECODE":null,"CLAUDE_CODE_CHILD_SESSION":null,"CLAUDE_CONFIG_DIR":null,"CODEX_THREAD_ID":null},"icon":"mortar-board","color":"terminal.ansiMagenta"}},"terminal.integrated.defaultProfile.linux":"PFM"}`,
			)
			writeVSCodeOwnershipFixture(t, home, vscodeOwnershipRecord{
				Path: settings, Platform: "linux", ProfileOwned: owned, DefaultOwned: true,
			})
			if _, err := Run(context.Background(), Options{
				MCPConfigPath: testConfigPath(t), Mode: ModeApply, Home: home,
				Runner: &fakeRunner{}, Stdout: &bytes.Buffer{}, VSCode: !owned,
				PrimaryConfigDir: filepath.Join(home, "primary"),
				vscodePlatform:   "linux", vscodeSettingsPaths: []string{settings},
			}); err != nil {
				t.Fatalf("develop-era profile upgrade refused: %v", err)
			}
			doc, err := decodeJSONCObject([]byte(readFixture(t, settings)))
			if err != nil {
				t.Fatal(err)
			}
			if got := doc["terminal.integrated.profiles.linux"].(map[string]any)["PFM"]; !reflect.DeepEqual(
				got,
				vscodeProfile(),
			) {
				t.Fatalf("profile=%#v want canonical %#v", got, vscodeProfile())
			}
			ledger := readVSCodeLedgerFixture(t, managedRootForHome(home))
			if len(ledger.Files) != 1 || !ledger.Files[0].ProfileOwned || (!owned && !ledger.Files[0].EnvOwned) {
				t.Fatalf("profile/env ownership=%+v", ledger)
			}
		})
	}
}

// TestVSCodeReclaimsARelinquishedPostM8ProfileUnderVSCode pins the 2026-10-03
// host: the PFM profile pfm itself wrote after M8 added icon/color while the
// identity env still held three names was never registered as a legacy
// shape, so the upgrade that grew the env to five names read it as an
// operator edit and relinquished it, and every later `pfm install --vscode`
// refused it as "not PFM-owned" — the very command doctor printed as the fix.
// That shape, relinquished in the ledger, must be reclaimed under --vscode:
// rewritten to canonical, owned again, and CLAUDE_CONFIG_DIR written beside it.
func TestVSCodeReclaimsARelinquishedPostM8ProfileUnderVSCode(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	settings := filepath.Join(home, "settings.json")
	writeFixture(t, settings, `{
  "terminal.integrated.profiles.linux": {
    "PFM": {"path":"/bin/zsh","args":["-l"],"env":{"PFM_AUTO_OPEN":"pfm","TMUX":null,"TMUX_PANE":null,"CLAUDECODE":null,"CLAUDE_CODE_CHILD_SESSION":null,"CLAUDE_CODE_SESSION_ID":null},"icon":"mortar-board","color":"terminal.ansiMagenta"}
  },
  "terminal.integrated.defaultProfile.linux": "PFM"
}`)
	writeVSCodeOwnershipFixture(t, home, vscodeOwnershipRecord{
		Path: settings, Platform: "linux", ProfileOwned: false, DefaultOwned: true,
	})
	primary := filepath.Join(home, "primary")

	var output bytes.Buffer
	if _, err := Run(context.Background(), Options{
		MCPConfigPath: testConfigPath(t),
		Mode:          ModeApply, Home: home, Runner: &fakeRunner{}, Stdout: &output, VSCode: true,
		PrimaryConfigDir: primary,
		vscodePlatform:   "linux", vscodeSettingsPaths: []string{settings},
	}); err != nil {
		t.Fatalf("--vscode refused pfm's own relinquished profile: %v\n%s", err, output.String())
	}

	document, err := decodeJSONCObject([]byte(readFixture(t, settings)))
	if err != nil {
		t.Fatal(err)
	}
	profiles, _ := document["terminal.integrated.profiles.linux"].(map[string]any)
	canonicalRaw, err := json.Marshal(vscodeProfile())
	if err != nil {
		t.Fatal(err)
	}
	var canonical any
	if err := json.Unmarshal(canonicalRaw, &canonical); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(profiles[vscodeProfileName], canonical) {
		t.Fatalf("profile not rewritten to canonical:\ngot  %#v\nwant %#v", profiles[vscodeProfileName], canonical)
	}
	env, _ := document[vscodeEnvironmentKey].([]any)
	wrote := false
	for _, entry := range env {
		if item, ok := entry.(map[string]any); ok && item["name"] == "CLAUDE_CONFIG_DIR" && item["value"] == primary {
			wrote = true
		}
	}
	if !wrote {
		t.Fatalf("CLAUDE_CONFIG_DIR=%s not written: %#v", primary, document[vscodeEnvironmentKey])
	}
	ledger := readVSCodeLedgerFixture(t, filepath.Join(home, ".local", "share", "pfm", "install"))
	if len(ledger.Files) != 1 || !ledger.Files[0].ProfileOwned || !ledger.Files[0].EnvOwned {
		t.Fatalf("profile and env not owned after reclaim: %+v", ledger)
	}
}
