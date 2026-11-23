package installer

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testwait"
)

// TestPluginNameHeld: a retired plugin's log folder is still held while an id
// of its bare name the retired list does not name is keyed, enabled or not, in
// a config dir's settings.json or settings.local.json, or installed there (a
// project-scope install too); a longer name sharing its prefix, or a retired
// id, holds nothing; an unreadable record, a FIFO too, is an error at once,
// never an answer.
func TestPluginNameHeld(t *testing.T) {
	retiredIDs := []string{"tinsel@professor", "tinsel@inline"}
	for _, tc := range []struct {
		name, settings, local, installs, wantErr string
		fifo                                     bool
		held                                     bool
	}{
		{
			name:  "another marketplace's id enabled in settings.local.json holds it",
			local: `{"enabledPlugins":{"tinsel@other":true}}`, held: true,
		},
		{
			name: "another marketplace's id installed for one project holds it",
			installs: `{"version":2,"plugins":{"tinsel@other":[{"scope":"project","projectPath":"/work/app",` +
				`"installPath":"/x/cache/other/tinsel/1.0"}]}}`,
			held: true,
		},
		{
			name: "a settings.json that is a FIFO is an error", fifo: true,
			wantErr: "read {second}/settings.json: not a regular file",
		},
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
			if tc.local != "" {
				writeFixture(t, filepath.Join(second, "settings.local.json"), tc.local)
			}
			if tc.fifo {
				if err := syscall.Mkfifo(filepath.Join(second, "settings.json"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.installs != "" {
				writeFixture(t, filepath.Join(second, "plugins", "installed_plugins.json"), tc.installs)
			}
			type answer struct {
				held bool
				err  error
			}
			answered := make(chan answer, 1)
			go func() {
				held, err := pluginNameHeld([]string{first, second}, "tinsel", retiredIDs)
				answered <- answer{held, err}
			}()
			got, _ := testwait.Receive(t, "pluginNameHeld answers without blocking on "+second, answered)
			held, err := got.held, got.err
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
