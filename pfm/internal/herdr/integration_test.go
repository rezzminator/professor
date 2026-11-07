package herdr

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// herdrClaudeEntry is the SessionStart entry Herdr 0.9.5's
// `herdr integration install claude` writes for dir. It copies
// testjail.HerdrClaudeSettings' entry because testjail imports config, which
// imports this package.
func herdrClaudeEntry(dir string) string {
	return `{"matcher":"^(startup|resume|clear|compact|fork)$","hooks":[{"type":"command","command":` +
		fmt.Sprintf("%q", "bash '"+dir+"/hooks/herdr-agent-state.sh' session") + `,"timeout":10}]}`
}

// TestClaudeStoresGroupsDirsByPhysicalSettings is the store door's table: dirs
// sharing one settings.json through symlinks are one store, the chosen dir
// follows the single Herdr hook, and an unreadable file is an error, never none.
func TestClaudeStoresGroupsDirsByPhysicalSettings(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		// shared is the content of the store cc1 and cc2 link to; own is
		// cc3's own settings.json ("" leaves it absent).
		shared, own string
		want        []ClaudeStore
		wantErr     string
	}{
		{
			name:   "no hook chooses the first roster dir",
			shared: `{"theme":"dark"}`,
			want: []ClaudeStore{
				{Settings: "{store}", Dirs: []string{"{cc1}", "{cc2}"}, Chosen: "{cc1}"},
				{Settings: "{cc3}/settings.json", Dirs: []string{"{cc3}"}, Chosen: "{cc3}"},
			},
		},
		{
			name: "one hook chooses the dir it names, outside the roster too",
			shared: `{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":"pfm hook"}]}],` +
				`"SessionStart":[` + herdrClaudeEntry("{root}/.claude") + `]}}`,
			own: `{"hooks":{"SessionStart":[` + herdrClaudeEntry("{cc3}") + `]}}`,
			want: []ClaudeStore{
				{
					Settings: "{store}", Dirs: []string{"{cc1}", "{cc2}"}, Chosen: "{root}/.claude",
					Hooks: []string{"bash '{root}/.claude/hooks/herdr-agent-state.sh' session"},
				},
				{
					Settings: "{cc3}/settings.json", Dirs: []string{"{cc3}"}, Chosen: "{cc3}",
					Hooks: []string{"bash '{cc3}/hooks/herdr-agent-state.sh' session"},
				},
			},
		},
		{
			name: "two hooks keep the first roster dir",
			shared: `{"hooks":{"SessionStart":[` + herdrClaudeEntry("{cc2}") + `,` + herdrClaudeEntry("{cc1}") +
				`]}}`,
			want: []ClaudeStore{
				{
					Settings: "{store}", Dirs: []string{"{cc1}", "{cc2}"}, Chosen: "{cc1}",
					Hooks: []string{
						"bash '{cc2}/hooks/herdr-agent-state.sh' session",
						"bash '{cc1}/hooks/herdr-agent-state.sh' session",
					},
				},
				{Settings: "{cc3}/settings.json", Dirs: []string{"{cc3}"}, Chosen: "{cc3}"},
			},
		},
		{
			name:    "undecodable store is an error",
			shared:  `{"hooks":`,
			wantErr: "decode Claude settings {store}: unexpected end of JSON input",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := filepath.Join(root, strings.ReplaceAll(tc.name, " ", "-"))
			cc1, cc2, cc3 := filepath.Join(base, "cc1"), filepath.Join(base, "cc2"), filepath.Join(base, "cc3")
			store := filepath.Join(base, "store", "settings.json")
			fill := strings.NewReplacer("{root}", base, "{store}", store, "{cc1}", cc1, "{cc2}", cc2, "{cc3}", cc3)
			for _, dir := range []string{cc1, cc2, cc3, filepath.Dir(store)} {
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(store, []byte(fill.Replace(tc.shared)), 0o600); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{cc1, cc2} {
				if err := os.Symlink(store, filepath.Join(dir, claudeSettingsName)); err != nil {
					t.Fatal(err)
				}
			}
			if tc.own != "" {
				if err := os.WriteFile(
					filepath.Join(cc3, claudeSettingsName),
					[]byte(fill.Replace(tc.own)),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			got := ClaudeStores([]string{cc1, cc3, cc2, cc1})
			if tc.wantErr != "" {
				if len(got) != 2 || got[0].Err == nil || got[0].Err.Error() != fill.Replace(tc.wantErr) ||
					got[0].Hooks != nil || got[1].Err != nil {
					t.Fatalf("ClaudeStores = %+v, want the store's error %q", got, fill.Replace(tc.wantErr))
				}
				return
			}
			want := make([]ClaudeStore, len(tc.want))
			for at, store := range tc.want {
				store.Settings, store.Chosen = fill.Replace(store.Settings), fill.Replace(store.Chosen)
				for i := range store.Dirs {
					store.Dirs[i] = fill.Replace(store.Dirs[i])
				}
				for i := range store.Hooks {
					store.Hooks[i] = fill.Replace(store.Hooks[i])
				}
				want[at] = store
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("ClaudeStores =\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}

// The install argv and its shell line are one door doctor and the installer
// share.
func TestClaudeIntegrationCommand(t *testing.T) {
	if got, want := ClaudeIntegrationArgv(
		"/opt/herdr",
	), []string{
		"/opt/herdr",
		"integration",
		"install",
		"claude",
	}; !reflect.DeepEqual(
		got,
		want,
	) {
		t.Fatalf("ClaudeIntegrationArgv = %q, want %q", got, want)
	}
	for _, tc := range []struct{ binary, want string }{
		{Binary, `CLAUDE_CONFIG_DIR='/h/it'"'"'s' herdr integration install claude`},
		{"/opt/my herdr", `CLAUDE_CONFIG_DIR='/h/it'"'"'s' '/opt/my herdr' integration install claude`},
	} {
		if got := ClaudeIntegrationCommand(tc.binary, "/h/it's"); got != tc.want {
			t.Fatalf("ClaudeIntegrationCommand(%q) = %q, want %q", tc.binary, got, tc.want)
		}
	}
}
