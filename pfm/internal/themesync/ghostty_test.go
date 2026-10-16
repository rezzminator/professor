package themesync

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

func TestGhosttyRenderFilesPerMode(t *testing.T) {
	palette := tokyoNight(t)
	dir := "/cfg"
	auto := ghosttyRender(palette, ModeAuto, 5, dir)
	include := auto["/cfg/pfm-theme.ghostty"]
	if !strings.HasPrefix(include, "# pfm theme · tokyo-night · gen 5 · mode auto\n") ||
		!strings.Contains(
			include,
			"theme = light:/cfg/pfm-theme/tokyo-night-light,dark:/cfg/pfm-theme/tokyo-night-dark\n",
		) {
		t.Fatalf("include file:\n%s", include)
	}
	dark := auto["/cfg/pfm-theme/tokyo-night-dark"]
	for _, want := range []string{
		"background = #1a1b26\n", "foreground = #c0caf5\n", "cursor-color = #c0caf5\n",
		"selection-background = #283457\n", "selection-foreground = #c0caf5\n",
		"palette = 0=#15161e\n", "palette = 15=#c0caf5\n",
	} {
		if !strings.Contains(dark, want) {
			t.Errorf("dark theme file lacks %q:\n%s", want, dark)
		}
	}
	if strings.Count(dark, "palette = ") != 16 || len(auto) != 3 {
		t.Errorf(
			"want 16 palette lines and 3 files, got %d lines %d files",
			strings.Count(dark, "palette = "),
			len(auto),
		)
	}
	if !strings.Contains(auto["/cfg/pfm-theme/tokyo-night-light"], "background = #e1e2e7\n") {
		t.Errorf("light theme file wrong:\n%s", auto["/cfg/pfm-theme/tokyo-night-light"])
	}
	forced := ghosttyRender(palette, ModeLight, 5, dir)["/cfg/pfm-theme.ghostty"]
	if !strings.Contains(forced, "theme = /cfg/pfm-theme/tokyo-night-light\n") ||
		!strings.Contains(forced, "mode light") {
		t.Errorf("forced-light include:\n%s", forced)
	}
	if got := ghosttyRender(palette, ModeDark, 5, dir)["/cfg/pfm-theme.ghostty"]; !strings.Contains(
		got,
		"theme = /cfg/pfm-theme/tokyo-night-dark\n",
	) {
		t.Errorf("forced-dark include:\n%s", got)
	}
}

func TestGhosttyIncludeRoundTripWithAndWithoutTrailingNewline(t *testing.T) {
	for _, original := range []string{"", "font-size = 13\n", "font-size = 13", "a = 1\n\n\n", "# only a comment"} {
		updated, added := ghosttyInclude(original, "/cfg")
		if !strings.HasSuffix(updated, "config-file = ?/cfg/pfm-theme.ghostty\n") ||
			strings.Count(updated, "config-file = ?") != 1 {
			t.Fatalf("%q: updated = %q", original, updated)
		}
		if !strings.HasPrefix(updated, original) {
			t.Fatalf("%q: the user's bytes must stay a prefix, got %q", original, updated)
		}
		if again, addedAgain := ghosttyInclude(updated, "/cfg"); again != updated || addedAgain != "" {
			t.Fatalf("%q: a second include must change nothing, got %q / %q", original, again, addedAgain)
		}
		if got := ghosttyExclude(updated, added, "/cfg"); got != original {
			t.Fatalf("%q: exclude = %q", original, got)
		}
	}
}

func TestGhosttyExcludeWhenTheUserMovedTheLines(t *testing.T) {
	_, added := ghosttyInclude("a = 1\n", "/cfg")
	moved := "# pfm theme (managed: pfm theme off removes these two lines)\nconfig-file = ?/cfg/pfm-theme.ghostty\na = 1\nb = 2\n"
	if got := ghosttyExclude(moved, added, "/cfg"); got != "a = 1\nb = 2\n" {
		t.Fatalf("exclude of a moved pair = %q", got)
	}
}

func TestGhosttyProcessesAreExactlyTheBareAppBinary(t *testing.T) {
	ps := strings.Join([]string{
		"  100 /Applications/Ghostty.app/Contents/MacOS/ghostty",
		"  101 /Applications/Ghostty.app/Contents/MacOS/ghostty --config-default-files=false --config-file=/x",
		"  102 /bin/zsh -l",
		"  103 /opt/apps/Ghostty.app/Contents/MacOS/ghostty",
		"  104 /usr/bin/grep /Applications/Ghostty.app/Contents/MacOS/ghostty",
		"bad line",
		"",
	}, "\n")
	got := ghosttyProcesses(ps)
	if len(got) != 2 || got[0].PID != 100 || got[1].PID != 103 ||
		got[1].Binary != "/opt/apps/Ghostty.app/Contents/MacOS/ghostty" {
		t.Fatalf("ghosttyProcesses = %+v", got)
	}
}

func TestGhosttyConfigCandidatesOrder(t *testing.T) {
	got := ghosttyCandidates("/h")
	want := []string{
		"/h/Library/Application Support/com.mitchellh.ghostty/config.ghostty",
		"/h/Library/Application Support/com.mitchellh.ghostty/config",
		"/h/.config/ghostty/config.ghostty",
		"/h/.config/ghostty/config",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
}

// A process whose arguments merely end in the app path (a debugger or a
// wrapper started with a relative Ghostty path) is not Ghostty: it is neither
// validated with nor sent SIGUSR2, whose default action would end it.
func TestGhosttyReloadSignalsOnlyProcessesExecutingTheApp(t *testing.T) {
	h := newHarness(t)
	impostor := "/usr/bin/lldb ./Ghostty.app/Contents/MacOS/ghostty"
	h.psGhostty = "  601 " + impostor + "\n" + h.psGhostty
	h.rearm()
	h.runner.Script([]string{"ps", "-o", "args=", "-p", "601"}, deps.RunResult{Stdout: []byte(impostor + "\n")}, nil)
	h.runner.Script([]string{"ps", "-o", "comm=", "-p", "601"}, deps.RunResult{Stdout: []byte("/usr/bin/lldb\n")}, nil)
	h.runner.Script([]string{"kill", "-USR2", "601"}, deps.RunResult{}, nil)
	h.runner.Script([]string{impostor, "+validate-config"}, deps.RunResult{}, nil)

	report := h.set("tokyo-night", ModeAuto)

	wantVerdict(t, report, "ghostty", VerdictInSync, "")
	var signalled []string
	for _, call := range h.runner.Calls() {
		if call.Argv[0] == "kill" || strings.HasSuffix(call.Argv[0], "lldb ./Ghostty.app/Contents/MacOS/ghostty") {
			signalled = append(signalled, strings.Join(call.Argv, " "))
		}
	}
	if strings.Join(signalled, "|") != "kill -USR2 501" {
		t.Fatalf("want only the app itself signalled, got %q", signalled)
	}
}
