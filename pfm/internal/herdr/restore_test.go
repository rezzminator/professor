package herdr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestResumeOnRestoreReadsTheServersConfig(t *testing.T) {
	for _, scenario := range []struct {
		name                 string
		xdg, override, debug bool
		want                 string
	}{
		{name: "home", want: "false <nil>"},
		{name: "XDG", xdg: true, want: "false <nil>"},
		{name: "explicit wins", xdg: true, override: true, debug: true, want: "false <nil>"},
		{name: "debug ambiguous", debug: true, want: "false debug Herdr config exists at {home}/.config/herdr-dev/config.toml; set HERDR_CONFIG_PATH to identify the server's config"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			home := t.TempDir()
			env := &paths.MapEnv{Values: map[string]string{paths.EnvHome: home}}
			root := filepath.Join(home, ".config")
			if scenario.xdg {
				root = filepath.Join(home, "xdg")
				env.Values["XDG_CONFIG_HOME"] = root
			}
			config := filepath.Join(root, "herdr", "config.toml")
			if scenario.override {
				config = filepath.Join(home, "custom.toml")
				env.Values["HERDR_CONFIG_PATH"] = config
			}
			if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(config, []byte("[session]\nresume_agents_on_restore = false\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if scenario.debug {
				debugPath := filepath.Join(root, "herdr-dev", "config.toml")
				if err := os.MkdirAll(filepath.Dir(debugPath), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(
					debugPath,
					[]byte("[session]\nresume_agents_on_restore = true\n"),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}
			resume, err := ResumeOnRestore(env)
			if got := strings.ReplaceAll(fmt.Sprint(resume, " ", err), home, "{home}"); got != scenario.want {
				t.Fatalf("resume and error = %q, want %q", got, scenario.want)
			}
		})
	}
}

// TestResumePendingYieldsOnlyARestoredPane: a pane Herdr restored with its
// agent's session is Herdr's to resume; a fresh pane, an ended agent's pane,
// a failed restore and a server with restore off are not; a pane Herdr could
// not be asked about is an error, never "not pending".
func TestResumePendingYieldsOnlyARestoredPane(t *testing.T) {
	const session = `"agent_session":{"source":"herdr:claude","agent":"claude","kind":"id","value":"0b5c"}`
	for _, testCase := range []struct {
		name, answer, config string
		want                 bool
		wantErr              string
	}{
		{name: "restored", answer: `{"type":"pane_info","pane":{"pane_id":"w1:p1",` + session + `}}`, want: true},
		{name: "fresh", answer: `{"type":"pane_info","pane":{"pane_id":"w1:p1"}}`},
		{name: "session null", answer: `{"type":"pane_info","pane":{"pane_id":"w1:p1","agent_session":null}}`},
		{
			name:   "restore failed",
			answer: `{"type":"pane_info","pane":{"pane_id":"w1:p1",` + session + `,"restore_error":"resume exited 1"}}`,
		},
		{
			name:   "restore off",
			answer: `{"type":"pane_info","pane":{"pane_id":"w1:p1",` + session + `}}`,
			config: "[session]\nresume_agents_on_restore = false\n",
		},
		{name: "server gone", answer: "error:server_unavailable", wantErr: "server_unavailable"},
		{
			name:    "another pane",
			answer:  `{"type":"pane_info","pane":{"pane_id":"w9:p9",` + session + `}}`,
			wantErr: `pane w1:p1 answered as another pane: {"type":"pane_info","pane":{"pane_id":"w9:p9",` + session + `}}`,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			home := t.TempDir()
			env := &paths.MapEnv{Values: map[string]string{paths.EnvHome: home}}
			if testCase.config != "" {
				config := filepath.Join(home, ".config", "herdr", "config.toml")
				if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config, []byte(testCase.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			fake := serveHerdr(t, map[string]string{"pane.get": testCase.answer})
			pending, err := ResumePending(context.Background(), env, fake.path, "w1:p1")
			switch {
			case testCase.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
					t.Fatalf("ResumePending() = %v, %v; want error %q", pending, err, testCase.wantErr)
				}
			case err != nil || pending != testCase.want:
				t.Fatalf("ResumePending() = %v, %v; want %v, nil", pending, err, testCase.want)
			}
			want := 1
			if testCase.config != "" {
				want = 0
			}
			if asked := fake.count("pane.get"); asked != want {
				t.Fatalf("pane.get asked %d times, want %d", asked, want)
			}
		})
	}
}
