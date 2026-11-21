package installping

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestReasonNamesTheSwitchThatTurnsThePingOff(t *testing.T) {
	for _, test := range []struct {
		name, variable, value, version, want string
		configOff                            bool
		configError                          error
	}{
		{name: "nothing set", version: "v1.2.3"},
		{
			name: "config false", version: "v1.2.3", configOff: true,
			want: `"installPing": false in pfm.config.json`,
		},
		{
			name: "config false outranks DO_NOT_TRACK", variable: "DO_NOT_TRACK", value: "1", version: "v1.2.3",
			configOff: true, want: `"installPing": false in pfm.config.json`,
		},
		{
			name: "config unreadable", version: "v1.2.3", configError: errors.New("parse config /x at byte 3"),
			want: "pfm.config.json unreadable",
		},
		{
			name: "config unreadable outranks the fence", variable: paths.EnvDevFence, value: "1", version: "v1.2.3",
			configError: errors.New("parse config /x at byte 3"), want: "pfm.config.json unreadable",
		},
		{
			name: "config false and unreadable", version: "v1.2.3", configOff: true,
			configError: errors.New("parse config /x at byte 3"), want: `"installPing": false in pfm.config.json`,
		},
		{name: "a stamped alpha", version: "1.2.3-alpha"},
		{name: "opt-out on", variable: paths.EnvInstallPingOffline, value: "1", version: "v1.2.3", want: "PFM_INSTALL_PING_OFFLINE"},
		{name: "opt-out 0", variable: paths.EnvInstallPingOffline, value: "0", version: "v1.2.3"},
		{name: "opt-out true", variable: paths.EnvInstallPingOffline, value: "true", version: "v1.2.3"},
		{name: "DO_NOT_TRACK 1", variable: "DO_NOT_TRACK", value: "1", version: "v1.2.3", want: "DO_NOT_TRACK"},
		{name: "DO_NOT_TRACK yes", variable: "DO_NOT_TRACK", value: "yes", version: "v1.2.3", want: "DO_NOT_TRACK"},
		{name: "DO_NOT_TRACK 0", variable: "DO_NOT_TRACK", value: "0", version: "v1.2.3"},
		{name: "DO_NOT_TRACK false", variable: "DO_NOT_TRACK", value: "false", version: "v1.2.3"},
		{name: "DO_NOT_TRACK FALSE", variable: "DO_NOT_TRACK", value: "FALSE", version: "v1.2.3"},
		{name: "CI true", variable: "CI", value: "true", version: "v1.2.3", want: "CI"},
		{name: "CI 1", variable: "CI", value: "1", version: "v1.2.3", want: "CI"},
		{name: "CI false", variable: "CI", value: "false", version: "v1.2.3"},
		{name: "CI 0", variable: "CI", value: "0", version: "v1.2.3"},
		{name: "GitHub Actions", variable: "GITHUB_ACTIONS", value: "true", version: "v1.2.3", want: "GITHUB_ACTIONS"},
		{name: "GitHub Actions false", variable: "GITHUB_ACTIONS", value: "false", version: "v1.2.3"},
		{name: "the fence", variable: paths.EnvDevFence, value: "1", version: "v1.2.3", want: "PFM_DEV_FENCE"},
		{name: "the fence 0", variable: paths.EnvDevFence, value: "0", version: "v1.2.3"},
		{name: "an unstamped build", version: "dev", want: "unstamped development build"},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := &paths.MapEnv{Values: map[string]string{}}
			if test.variable != "" {
				env.Values[test.variable] = test.value
			}
			options := Options{
				Env:         env,
				Version:     test.version,
				ConfigOff:   test.configOff,
				ConfigError: test.configError,
			}
			if got := Reason(options); got != test.want {
				t.Fatalf("Reason(%s=%q, %q, off=%v, err=%v) = %q, want %q",
					test.variable, test.value, test.version, test.configOff, test.configError, got, test.want)
			}
		})
	}
}

func TestNoticeSaysOnWithTheConfigKeyOrWhatTurnedItOff(t *testing.T) {
	for reason, want := range map[string]string{
		"": `pfm: install ping on — an anonymous count of Professor installs (random install id, pfm version, OS, ` +
			`architecture, enabled Professor plugins; no IP, hostname, username or paths), sent at install and once a ` +
			`day; set "installPing": false in pfm.config.json to turn it off`,
		"DO_NOT_TRACK": "pfm: install ping off (DO_NOT_TRACK)",
		`"installPing": false in pfm.config.json`: `pfm: install ping off ("installPing": false in pfm.config.json)`,
		"pfm.config.json unreadable":              "pfm: install ping off (pfm.config.json unreadable)",
	} {
		if got := Notice(reason); got != want {
			t.Errorf("Notice(%q) = %q, want %q", reason, got, want)
		}
	}
	const wantSkipped = `pfm: install ping not sent (--offline); the daily ping stays on — ` +
		`set "installPing": false in pfm.config.json to turn it off`
	if got := NoticeSkipped("--offline"); got != wantSkipped {
		t.Errorf("NoticeSkipped(--offline) = %q, want %q", got, wantSkipped)
	}
}

func TestForRuntimeCarriesTheConfigSwitchAndLoadError(t *testing.T) {
	loadErr := errors.New("parse config /x at byte 3")
	for _, test := range []struct {
		name          string
		runtime       pfmconfig.Runtime
		wantOff       bool
		wantConfigErr error
	}{
		{name: "installPing true", runtime: pfmconfig.Runtime{Config: pfmconfig.Config{InstallPing: true}}},
		{name: "installPing false", runtime: pfmconfig.Runtime{}, wantOff: true},
		{
			name:    "load error",
			runtime: pfmconfig.Runtime{Config: pfmconfig.Config{InstallPing: true}, ConfigError: loadErr}, wantConfigErr: loadErr,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := ForRuntime(test.runtime, &paths.MapEnv{Values: map[string]string{}})
			if got.ConfigOff != test.wantOff || !errors.Is(got.ConfigError, test.wantConfigErr) {
				t.Fatalf("ForRuntime ConfigOff=%v ConfigError=%v, want %v %v",
					got.ConfigOff, got.ConfigError, test.wantOff, test.wantConfigErr)
			}
		})
	}
}

// counter is an httptest stand-in for the Worker, recording each body.
type counter struct {
	server *httptest.Server
	bodies []string
	types  []string
	status int
	reason string
}

func newCounter(t *testing.T, status int, reason string) *counter {
	c := &counter{status: status, reason: reason}
	c.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		c.bodies = append(c.bodies, string(body))
		c.types = append(c.types, request.Method+" "+request.URL.Path+" "+request.Header.Get("Content-Type"))
		writer.WriteHeader(c.status)
		_, _ = io.WriteString(writer, c.reason)
	}))
	t.Cleanup(c.server.Close)
	return c
}

// testOptions is a ping that would send: a stamped version, nothing set in
// env, a fresh state dir, a Claude config dir enabling three plugins of which
// the Professor listing holds two.
func testOptions(t *testing.T, endpoint string, clk clock.Clock) Options {
	root := t.TempDir()
	config, market := filepath.Join(root, "claude"), filepath.Join(root, "market")
	for path, content := range map[string]string{
		filepath.Join(config, "settings.json"): `{"enabledPlugins":{"flights@professor":true,"harvester@professor":true,` +
			`"secret@professor":true,"flights@other":true,"buddy@professor":false}}`,
		filepath.Join(config, "plugins", "known_marketplaces.json"): `{"professor":{"source":{"source":"directory",` +
			`"path":"` + market + `"},"installLocation":"` + market + `"}}`,
		filepath.Join(market, ".claude-plugin", "marketplace.json"): `{"name":"professor","plugins":[` +
			`{"name":"flights"},{"name":"harvester"},{"name":"buddy"}]}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Options{
		Env: &paths.MapEnv{Values: map[string]string{}}, StateDir: filepath.Join(root, "state"),
		ClaudeConfigDir: config, Version: "0.42.0-alpha", OS: "linux", Arch: "arm64", Endpoint: endpoint,
		Client: obs.WrapClient(&http.Client{Timeout: Timeout}), Clock: clk,
	}
}

func TestDailySendsOncePerUTCDay(t *testing.T) {
	c := newCounter(t, http.StatusNoContent, "")
	clk := clock.NewFake(time.Date(2026, 11, 17, 23, 59, 58, 0, time.UTC))
	options := testOptions(t, c.server.URL, clk)
	for _, step := range []struct {
		advance   time.Duration
		wantPosts int
	}{
		{0, 1},           // 23:59:58: the day's first ping
		{time.Second, 1}, // 23:59:59: the same UTC day, nothing
		{time.Second, 2}, // 00:00:00 the next day: sent again
		{time.Second, 2}, // 00:00:01: the same day, nothing
	} {
		clk.Advance(step.advance)
		if err := SendDaily(context.Background(), options); err != nil || len(c.bodies) != step.wantPosts {
			t.Fatalf(
				"Daily at %s = %v after %d posts, want nil after %d",
				clk.Now(),
				err,
				len(c.bodies),
				step.wantPosts,
			)
		}
	}
	want := postedBody(t, options, EventDaily)
	if c.bodies[0] != want || c.bodies[1] != want {
		t.Fatalf("daily bodies = %q; want %s twice", c.bodies, want)
	}
	entries, err := os.ReadDir(options.StateDir)
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if got := strings.Join(names, " "); err != nil || got != "install-ping-day-2026-11-18 install-ping-id" {
		t.Fatalf("state dir %s = %q, %v; want the new day's claim alone beside the id", options.StateDir, got, err)
	}
}

func TestPingOffSendsAndWritesNothing(t *testing.T) {
	c := newCounter(t, http.StatusNoContent, "")
	options := testOptions(t, c.server.URL, clock.NewFake(time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)))
	for name, off := range map[string]func(*Options){
		"DO_NOT_TRACK=1":       func(o *Options) { o.Env = &paths.MapEnv{Values: map[string]string{"DO_NOT_TRACK": "1"}} },
		`"installPing": false`: func(o *Options) { o.ConfigOff = true },
		"an unreadable config": func(o *Options) { o.ConfigError = errors.New("parse config /x at byte 3") },
	} {
		off(&options)
		if err := SendInstall(context.Background(), options); err != nil {
			t.Fatal(err)
		}
		if err := SendDaily(context.Background(), options); err != nil {
			t.Fatal(err)
		}
		entries, _ := os.ReadDir(options.StateDir)
		if len(c.bodies) != 0 || len(entries) != 0 {
			t.Fatalf("with %s: %d posts, state %v; want none", name, len(c.bodies), entries)
		}
		options = testOptions(t, c.server.URL, clock.NewFake(time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)))
	}
	options.Env = &paths.MapEnv{Values: map[string]string{}}
	if err := SendDaily(context.Background(), options); err != nil || len(c.bodies) != 1 {
		t.Fatalf("Daily with nothing set = %v after %d posts, want one", err, len(c.bodies))
	}
}

func TestFailedPingLogsWarnAndReturnsTheCause(t *testing.T) {
	refused := newCounter(t, http.StatusBadRequest, "os must be darwin, linux, windows or freebsd\n")
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	for _, test := range []struct {
		name, endpoint, wantErr string
	}{
		{
			name: "refused", endpoint: refused.server.URL,
			wantErr: "counter answered 400 Bad Request: os must be darwin, linux, windows or freebsd",
		},
		{
			name: "unreachable", endpoint: closed.URL, wantErr: "post: Post \"" + closed.URL + "\": dial tcp",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, recorder := obs.Test(t)
			options := testOptions(t, test.endpoint, clock.NewFake(time.Date(2026, 11, 17, 9, 0, 0, 0, time.UTC)))
			err := SendDaily(context.Background(), options)
			if err == nil || !strings.HasPrefix(err.Error(), test.wantErr) {
				t.Fatalf("Daily error = %v, want prefix %q", err, test.wantErr)
			}
			var warned *obs.Record
			for _, record := range recorder.Records() {
				if record.Message == "install ping: not sent" {
					warned = &record
				}
			}
			if warned == nil || warned.Level != "WARN" {
				t.Fatalf("no WARN install ping record: %s", recorder.Raw())
			}
			// The round trip's own record: a best-effort ping that fails is a WARN, never an ERROR.
			var requested *obs.Record
			for _, record := range recorder.Records() {
				if record.Message == "http.out.request" {
					requested = &record
				}
			}
			if requested == nil || requested.Level != "WARN" {
				t.Fatalf("http.out.request record = %+v, want level WARN: %s", requested, recorder.Raw())
			}
			for key, want := range map[string]any{
				"kind": "daily", "target": test.endpoint, "path": options.StateDir, obs.FieldErr: err.Error(),
			} {
				if got, _ := warned.Field(key); got != want {
					t.Fatalf("WARN record %s = %v, want %v: %v", key, got, want, warned.Fields)
				}
			}
			// The day was claimed: a failed ping is not retried the same day.
			refusedBefore := len(refused.bodies)
			if again := SendDaily(context.Background(), options); again != nil || len(refused.bodies) != refusedBefore {
				t.Fatalf("second Daily the same day = %v, %d refused posts; want nil and no retry (%d)",
					again, len(refused.bodies), refusedBefore)
			}
		})
	}
}

// postedBody is the exact JSON the counter receives for event from options:
// the install id SendInstall or SendDaily stored, the fixture's version,
// platform and Professor plugins.
func postedBody(t *testing.T, options Options, event string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(options.StateDir, "install-ping-id"))
	if err != nil {
		t.Fatalf("read the install id: %v", err)
	}
	return `{"id":"` + strings.TrimSpace(string(raw)) + `","product":"pfm","version":"0.42.0-alpha","os":"linux",` +
		`"arch":"arm64","plugins":["flights","harvester"],"event":"` + event + `"}`
}
