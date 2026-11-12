package harness

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// bashHooks opens every terminal as a bare interactive bash prompting "READY$ ".
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
					5*time.Second,
					func(screen string) bool { return screen == row.want },
				)
				return in
			}), 0, bashHooks(t))
		})
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
			term.WaitFor("never", 100*time.Millisecond)
			return in
		}), 0, bashHooks(t))
	})
	if !parent {
		return
	}
	rule := strings.Repeat("─", Cols)
	want := `root: terminal sh: waited 100ms for "never" on the screen; the screen:` + "\n" +
		strings.Repeat(" ", 8) + rule + "\n" + strings.Repeat(" ", 8) + "READY$\n" + strings.Repeat(" ", 8) + rule
	i := strings.Index(out, "root: terminal sh:")
	if !failed || i < 0 || !strings.HasPrefix(out[i:], want) {
		t.Fatalf("failed=%v, want the miss with its whole screen:\n%s\ngot:\n%s", failed, want, out)
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
	bad, torn, after := strings.Index(out, "STEP bad"), strings.Index(out, "TEARDOWN with its terminal open"),
		strings.Index(out, "STEP after")
	if !failed || bad < 0 || torn < bad || after < torn || strings.Count(out, "TEARDOWN") != 1 {
		t.Fatalf("failed=%v, STEP bad at %d, TEARDOWN at %d (want once, after it), STEP after at %d (want last):\n%s",
			failed, bad, torn, after, out)
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
		term.WaitScreen("the descriptors a child of "+name+" holds", 5*time.Second, func(screen string) bool {
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
	if !strings.HasPrefix(held["first"], "0 1 2") || held["second"] != held["first"] {
		t.Fatalf(
			"a child of the second terminal holds descriptors %q, want those a child of the first holds, %q (0 1 2 …)",
			held["second"], held["first"],
		)
	}
}
