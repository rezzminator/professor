package hookentry

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// TestRefreshSeatRunRewritesTheLaunchMCPFile pins the start door every Herdr
// seat runs: a Claude recipe whose launch MCP file was pruned starts with the
// file present again, holding the current rendering; another engine's recipe
// is left as recorded.
func TestRefreshSeatRunRewritesTheLaunchMCPFile(t *testing.T) {
	for _, tc := range []struct {
		engine pfmengine.ID
		want   string
	}{
		{pfmengine.Claude, `{"mcpServers":{}}`},
		{pfmengine.Codex, ""},
	} {
		t.Run(string(tc.engine), func(t *testing.T) {
			home := t.TempDir()
			file := filepath.Join(paths.ClaudeMCPConfigDir(home), "claude-mcp-5e6f7a8b.json")
			binary := filepath.Join(home, "engine")
			recorded := []string{"--resume", "a-session", "--mcp-config", file}
			run, err := refreshSeatRun(
				fleetdb.SeatRun{Binary: binary, CWD: home, Args: recorded},
				fleetdb.Seat{Name: "desk", Engine: string(tc.engine)},
				config.Runtime{Config: config.Defaults(home, nil), Paths: paths.Values{Home: home}},
				io.Discard,
			)
			if err != nil {
				t.Fatal(err)
			}
			if got := run.Args[len(run.Args)-1]; got != file {
				t.Fatalf("recipe MCP word = %q, want the recorded %q", got, file)
			}
			body, err := os.ReadFile(file)
			if tc.want == "" {
				if err == nil {
					t.Fatalf("%s engine recipe wrote %s: %s", tc.engine, file, body)
				}
				return
			}
			if err != nil || string(body) != tc.want {
				t.Fatalf("launch MCP file %s = %q (%v), want %q", file, body, err, tc.want)
			}
		})
	}
}
