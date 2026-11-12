package harness

import (
	"os"
	"strings"
	"testing"
	"time"
)

// bashHooks opens every terminal as a bare interactive bash prompting "READY$ ".
func bashHooks(t *testing.T) Hooks {
	return Hooks{
		Shell: []string{"bash", "--norc", "--noprofile", "-i"},
		Env:   []string{"HOME=" + t.TempDir(), "PATH=" + os.Getenv("PATH"), "PS1=READY$ "},
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
