package installer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// The door judges only a store whose one Herdr hook is herdr-shield's guarded
// form: it skips with the owner's steps while Herdr's default_shell still runs
// herdr-shield, clears the hook for unwrapping once it does not, and names an
// unreadable Herdr config.
func TestRetireHerdrShieldHook(t *testing.T) {
	const store = "/fixture/settings.json"
	guarded := `[ -n "${HERDR_SHIELD_INNER:-}" ] || sh '/fixture/hooks/herdr-agent-state.sh' session`
	for _, tc := range []struct {
		name, config string
		hooks        []string
		want         HerdrShieldRetirement
		// wantErr is the whole error of an unreadable Herdr config.
		wantErr string
	}{
		{
			name: "guarded hook while Herdr runs herdr-shield", config: "[terminal]\ndefault_shell = \"/opt/bin/herdr-shield -l\"\n",
			hooks: []string{guarded},
			want: HerdrShieldRetirement{Guarded: true, Skip: "herdr claude integration in /fixture/settings.json: " +
				"Herdr's default_shell in {config} still names herdr-shield, so its guarded hook stays — end the chats in Herdr," +
				` remove default_shell = "/opt/bin/herdr-shield -l" from {config}, restart Herdr, then run pfm install --yes again`},
		},
		{name: "guarded hook once Herdr runs another shell", config: "[terminal]\ndefault_shell = \"zsh\"\n", hooks: []string{guarded}, want: HerdrShieldRetirement{Guarded: true}},
		{name: "guarded hook with no Herdr config", hooks: []string{guarded}, want: HerdrShieldRetirement{Guarded: true}},
		{
			name: "guarded hook under an unreadable Herdr config", config: "[terminal\n", hooks: []string{guarded},
			want: HerdrShieldRetirement{Guarded: true},
			wantErr: "herdr claude integration in /fixture/settings.json: read Herdr's default_shell: parse Herdr config" +
				` {config}: toml: line 2: expected '.' or ']' to end table name, but got '\n' instead`,
		},
		{name: "plain hook", config: "[terminal]\ndefault_shell = \"herdr-shield\"\n", hooks: []string{"sh '/fixture/hooks/herdr-agent-state.sh' session"}},
		{name: "two hooks", config: "[terminal]\ndefault_shell = \"herdr-shield\"\n", hooks: []string{guarded, guarded}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			config := filepath.Join(t.TempDir(), "config.toml")
			if tc.config != "" {
				if err := os.WriteFile(config, []byte(tc.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			env := &paths.MapEnv{Values: map[string]string{"HERDR_CONFIG_PATH": config}}
			got := RetireHerdrShieldHook(herdr.ClaudeStore{Settings: store, Hooks: tc.hooks}, t.TempDir(), env)
			gotErr := ""
			if got.Err != nil {
				gotErr = got.Err.Error()
			}
			if wantErr := strings.ReplaceAll(tc.wantErr, "{config}", config); gotErr != wantErr {
				t.Fatalf("error %q; want %q", gotErr, wantErr)
			}
			got.Err = nil
			want := tc.want
			want.Skip = strings.ReplaceAll(want.Skip, "{config}", config)
			if got != want {
				t.Fatalf("verdict\n%+v\nwant\n%+v", got, want)
			}
		})
	}
}
