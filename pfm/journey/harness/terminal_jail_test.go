//go:build jail

package harness

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// bashHooks opens every terminal as a bare interactive bash prompting "READY$ ".
// spentDeadline is a runner deadline already past: a wait under it gives up at its first look, so a
// test reaches a wait's miss without waiting (testwait.Expired's idea for a step).
func spentDeadline() (time.Time, bool) { return time.Unix(0, 0), true }

func bashHooks(t *testing.T) Hooks {
	return Hooks{
		Shell: []string{"bash", "--norc", "--noprofile", "-i"},
		Env: []string{
			"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH"), "PS1=READY$ ",
			// macOS's bash 3.2 otherwise prints its zsh notice above the first prompt.
			"BASH_SILENCE_DEPRECATION_WARNING=1",
		},
	}
}

func TestTerminalShowsWhatTheOwnerSees(t *testing.T) {
	for _, row := range []struct {
		name  string
		drive func(term *Terminal)
		want  string
	}{
		{"typed text is echoed and run", func(term *Terminal) {
			term.Type("echo $((6*7))\r")
		}, "READY$ echo $((6*7))\n42\nREADY$"},
		{"keys edit the line", func(term *Terminal) {
			term.Type("echo hxllo")
			term.Press(Left, Left, Left, Backspace)
			term.Type("e")
			term.Press(Enter)
		}, "READY$ echo hello\nhello\nREADY$"},
		{"a clear and a cursor move", func(term *Terminal) {
			term.Type(`printf '\033[2J\033[5;10HMARK\n'` + "\r")
		}, "\n\n\n\n         MARK\nREADY$"},
		{"the child sees a resize", func(term *Terminal) {
			term.Resize(100, 30)
			term.Type("stty size\r")
		}, "READY$ stty size\n30 100\nREADY$"},
	} {
		t.Run(row.name, func(t *testing.T) {
			Run(t, Do("root", func(c *Ctx, in int) int {
				term := c.Terminal("sh")
				term.WaitFor("READY$")
				row.drive(term)
				term.WaitScreen(
					"the screen "+row.name,
					func(screen string) bool { return screen == row.want },
				)
				return in
			}), 0, bashHooks(t))
		})
	}
}

func TestTypeDrawnSendsEachPieceOnceTheLastIsDrawn(t *testing.T) {
	dir := t.TempDir()
	file := dir + "/typed"
	// 255 bytes, then a two-byte rune across byte 256, so the first piece ends before it.
	text := "echo " + strings.Repeat("a", 250) + strings.Repeat("é", 200) + "\r"
	var asked []int
	Run(t, Do("root", func(c *Ctx, in int) int {
		term := c.Terminal("sh")
		term.WaitFor("READY$")
		// The quotes keep the echoed command line from reading as the markers its run prints.
		term.Type(fmt.Sprintf(
			"stty raw -echo; echo dd-read''y; dd bs=1 count=%d of=%s 2>/dev/null; stty sane; echo dd-don''e\r",
			len(text),
			file,
		))
		term.WaitFor("dd-ready")
		term.TypeDrawn(text, func(typed, _ string) bool {
			if len(asked) == 0 || asked[len(asked)-1] != len(typed) {
				asked = append(asked, len(typed))
			}
			info, err := os.Stat(file)
			return err == nil && info.Size() == int64(len(typed))
		})
		term.WaitFor("dd-done")
		return in
	}), 0, bashHooks(t))
	got, err := os.ReadFile(file)
	if err != nil || string(got) != text || fmt.Sprint(asked) != "[255 511]" {
		t.Fatalf("%s holds %q (err %v) after waits on %v typed bytes; want %q after waits on [255 511]",
			file, got, err, asked, text)
	}
}

func TestChildrenShareTheirParentsTerminal(t *testing.T) {
	pids := map[string]int{}
	Run(t, Do("root", func(c *Ctx, in int) int {
		pids["root"] = c.Terminal("sh").Pid()
		return in
	}, Do("kid", func(c *Ctx, in int) int {
		pids["kid"] = c.Terminal("sh").Pid()
		pids["other"] = c.Terminal("other").Pid()
		return in
	})), 0, bashHooks(t))
	if pids["root"] == 0 || pids["kid"] != pids["root"] || pids["other"] == pids["root"] {
		t.Fatalf("terminal pids %v, want kid's sh = root's sh and other a terminal of its own", pids)
	}
}

func TestAFailedWaitPrintsTheWholeScreen(t *testing.T) {
	out, failed, parent := inChild(t, func(t *testing.T) {
		Run(t, Do("root", func(c *Ctx, in int) int {
			term := c.Terminal("sh")
			term.WaitFor("READY$")
			c.deadline = spentDeadline
			term.WaitFor("never")
			return in
		}), 0, bashHooks(t))
	})
	if !parent {
		return
	}
	_, miss, _ := strings.Cut(out, "root: terminal sh:")
	if got, want := testDuration.ReplaceAllString(
		miss,
		"{d}",
	), " waited for \"never\" on the screen, past the hung-only limit; the screen:\n        ────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\n        READY$\n        ────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────\nTIMELINE\nFAIL       {d}    {d}  root\nPASS 0 · FAIL 1 · BLOCKED 0\n--- FAIL: TestAFailedWaitPrintsTheWholeScreen ({d})\n    --- FAIL: TestAFailedWaitPrintsTheWholeScreen/root ({d})\nFAIL\n"; !failed ||
		got != want {
		t.Fatalf("miss = %q, want %q (failed %v)", got, want, failed)
	}
}

func TestCleanupRunsWhenTheSubtreeEndsBeforeItsTerminalsClose(t *testing.T) {
	out, failed, parent := inChild(t, func(t *testing.T) {
		Run(t, Do("root", nil,
			Do("owner", func(c *Ctx, in int) int {
				term := c.Terminal("sh")
				term.WaitFor("READY$")
				c.Cleanup(func() {
					term.Type("echo torn-$((40+2))\r")
					term.WaitFor("torn-42")
					fmt.Println("TEARDOWN with its terminal open")
				})
				return in
			}, Do("bad", func(c *Ctx, in int) int { fmt.Println("STEP bad"); c.Failf("broken"); return in })),
			Do("after", func(_ *Ctx, in int) int { fmt.Println("STEP after"); return in }),
		), 0, bashHooks(t))
	})
	if !parent {
		return
	}
	var order []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "STEP ") || strings.HasPrefix(line, "TEARDOWN") {
			order = append(order, line)
		}
	}
	if got, want := strings.Join(
		order,
		"\n",
	), "STEP bad\nTEARDOWN with its terminal open\nSTEP after"; !failed ||
		got != want {
		t.Fatalf("order = %q, want %q (failed %v):\n%s", got, want, failed, out)
	}
}

// A terminal's controller end stays with the harness: a shell opened later inherits no descriptor of
// an earlier terminal, so closing that one hangs it up.
func TestALaterTerminalInheritsNoDescriptorOfAnEarlierOne(t *testing.T) {
	held := map[string]string{}
	list := func(c *Ctx, name string) {
		term := c.Terminal(name)
		// Sorted as numbers: a runner that leaves descriptors past 9 open would sort them before 2 as text.
		term.Type(`echo "HELD:$(ls /dev/fd | sort -n | tr '\n' ' ')"` + "\r")
		term.WaitScreen("the descriptors a child of "+name+" holds", func(screen string) bool {
			for _, line := range strings.Split(screen, "\n") {
				if rest, found := strings.CutPrefix(line, "HELD:"); found {
					held[name] = strings.TrimSpace(rest)
					return true
				}
			}
			return false
		})
	}
	Run(t, Do("root", func(c *Ctx, in int) int {
		list(c, "first")
		list(c, "second")
		return in
	}), 0, bashHooks(t))
	// Relative, never a fixed list: the runner's own descriptors past 2 differ by runner and OS.
	first := strings.Fields(held["first"])
	if len(first) < 3 || strings.Join(first[:3], " ") != "0 1 2" || held["second"] != held["first"] {
		t.Fatalf(
			"a child of the second terminal holds descriptors %q, want those a child of the first holds, %q (0 1 2 …)",
			held["second"], held["first"],
		)
	}
}
