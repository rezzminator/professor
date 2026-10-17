package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestClaudeEnvReportsPreservedPluginLogConflict(t *testing.T) {
	for _, other := range []bool{false, true} {
		t.Run(map[bool]string{false: "only log key", true: "with another edit"}[other], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			writeFixture(t, path, `{"env":{"PLUGIN_LOG_DIR":"operator"}}`)
			var out bytes.Buffer
			installer := &engine{options: Options{Stdout: &out}, apply: true}
			want := map[string]string{PluginLogDirEnv: "configured"}
			if other {
				want["OTHER"] = "new"
			}
			owned, err := installer.ensureClaudeEnvFile(path, path, nil, want)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte(`"operator"`)) || owned[PluginLogDirEnv] != "" {
				t.Fatalf("operator value lost or adopted: %s, %v", raw, owned)
			}
			if !strings.Contains(out.String(), "skip") || !strings.Contains(out.String(), PluginLogDirEnv) ||
				!strings.Contains(out.String(), "hand-set") ||
				strings.Contains(out.String(), "ok      claude.env:") {
				t.Fatalf("preserved mismatch reported as health: %s", out.String())
			}
		})
	}
}
