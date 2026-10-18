package doctor

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/agentlauncher"
)

func writeLaunchAgent(t *testing.T, home, label, program string) {
	t.Helper()
	path := filepath.Join(home, "Library", "LaunchAgents", label+".plist")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "<plist><dict><key>ProgramArguments</key><array>\n\t\t<string>" + program +
		"</string>\n</array></dict></plist>\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPrintLaunchAgentsDoctor(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		setup    func(t *testing.T, home string)
		warnings int
		want     string
	}{
		{"no launch agent", func(*testing.T, string) {}, 0, ""},
		{"every agent on the launcher", func(t *testing.T, home string) {
			if err := agentlauncher.Stage(home); err != nil {
				t.Fatal(err)
			}
			writeLaunchAgent(t, home, "com.professor.pfm.mcp", agentlauncher.Program(home))
			writeLaunchAgent(t, home, "com.professor.pfm.theme", agentlauncher.Program(home))
		}, 0, "doctor: launch-agents agents=2 launcher=in-sync"},
		{"a plist still on the raw binary", func(t *testing.T, home string) {
			if err := agentlauncher.Stage(home); err != nil {
				t.Fatal(err)
			}
			writeLaunchAgent(t, home, "com.professor.pfm.schedule", agentlauncher.ExecTarget(home))
		}, 1, " — run pfm install --yes\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			test.setup(t, home)
			var out bytes.Buffer
			if got := printLaunchAgentsDoctor(&out, home); got != test.warnings {
				t.Fatalf("warnings = %d, want %d; output %q", got, test.warnings, out.String())
			}
			if !strings.Contains(out.String(), test.want) || (test.want == "" && out.Len() != 0) {
				t.Fatalf("output %q, want it to hold %q", out.String(), test.want)
			}
		})
	}
}
