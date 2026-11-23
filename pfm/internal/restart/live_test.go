package restart

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestLiveDepsReadsTheDaemonFromConfig(t *testing.T) {
	runtime := config.Runtime{Paths: paths.Values{StateDB: "/jail/state/fleet.db", Home: "/jail"}}
	plain := LiveDeps(runtime, &bytes.Buffer{}, []int{9}, chatAddress{Socket: "pfm-cc-a"})
	if plain.DaemonProbe != nil || plain.LaunchedFrom.Socket != "pfm-cc-a" ||
		!slices.Equal(plain.Spare, []int{9}) {
		t.Fatalf("plain deps = %+v", plain)
	}
	if plain.LogDir != "/jail/.local/state/pfm/log/restart" {
		t.Fatalf("log dir = %q, want the jailed home's log root", plain.LogDir)
	}
	runtime.Config.MCPServers = map[string]config.MCPServer{"chat": {Enabled: true}}
	configured := LiveDeps(runtime, &bytes.Buffer{}, nil, chatAddress{})
	if configured.DaemonProbe == nil {
		t.Fatalf("configured deps: daemon probe set %v", configured.DaemonProbe != nil)
	}
}

// TestLiveDepsReadsHerdrsRestoreSetting: the live restore decision reads the
// Herdr config the server reads, so resume_agents_on_restore turned off is
// seen as off.
func TestLiveDepsReadsHerdrsRestoreSetting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("[session]\nresume_agents_on_restore = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HERDR_CONFIG_PATH", path)
	runtime := config.Runtime{Paths: paths.Values{StateDB: "/jail/state/fleet.db", Home: "/jail"}}
	on, err := LiveDeps(runtime, &bytes.Buffer{}, nil, chatAddress{}).ResumeOnRestore()
	if on || err != nil {
		t.Fatalf("resume_agents_on_restore = %v, %v; want false from %s", on, err, path)
	}
}
