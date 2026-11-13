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

// vscodeCanonicalSettings is the settings file an owned PFM profile in any
// recognized legacy shape is upgraded to: vscodeProfile() with every inherited
// chat-identity variable spelled as JSON null, the default profile kept.
const vscodeCanonicalSettings = `{
  "terminal.integrated.profiles.linux": {
    "PFM": {
  "args": [
    "-l"
  ],
  "color": "terminal.ansiMagenta",
  "env": {
    "CLAUDECODE": null,
    "CLAUDE_CODE_CHILD_SESSION": null,
    "CLAUDE_CODE_SESSION_ID": null,
    "CLAUDE_CONFIG_DIR": null,
    "CODEX_THREAD_ID": null,
    "PFM_AUTO_OPEN": "pfm",
    "PFM_CLAUDE_CONFIG_DIR_DEFAULT": null,
    "TMUX": null,
    "TMUX_PANE": null
  },
  "icon": "mortar-board",
  "path": "/bin/zsh"
}
  },
  "terminal.integrated.defaultProfile.linux": "PFM"
}`

// TestVSCodeLegacyProfileMigration pins what install and uninstall do to a
// profile pfm owns in an earlier shape: an exact legacy or previous-canonical
// shape is upgraded (install) or removed alone (uninstall); a shape an
// operator edited is left byte-for-byte alone and its ownership given up.
func TestVSCodeLegacyProfileMigration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		mode         Mode
		settings     string
		defaultOwned bool
		want         string // "" = settings left unchanged
		wantLedger   string // the ownership ledger, {home} for the home; "" = no ledger
	}{
		{
			name: "owned legacy auto-open upgrades", mode: ModeApply, defaultOwned: true,
			settings: `{
  "terminal.integrated.profiles.linux": {
    "PFM": {"path":"/bin/zsh","args":["-l"],"env":{"CC_AUTO_OPEN":"pfm"}}
  },
  "terminal.integrated.defaultProfile.linux": "PFM"
}`,
			want:       vscodeCanonicalSettings,
			wantLedger: "{\n  \"version\": 1,\n  \"files\": [\n    {\n      \"path\": \"{home}/settings.json\",\n      \"platform\": \"linux\",\n      \"profileOwned\": true,\n      \"defaultOwned\": true\n    }\n  ]\n}\n",
		},
		{
			name: "owned previous canonical upgrades to null env", mode: ModeApply, defaultOwned: true,
			settings: `{
  "terminal.integrated.profiles.linux": {
    "PFM": {"path":"/bin/zsh","args":["-l"],"env":{"PFM_AUTO_OPEN":"pfm"}}
  },
  "terminal.integrated.defaultProfile.linux": "PFM"
}`,
			want:       vscodeCanonicalSettings,
			wantLedger: "{\n  \"version\": 1,\n  \"files\": [\n    {\n      \"path\": \"{home}/settings.json\",\n      \"platform\": \"linux\",\n      \"profileOwned\": true,\n      \"defaultOwned\": true\n    }\n  ]\n}\n",
		},
		{
			name: "customized legacy auto-open is preserved", mode: ModeApply,
			settings:   `{"terminal.integrated.profiles.linux":{"PFM":{"path":"/bin/zsh","args":["-l"],"env":{"CC_AUTO_OPEN":"operator-choice"}}}}`,
			wantLedger: "",
		},
		{
			name: "hand-edited previous canonical is preserved", mode: ModeApply,
			settings:   `{"terminal.integrated.profiles.linux":{"PFM":{"path":"/bin/zsh","args":["-l"],"env":{"PFM_AUTO_OPEN":"operator-choice"}}}}`,
			wantLedger: "",
		},
		{
			name: "uninstall removes the owned legacy profile alone", mode: ModeUninstall,
			settings:   `{"terminal.integrated.profiles.linux":{"zsh":{"path":"/bin/zsh"},"PFM":{"path":"/bin/zsh","args":["-l"],"env":{"CC_AUTO_OPEN":"pfm"}}}}`,
			want:       `{"terminal.integrated.profiles.linux":{"zsh":{"path":"/bin/zsh"}}}`,
			wantLedger: "",
		},
		{
			name: "uninstall preserves an edited legacy profile", mode: ModeUninstall,
			settings:   `{"terminal.integrated.profiles.linux":{"PFM":{"path":"/operator/zsh","args":["-l"],"env":{"CC_AUTO_OPEN":"pfm"}}}}`,
			wantLedger: "{\n  \"version\": 1,\n  \"files\": [\n    {\n      \"path\": \"{home}/settings.json\",\n      \"platform\": \"linux\",\n      \"profileOwned\": true\n    }\n  ]\n}\n",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := vscodeLawHome(t)
			settings := filepath.Join(home, "settings.json")
			writeFixture(t, settings, tc.settings)
			writeVSCodeOwnershipFixture(t, home, vscodeOwnershipRecord{
				Path: settings, Platform: "linux", ProfileOwned: true, DefaultOwned: tc.defaultOwned,
			})
			if _, err := Run(context.Background(), Options{
				MCPConfigPath: testConfigPath(t),
				Mode:          tc.mode, Home: home, Runner: &fakeRunner{}, Stdout: &bytes.Buffer{},
				vscodePlatform: "linux", vscodeSettingsPaths: []string{settings},
			}); err != nil {
				t.Fatal(err)
			}
			want := tc.want
			if want == "" {
				want = tc.settings
			}
			if got := readFixture(t, settings); got != want {
				t.Errorf("%s =\n%s\nwant\n%s", settings, got, want)
			}
			ledgerPath := filepath.Join(home, ".local", "share", "pfm", "install", vscodeOwnershipName)
			ledger, err := os.ReadFile(ledgerPath)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			if got := strings.ReplaceAll(string(ledger), home, "{home}"); got != tc.wantLedger {
				t.Errorf("%s = %q, want %q", ledgerPath, got, tc.wantLedger)
			}
		})
	}
}

func writeVSCodeOwnershipFixture(t *testing.T, home string, record vscodeOwnershipRecord) {
	t.Helper()
	ledger, err := json.Marshal(vscodeOwnershipDocument{
		Version: vscodeOwnershipVersion,
		Files:   []vscodeOwnershipRecord{record},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(home, ".local", "share", "pfm", "install", vscodeOwnershipName), string(ledger))
}
