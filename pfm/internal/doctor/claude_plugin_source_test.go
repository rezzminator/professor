package doctor

import (
	"bytes"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// TestClaudePluginSourceDoctor: the source row names claude.pluginSource, the
// config source that set it, and where Professor's plugins install from.
func TestClaudePluginSourceDoctor(t *testing.T) {
	for _, test := range []struct {
		name, source, want string
	}{
		{
			"unset is the release", "",
			"doctor: claude_plugins source: claude.pluginSource=release (default) — " +
				"Professor's plugins install from github.com/rezzminator/professor\n",
		},
		{
			"a checkout host set in the file", pfmconfig.PluginSourceCheckout,
			"doctor: claude_plugins source: claude.pluginSource=checkout (file) — " +
				"Professor's plugins install from their checkouts\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := pfmconfig.Defaults(t.TempDir(), nil)
			if test.source != "" {
				config.Claude.PluginSource = test.source
				config.Sources[pfmconfig.KeyClaudePluginSource] = pfmconfig.SourceFile
			}
			var out bytes.Buffer
			printClaudePluginSourceDoctor(&out, config)
			if got := out.String(); got != test.want {
				t.Fatalf("row = %q, want %q", got, test.want)
			}
		})
	}
}
