package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/herdr"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func serveHerdrDoctor(t *testing.T, result string) string {
	t.Helper()
	socket := filepath.Join(testjail.ShortRoot(t), "h.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			connection, err := listener.Accept()
			if err != nil {
				return // Cleanup closes our listener after the synchronous probe.
			}
			var request struct {
				ID     string `json:"id"`
				Method string `json:"method"`
			}
			if err := json.NewDecoder(connection).Decode(&request); err != nil {
				t.Errorf("decode doctor request: %v", err)
			} else if request.Method != "ping" {
				t.Errorf("doctor method=%q, want ping", request.Method)
			} else if _, err := fmt.Fprintf(connection, "{\"id\":%q,\"result\":%s}\n", request.ID, result); err != nil {
				t.Errorf("write doctor response: %v", err)
			}
			if err := connection.Close(); err != nil {
				t.Errorf("close doctor connection: %v", err)
			}
		}
	}()
	t.Cleanup(func() {
		if err := listener.Close(); err != nil {
			t.Errorf("close doctor listener: %v", err)
		}
		<-done
	})
	return socket
}

func TestHerdrBackendDoctor(t *testing.T) {
	const healthy = `{"type":"pong","version":"0.9.5","protocol":22}`
	for _, scenario := range []struct {
		name, pong, config, platform, status string
		dead, discover, directory            bool
		warnings, failures                   int
		want                                 string
	}{
		{name: "valid protocol", pong: healthy, want: "doctor: herdr backend ok (protocol 22, SOCKET)"},
		{name: "protocol mismatch", pong: `{"type":"pong","version":"0.9.5","protocol":23}`, failures: 1, want: "doctor: herdr protocol 23 DIVERGES from supported 22 — herdr seats disabled"},
		{name: "dead socket", dead: true, failures: 1, want: "doctor: herdr backend UNREACHABLE — herdr socket SOCKET: dial for ping"},
		{name: "null result", pong: `null`, failures: 1, want: "doctor: herdr backend UNREACHABLE — herdr ping: response has no result"},
		{name: "missing protocol", pong: `{"type":"pong","version":"0.9.5"}`, failures: 1, want: "doctor: herdr backend UNREACHABLE — malformed ping result"},
		{name: "string protocol", pong: `{"type":"pong","version":"0.9.5","protocol":"22"}`, failures: 1, want: "doctor: herdr backend UNREACHABLE — malformed ping result"},
		{name: "negative protocol", pong: `{"type":"pong","version":"0.9.5","protocol":-1}`, failures: 1, want: "doctor: herdr backend UNREACHABLE — malformed ping result"},
		{name: "wrong result type", pong: `{"type":"other","version":"0.9.5","protocol":22}`, failures: 1, want: "doctor: herdr backend UNREACHABLE — malformed ping result"},
		{name: "empty version", pong: `{"type":"pong","version":"","protocol":22}`, want: "doctor: herdr backend ok (protocol 22, SOCKET)"},
		{name: "numeric version", pong: `{"type":"pong","version":95,"protocol":22}`, want: "doctor: herdr backend ok (protocol 22, SOCKET)"},
		{name: "restore disabled", pong: healthy, config: "[session]\nresume_agents_on_restore = false\n", warnings: 1, want: "doctor: herdr resume_agents_on_restore=false — Herdr chats will not come back after a restart"},
		{name: "restore explicit true", pong: healthy, config: "[session]\nresume_agents_on_restore = true\n", want: "doctor: herdr resume_agents_on_restore=true (on-disk config)"},
		{name: "restore default", pong: healthy, want: "doctor: herdr resume_agents_on_restore=true (on-disk config)"},
		{name: "BOM config", pong: healthy, config: "\ufeff[session]\nresume_agents_on_restore = false\n", warnings: 1, want: "doctor: herdr resume_agents_on_restore=false — Herdr chats will not come back after a restart"},
		{name: "empty config", pong: healthy, config: "# comment only\n", want: "doctor: herdr resume_agents_on_restore=true (on-disk config)"},
		{name: "wrong session shape", pong: healthy, config: "session = false\n", warnings: 1, want: "doctor: herdr restore config UNREADABLE —"},
		{name: "malformed config", pong: healthy, config: "[session\n", warnings: 1, want: "doctor: herdr restore config UNREADABLE —"},
		{name: "wrong config boolean", pong: healthy, config: "[session]\nresume_agents_on_restore = 'false'\n", warnings: 1, want: "doctor: herdr restore config UNREADABLE —"},
		{name: "unreadable config", pong: healthy, directory: true, warnings: 1, want: "doctor: herdr restore config UNREADABLE —"},
		{name: "macOS lifetime", pong: healthy, platform: "darwin", warnings: 1, want: "doctor: herdr macOS lifetime fix (#5236) UNVERIFIED in this Herdr build (0.9.5) — no supported fixed release established"},
		{name: "custom version does not prove lifetime fix", pong: `{"type":"pong","version":"0.9.5-preview.2099-01-01-abcd","protocol":22}`, platform: "darwin", warnings: 1, want: "doctor: herdr macOS lifetime fix (#5236) UNVERIFIED in this Herdr build (0.9.5-preview.2099-01-01-abcd) — no supported fixed release established"},
		{name: "protocol zero is not missing", pong: `{"type":"pong","version":"0.9.5","protocol":0}`, failures: 1, want: "doctor: herdr protocol 0 DIVERGES from supported 22 — herdr seats disabled"},
		{name: "protocol uint32 maximum", pong: `{"type":"pong","version":"0.9.5","protocol":4294967295}`, failures: 1, want: "doctor: herdr protocol 4294967295 DIVERGES from supported 22 — herdr seats disabled"},
		{name: "protocol overflow", pong: `{"type":"pong","version":"0.9.5","protocol":4294967296}`, failures: 1, want: "doctor: herdr backend UNREACHABLE — malformed ping result"},
		{name: "missing type", pong: `{"version":"0.9.5","protocol":22}`, failures: 1, want: "doctor: herdr backend UNREACHABLE — malformed ping result"},
		{name: "discover endpoint", pong: healthy, discover: true, want: "doctor: herdr backend ok (protocol 22, SOCKET)"},
		{name: "malformed status", discover: true, status: `broken`, failures: 1, want: "doctor: herdr backend UNREACHABLE — herdr status --json:"},
		{name: "missing status endpoint", discover: true, status: `{"server":{"running":true}}`, failures: 1, want: "doctor: herdr backend UNREACHABLE — herdr status --json:"},
		{name: "stopped status", discover: true, status: `{"server":{"running":false,"socket":"/missing"}}`, failures: 1, want: "doctor: herdr backend UNREACHABLE — herdr status --json:"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			socket := filepath.Join(testjail.ShortRoot(t), "dead.sock")
			if scenario.pong != "" {
				socket = serveHerdrDoctor(t, scenario.pong)
			}
			config := filepath.Join(t.TempDir(), "config.toml")
			if scenario.directory {
				if err := os.Mkdir(config, 0o700); err != nil {
					t.Fatal(err)
				}
			} else if scenario.config != "" {
				if err := os.WriteFile(config, []byte(scenario.config), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("HERDR_CONFIG_PATH", config)
			t.Setenv(herdr.EnvSocket, socket)
			t.Setenv(herdr.EnvBin, "")
			runner := &deps.FakeRunner{}
			if scenario.discover {
				t.Setenv(herdr.EnvSocket, "")
				runner.ScriptLookPath("herdr", "/opt/herdr", nil)
				status := scenario.status
				if status == "" {
					status = fmt.Sprintf(`{"server":{"running":true,"socket":%q}}`, socket)
				}
				runner.Script([]string{"/opt/herdr", "status", "--json"}, deps.RunResult{Stdout: []byte(status)}, nil)
			}
			platform := scenario.platform
			if platform == "" {
				platform = "linux"
			}
			var stdout bytes.Buffer
			warnings, failures := printHerdrBackendDoctor(
				context.Background(),
				&stdout,
				Dependencies{Env: paths.OSEnv{}, Runner: runner},
				platform == "darwin",
			)
			want := strings.ReplaceAll(scenario.want, "SOCKET", socket)
			if !strings.Contains(stdout.String(), want+"\n") && !strings.Contains(stdout.String(), want) {
				t.Fatalf("backend output=%q, want %q", stdout.String(), want)
			}
			if warnings != scenario.warnings || failures != scenario.failures {
				t.Fatalf(
					"warnings=%d failures=%d, want %d/%d; output=%s",
					warnings,
					failures,
					scenario.warnings,
					scenario.failures,
					stdout.String(),
				)
			}
		})
	}
}

func TestHerdrBackendDoctorThroughRun(t *testing.T) {
	runtime := buildCleanDoctorHome(t)
	runtime.Config.Chat.Backend = "herdr"
	socket := serveHerdrDoctor(t, `{"type":"pong","version":"0.9.5","protocol":23}`)
	t.Setenv(herdr.EnvSocket, socket)
	t.Setenv(herdr.EnvBin, "")
	var stdout, stderr bytes.Buffer
	code := runDoctor(nil, &stdout, &stderr, runtime)
	want := "doctor: herdr protocol 23 DIVERGES from supported 22 — herdr seats disabled"
	if code != 3 || !strings.Contains(stdout.String(), want) ||
		!strings.Contains(stdout.String(), "doctor: failures=1\n") {
		t.Fatalf("code=%d stdout=%s stderr=%s", code, stdout.String(), stderr.String())
	}
}

func TestHerdrBackendDoctorDiscoveryErrors(t *testing.T) {
	for _, scenario := range []struct {
		name        string
		lookup, run error
		exit        int
		want        string
		failures    int
	}{
		{name: "configured absent binary", failures: 1, want: "doctor: herdr backend UNREACHABLE — herdr not installed but chat.backend=herdr requires it\ndoctor: herdr backend repair — install or start Herdr, then run herdr status --json; change chat.backend if Herdr is not required\n"},
		{name: "lookup error", lookup: errors.New("permission denied"), failures: 1, want: "doctor: herdr backend UNREACHABLE — herdr on doctor's PATH: permission denied\ndoctor: herdr backend repair — install or start Herdr, then run herdr status --json; change chat.backend if Herdr is not required\n"},
		{name: "status execution error", run: errors.New("fork failed"), failures: 1, want: "doctor: herdr backend UNREACHABLE — herdr status --json: fork failed\ndoctor: herdr backend repair — install or start Herdr, then run herdr status --json; change chat.backend if Herdr is not required\n"},
		{name: "status nonzero exit", exit: 2, failures: 1, want: "doctor: herdr backend UNREACHABLE — herdr status --json: exit 2: stopped\ndoctor: herdr backend repair — install or start Herdr, then run herdr status --json; change chat.backend if Herdr is not required\n"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runner := &deps.FakeRunner{}
			if scenario.lookup != nil {
				runner.ScriptLookPath("herdr", "", scenario.lookup)
			} else if scenario.run != nil || scenario.exit != 0 {
				runner.ScriptLookPath("herdr", "/opt/herdr", nil)
				runner.Script(
					[]string{"/opt/herdr", "status", "--json"},
					deps.RunResult{ExitCode: scenario.exit, Stderr: []byte("stopped")},
					scenario.run,
				)
			}
			var stdout bytes.Buffer
			warnings, failures := printHerdrBackendDoctor(
				context.Background(),
				&stdout,
				Dependencies{Runner: runner, Env: &paths.MapEnv{Values: map[string]string{}}},
				false,
			)
			if got := stdout.String(); warnings != 0 || failures != scenario.failures || got != scenario.want {
				t.Errorf("warnings=%d failures=%d output = %q, want %q", warnings, failures, got, scenario.want)
			}
		})
	}
}

func TestHerdrBackendDoctorFollowsConfiguredBackend(t *testing.T) {
	for _, scenario := range []struct {
		name, backend, marker, pane, tmux string
		source                            pfmconfig.Source
		code                              int
		want, resolution                  string
	}{
		{name: "default auto outside", backend: pfmconfig.DefaultChatBackend, source: pfmconfig.SourceDefault, want: "doctor: herdr backend NOT CHECKED — chat.backend=auto (default) resolves to tmux here: no Herdr server runs at {root}\n"},
		{name: "explicit auto outside", backend: "auto", source: pfmconfig.SourceFile, want: "doctor: herdr backend NOT CHECKED — chat.backend=auto (file) resolves to tmux here: no Herdr server runs at {root}\n"},
		{name: "default auto inside unreachable", backend: "auto", marker: "1", pane: "w1:p1", source: pfmconfig.SourceDefault, want: "doctor: herdr backend NOT CHECKED — chat.backend=auto (default) resolves to tmux here: no Herdr server runs at {root}\n"},
		{name: "explicit auto inside unreachable", backend: "auto", marker: "1", pane: "w1:p1", source: pfmconfig.SourceFile, want: "doctor: herdr backend NOT CHECKED — chat.backend=auto (file) resolves to tmux here: no Herdr server runs at {root}\n"},
		{name: "auto cannot read tmux", backend: "auto", marker: "1", pane: "w1:p1", tmux: "/tmp/tmux-1/dead,1,0", source: pfmconfig.SourceDefault, want: "doctor: herdr backend NOT CHECKED — chat.backend=auto (default) resolves to tmux here: no Herdr server runs at {root}\n"},
		{name: "auto missing pane", backend: "auto", marker: "1", source: pfmconfig.SourceDefault, want: "doctor: herdr backend NOT CHECKED — chat.backend=auto (default) resolves to tmux here: no Herdr server runs at {root}\n"},
		{name: "auto missing marker", backend: "auto", pane: "w1:p1", source: pfmconfig.SourceDefault, want: "doctor: herdr backend NOT CHECKED — chat.backend=auto (default) resolves to tmux here: no Herdr server runs at {root}\n"},
		{name: "explicit tmux inside", backend: "tmux", marker: "1", pane: "w1:p1", source: pfmconfig.SourceFile, want: "doctor: herdr backend NOT CHECKED — chat.backend=tmux (file) selects tmux\n"},
		{name: "configured unreachable", backend: "herdr", source: pfmconfig.SourceFile, code: 3, want: "doctor: herdr backend UNREACHABLE — herdr socket {root}: dial for ping pfm-seat: dial unix {root}: connect: no such file or directory\ndoctor: herdr backend repair — install or start Herdr, then run herdr status --json; change chat.backend if Herdr is not required\n"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			runtime := buildCleanDoctorHome(t)
			runtime.Config.Chat.Backend = scenario.backend
			runtime.Config.Sources["chat.backend"] = scenario.source
			// Every probe is jailed. The dead endpoint makes accidentally checking
			// an optional backend observably fail, never contact a live Herdr.
			socket := filepath.Join(testjail.ShortRoot(t), "absent.sock")
			t.Setenv(herdr.EnvSocket, socket)
			t.Setenv(herdr.EnvBin, "")
			t.Setenv(herdr.EnvMarker, scenario.marker)
			t.Setenv(herdr.EnvPane, scenario.pane)
			t.Setenv("TMUX", scenario.tmux)
			var stdout, stderr bytes.Buffer
			code := runDoctor(nil, &stdout, &stderr, runtime)
			if got := rooted(
				doctorRows(stdout.String(), "doctor: herdr backend"),
				socket,
			); code != scenario.code ||
				got != scenario.want {
				t.Errorf(
					"code=%d herdr backend rows = %q, want %q; stderr=%s",
					code,
					got,
					scenario.want,
					stderr.String(),
				)
			}
		})
	}
}
