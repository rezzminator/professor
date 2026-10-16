package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/herdr"
)

func loadTmuxConfig(t *testing.T, body string) (Config, error) {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"version": 2` + body + `}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path, home, nil)
}

// A host that owns its own outer-pty title must not have it seized by an
// upgrade, and a host that never heard of the key must not lose the title it
// already has. Both are the same assertion: the default is today's behaviour.
func TestTmuxTitlesDefaultsToPfmOwningTheTitle(t *testing.T) {
	got, err := loadTmuxConfig(t, "")
	if err != nil {
		t.Fatal(err)
	}
	if !got.Tmux.Titles.Enabled {
		t.Fatalf("Tmux.Titles = %+v, want enabled by default", got.Tmux.Titles)
	}
	if source := got.Source("tmux.titles.enabled"); source != SourceDefault {
		t.Fatalf("unset tmux.titles.enabled reports source %q, want default", source)
	}
	options := got.Tmux.Titles.Options()
	want := [][]string{
		{"set-option", "-g", "set-titles", "on"},
		{"set-option", "-g", "set-titles-string", TmuxTitlesString},
	}
	if len(options) != len(want) {
		t.Fatalf("Options() = %v, want %v", options, want)
	}
	for index := range want {
		if strings.Join(options[index], " ") != strings.Join(want[index], " ") {
			t.Fatalf("Options()[%d] = %v, want %v", index, options[index], want[index])
		}
	}
}

func TestTmuxTitlesDisabledSetsNeitherOption(t *testing.T) {
	got, err := loadTmuxConfig(t, `, "tmux": {"titles": {"enabled": false}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tmux.Titles.Enabled {
		t.Fatalf("Tmux.Titles = %+v, want disabled", got.Tmux.Titles)
	}
	if source := got.Source("tmux.titles.enabled"); source != SourceFile {
		t.Fatalf("tmux.titles.enabled reports source %q, want file", source)
	}
	if options := got.Tmux.Titles.Options(); len(options) != 0 {
		t.Fatalf("a disabled policy planned %v, want no tmux options at all", options)
	}
}

// A tmux client built without a machine config — a test fixture, an internal
// caller — keeps pfm's default rather than silently handing the terminal title
// to the host.
func TestTmuxTitlesOrDefaultTreatsNilAsPfmOwned(t *testing.T) {
	if !TmuxTitlesOrDefault(nil).Enabled {
		t.Fatal("a nil tmux.titles policy resolved to host-owned, want pfm-owned")
	}
	disabled := TmuxTitles{Enabled: false}
	if TmuxTitlesOrDefault(&disabled).Enabled {
		t.Fatal("an explicit disabled policy resolved to pfm-owned")
	}
}

func TestTmuxTitlesRoundTripsThroughMarshal(t *testing.T) {
	got, err := loadTmuxConfig(t, `, "tmux": {"titles": {"enabled": false}}`)
	if err != nil {
		t.Fatal(err)
	}
	content, err := Marshal(got, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), `"titles"`) || !strings.Contains(string(content), `"enabled": false`) {
		t.Fatalf("Marshal dropped tmux.titles.enabled:\n%s", content)
	}
}

func TestTmuxRejectsUnknownKeys(t *testing.T) {
	if _, err := loadTmuxConfig(t, `, "tmux": {"title": {"enabled": false}}`); err == nil {
		t.Fatal("a misspelled tmux key was accepted")
	}
}

// ChatServerOptions is the ONE list a chat server carries: every creation
// door applies it at birth and name-sync converges live servers onto it. The
// title half follows the policy; automatic-rename off is never gated — the
// window name is the fleet's DNS record and pfm is its only writer. The Herdr
// half is never gated: the session environment must follow every attach so the
// reporter finds the pane of the current attach, and the client-attached hook
// (`attach`) re-reports a chat attached late, the client-resized hook (`sync`)
// re-binds a chat whose Herdr pane moved, and the window-renamed hook hands every
// rename to the reporter, which re-sends the chat's name.
func TestChatServerOptionsAreTheTitlePolicyPlusAFrozenWindowName(t *testing.T) {
	frozen := "set-window-option -g automatic-rename off"
	herdrOptions := []string{
		"set-option -g update-environment[20] HERDR_ENV",
		"set-option -g update-environment[21] HERDR_PANE_ID",
		"set-option -g update-environment[22] HERDR_SOCKET_PATH",
		"set-option -g update-environment[23] HERDR_BIN_PATH",
		`set-hook -g client-attached[50] run-shell -b "'/pfm/bin/pfm' internal herdr-state attach --socket #{q:socket_path} --session #{q:session_name} --client #{q:client_pid}"`,
		`set-hook -g client-detached[50] run-shell -b "'/pfm/bin/pfm' internal herdr-state detach --socket #{q:socket_path} --session #{q:session_name} --client #{q:client_pid}"`,
		`set-hook -g client-resized[50] run-shell -b "'/pfm/bin/pfm' internal herdr-state sync --socket #{q:socket_path} --session #{q:session_name} --client #{q:client_pid}"`,
		`set-hook -g window-renamed[50] run-shell -b "'/pfm/bin/pfm' internal herdr-state name --socket #{q:socket_path} --session #{q:session_name}"`,
	}
	disabled := TmuxTitles{Enabled: false}
	for _, test := range []struct {
		name   string
		titles *TmuxTitles
		want   []string
	}{
		{"nil policy is pfm-owned", nil, []string{
			"set-option -g set-titles on",
			"set-option -g set-titles-string " + TmuxTitlesString,
			frozen,
		}},
		{"host-owned policy keeps only the frozen name", &disabled, []string{frozen}},
	} {
		options := ChatServerOptions(test.titles, "/pfm/bin/pfm")
		got := make([]string, 0, len(options))
		for _, option := range options {
			got = append(got, strings.Join(option, " "))
		}
		test.want = append(test.want, herdrOptions...)
		if strings.Join(got, "\n") != strings.Join(test.want, "\n") {
			t.Fatalf("%s: ChatServerOptions = %q, want %q", test.name, got, test.want)
		}
	}
}

// Every interpolated value of a client hook reaches sh quoted: a binary path
// with a quote, dollar, backtick, space, double quote or hash stays one word.
func TestHerdrHookCommandQuotesTheBinaryForShellAndTmux(t *testing.T) {
	got := herdrHookCommand("/a b/p'q$x\"y#z`w/pfm", herdr.EventSync)
	want := "run-shell -b \"'/a b/p'\\\\''q\\$x\\\"y##z`w/pfm' internal herdr-state sync --socket #{q:socket_path} --session #{q:session_name} --client #{q:client_pid}\""
	if got != want {
		t.Fatalf("herdrHookCommand =\n%s\nwant\n%s", got, want)
	}
	// The name event runs for a window, not a client: no --client word.
	gotName := herdrHookCommand("/a b/p'q$x\"y#z`w/pfm", herdr.EventName)
	wantName := "run-shell -b \"'/a b/p'\\\\''q\\$x\\\"y##z`w/pfm' internal herdr-state name --socket #{q:socket_path} --session #{q:session_name}\""
	if gotName != wantName {
		t.Fatalf("herdrHookCommand(name) =\n%s\nwant\n%s", gotName, wantName)
	}
}
