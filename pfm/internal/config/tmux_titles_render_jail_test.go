//go:build jail

package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestTmuxTitlesStringRendersOneNamePerTab drives a REAL tmux server on a
// private socket — never the fleet's — and reads the ACTUAL expansion of
// TmuxTitlesString the way a terminal emulator would: through
// `display-message '#{T:set-titles-string}'`. The two sibling jail tests
// (internal/spawn, internal/action) prove pfm APPLIES the option; this one
// proves what tmux does with it once applied, across both a Claude (`cc-*`)
// session and every other engine's session, and across the two status
// glyphs Claude Code's own pane title carries while idle vs busy.
func TestTmuxTitlesStringRendersOneNamePerTab(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	root, err := os.MkdirTemp("/tmp", "pfmtitlerender")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	tmuxDir := filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	socketPath := filepath.Join(tmuxDir, "probe-title-render")
	env := append(os.Environ(), "TMUX=")

	run := func(arguments ...string) string {
		t.Helper()
		command := exec.Command("tmux", append([]string{"-S", socketPath}, arguments...)...)
		command.Env = env
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("tmux %s: %v: %s", strings.Join(arguments, " "), err, output)
		}
		return strings.TrimSpace(string(output))
	}

	const (
		ccSession = "cc-1-2-3"
		cxSession = "cx-4-5-6"
	)
	run("-f", "/dev/null", "new-session", "-d", "-s", ccSession, "-n", "WIN-SHORT", "sleep 60")
	t.Cleanup(func() {
		kill := exec.Command("tmux", "-S", socketPath, "kill-server")
		kill.Env = env
		_ = kill.Run()
	})
	run("new-session", "-d", "-s", cxSession, "-n", "RND:X", "sleep 60")
	run("set-option", "-g", "set-titles-string", TmuxTitlesString)

	const longName = "A Chat Name Longer Than Twenty Four Runes"
	cases := []struct {
		name      string
		session   string
		paneTitle string
		want      string
	}{
		// (a) a Claude pane's own status-glyph title, stripped to the bare
		// chat name and re-prefixed with pfm's own tab glyph.
		{"cc status glyph", ccSession, "✳ " + longName, "⬢ " + longName},
		// (b) the SAME name, but Claude Code is mid-spinner: the leading
		// glyph changed and nothing else did, so the rendered title must be
		// byte-identical to (a) — that stability is what lets tmux re-emit
		// the OSC title only on a real change.
		{"cc spinner glyph", ccSession, "⠐ " + longName, "⬢ " + longName},
		// (c) a non-Claude engine's pane title is its working directory, not
		// its name — TmuxTitlesString ignores it outright and renders the
		// window name instead.
		{"cx other engine", cxSession, "workdir", "⬢ RND:X"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			run("select-pane", "-t", testCase.session, "-T", testCase.paneTitle)
			got := run("display-message", "-p", "-t", testCase.session, "#{T:set-titles-string}")
			if got != testCase.want {
				t.Fatalf("rendered %q, want %q", got, testCase.want)
			}
		})
	}
}
