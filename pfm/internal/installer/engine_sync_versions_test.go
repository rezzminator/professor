package installer

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestReportEngineSyncCompatibilityAndVersionSeats(t *testing.T) {
	for _, name := range []string{
		"older required",
		"two versions",
		"two versions pinned",
		"two versions pinned through a linked parent",
		"two versions partial",
		"two versions disabled",
		"two versions unreadable seat",
		"two versions fifo seat",
		"backwards stable",
		"leading-zero cache",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", "")
			t.Setenv("XDG_DATA_HOME", "")
			t.Setenv("ENGINE_SYNC_HOME", "")
			esHome := filepath.Join(home, ".local", "share", "engine-sync")
			stableVersion := "0.4.0-alpha"
			if name == "older required" {
				stableVersion = "0.1.0"
			}
			binary := filepath.Join(esHome, "bin", "engine-sync")
			script := "#!/bin/sh\nif [ \"$1\" = version ]; then echo 'engine-sync " + stableVersion + "'; else echo '{\"ok\":true,\"rows\":[]}'; fi\n"
			// The plugin wrapper links the stable path at a cached version; the
			// pinned case keeps that shape so its resolved target counts too.
			stableTarget := binary
			if name == "two versions pinned" {
				stableTarget = filepath.Join(esHome, "versions", stableVersion, "engine-sync")
			}
			for _, dir := range []string{filepath.Dir(binary), filepath.Dir(stableTarget)} {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if err := testjail.WriteExecutable(stableTarget, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			if stableTarget != binary {
				relative := filepath.Join("..", "versions", stableVersion, "engine-sync")
				if err := os.Symlink(relative, binary); err != nil {
					t.Fatal(err)
				}
			}
			store, account := filepath.Join(home, ".claude"), filepath.Join(home, "account")
			enable := func(dir, id, pin string) {
				t.Helper()
				env := ""
				if pin != "" {
					env = `,"env":{"ENGINE_SYNC_BIN":"` + pin + `"}`
				}
				writeFixture(t, filepath.Join(dir, "settings.json"), `{"enabledPlugins":{"`+id+`":true}`+env+`}`)
			}
			switch name {
			case "two versions":
				enable(store, "engine-sync@professor", "")
			case "two versions pinned":
				enable(store, "engine-sync@professor", filepath.Join(esHome, "bin", "..", "bin", "engine-sync"))
				enable(account, "engine-sync@fixture-market", stableTarget)
			case "two versions pinned through a linked parent":
				// The same stable link, spelled through a linked data dir (a home or
				// share reached by a symlink): the wrapper runs the stable binary.
				linked := filepath.Join(home, "share-link")
				if err := os.Symlink(esHome, linked); err != nil {
					t.Fatal(err)
				}
				enable(store, "engine-sync@professor", filepath.Join(linked, "bin", "engine-sync"))
			case "two versions partial":
				enable(store, "engine-sync@professor", binary)
				enable(account, "engine-sync@professor", "/elsewhere/engine-sync")
			case "two versions disabled":
				writeFixture(
					t,
					filepath.Join(store, "settings.json"),
					`{"enabledPlugins":{"engine-sync@professor":false,"other@fixture-market":true}}`,
				)
			case "two versions unreadable seat":
				writeFixture(t, filepath.Join(store, "settings.json"), "{")
				enable(account, "engine-sync@professor", "")
			case "two versions fifo seat":
				if err := os.MkdirAll(store, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(filepath.Join(store, "settings.json"), 0o600); err != nil {
					t.Fatal(err)
				}
				enable(account, "engine-sync@professor", "")
			}
			t.Setenv("ENGINE_SYNC_BIN", binary)
			clone := filepath.Join(home, ".professor")
			writeFixture(
				t,
				filepath.Join(
					clone,
					"plugins",
					"engine-sync",
					"plugins",
					"engine-sync",
					".claude-plugin",
					"plugin.json",
				),
				`{"version":"0.1.0"}`,
			)
			if name != "older required" {
				version := "0.1.0"
				switch name {
				case "backwards stable":
					version = "0.4.0"
				case "leading-zero cache":
					version = "0.3.0-01"
				}
				cached := filepath.Join(esHome, "versions", version, "engine-sync")
				if err := os.MkdirAll(filepath.Dir(cached), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := testjail.WriteExecutable(
					cached,
					[]byte("#!/bin/sh\necho 'engine-sync "+version+"'\n"),
					0o755,
				); err != nil {
					t.Fatal(err)
				}
			}
			var output bytes.Buffer
			warnings, failures := ReportGlobalAgents(&output, home, false, paths.OSEnv{}, nil, []string{account}, nil)
			text := output.String()
			switch name {
			case "older required":
				if failures == 0 || !strings.Contains(text, "engine-sync 0.1.0 is older than required 0.4.0-alpha") {
					t.Fatalf("doctor %d/%d: %s", warnings, failures, text)
				}
			case "two versions", "two versions partial", "two versions unreadable seat", "two versions fifo seat":
				seat := filepath.Join(store, "settings.json") + " (ENGINE_SYNC_BIN unset)"
				seatFailure, storeSettings := "", filepath.Join(store, "settings.json")
				switch name {
				case "two versions partial":
					seat = filepath.Join(account, "settings.json") + " (ENGINE_SYNC_BIN=/elsewhere/engine-sync)"
				case "two versions unreadable seat":
					seat = filepath.Join(account, "settings.json") + " (ENGINE_SYNC_BIN unset)"
					seatFailure = "doctor: engine-sync state=FAIL seat settings: parse " + storeSettings
				case "two versions fifo seat":
					seat = filepath.Join(account, "settings.json") + " (ENGINE_SYNC_BIN unset)"
					seatFailure = "doctor: engine-sync state=FAIL seat settings " + storeSettings + ": not a regular file"
				}
				if seatFailure == "" && failures != 0 ||
					seatFailure != "" && (failures == 0 || !strings.Contains(text, seatFailure)) {
					t.Fatalf("doctor %d/%d, want seat failure %q: %s", warnings, failures, seatFailure, text)
				}
				want := "two versions can write the same roots: stable 0.4.0-alpha at " + binary +
					"; plugin cache 0.1.0 at " + filepath.Join(esHome, "versions", "0.1.0", "engine-sync") +
					"; seats running the plugin without ENGINE_SYNC_BIN on the stable binary: " + seat +
					" — set ENGINE_SYNC_BIN to the stable binary for the plugin seat or disable that seat on this development host\n"
				if warnings == 0 || !strings.Contains(text, want) {
					t.Fatalf("doctor %d/%d, want %q: %s", warnings, failures, want, text)
				}
			case "two versions pinned", "two versions pinned through a linked parent", "two versions disabled":
				if warnings != 0 || failures != 0 {
					t.Fatalf("doctor %d/%d, want 0/0: %s", warnings, failures, text)
				}
			case "leading-zero cache":
				want := "cache " + filepath.Join(esHome, "versions", "0.3.0-01") + " not probed: " +
					`invalid engine-sync version "0.3.0-01"`
				if warnings == 0 || !strings.Contains(text, want) {
					t.Fatalf("doctor %d/%d, want %q: %s", warnings, failures, want, text)
				}
			case "backwards stable":
				if warnings == 0 || !strings.Contains(text, "stable link moved backwards") ||
					!strings.Contains(text, "0.4.0") ||
					!strings.Contains(
						text,
						"start a new Claude session so the engine-sync plugin wrapper moves the stable link forward",
					) {
					t.Fatalf("doctor %d/%d: %s", warnings, failures, text)
				}
			}
		})
	}
}
