package installer

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestPluginNameHeld: a retired plugin's log folder is still held while an id
// of its bare name the retired list does not name is keyed, enabled or not, or
// installed in any config dir; a longer name sharing its prefix, or a retired
// id, holds nothing; an unreadable record is an error, never an answer.
func TestPluginNameHeld(t *testing.T) {
	retiredIDs := []string{"tinsel@professor", "tinsel@inline"}
	for _, tc := range []struct {
		name, settings, installs, wantErr string
		held                              bool
	}{
		{
			name:     "another marketplace's id keyed disabled holds it",
			settings: `{"enabledPlugins":{"tinsel@other":false}}`, held: true,
		},
		{
			name:     "another marketplace's id installed holds it",
			installs: `{"version":2,"plugins":{"tinsel@other":[]}}`, held: true,
		},
		{
			name:     "only retired ids hold nothing",
			settings: `{"enabledPlugins":{"tinsel@professor":true}}`,
			installs: `{"version":2,"plugins":{"tinsel@inline":[]}}`,
		},
		{
			name:     "a longer name sharing the prefix holds nothing",
			settings: `{"enabledPlugins":{"tinselly@other":true}}`,
			installs: `{"version":2,"plugins":{"tinsel-x@other":[]}}`,
		},
		{
			name:     "an unreadable settings file is an error",
			settings: `{"enabledPlugins":`, wantErr: "parse {second}/settings.json: unexpected EOF",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			first, second := t.TempDir(), t.TempDir()
			if tc.settings != "" {
				writeFixture(t, filepath.Join(second, "settings.json"), tc.settings)
			}
			if tc.installs != "" {
				writeFixture(t, filepath.Join(second, "plugins", "installed_plugins.json"), tc.installs)
			}
			held, err := pluginNameHeld([]string{first, second}, "tinsel", retiredIDs)
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			wantErr := strings.ReplaceAll(tc.wantErr, "{second}", second)
			if held != tc.held || gotErr != wantErr {
				t.Fatalf("pluginNameHeld = %t, %q; want %t, %q (dir %s)", held, gotErr, tc.held, wantErr, second)
			}
		})
	}
}
