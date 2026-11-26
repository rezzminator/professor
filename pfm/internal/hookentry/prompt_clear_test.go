package hookentry

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	"github.com/rezzminator/professor/pfm/internal/config"
)

// promptScreen plays an input box: each read returns the next frame (the
// last one repeats) and every key sent is recorded.
type promptScreen struct {
	pfmcall.Screen
	frames []string
	fails  int
	reads  int
	keys   []string
}

func (screen *promptScreen) CaptureScreen(context.Context, pfmcall.Target) (string, error) {
	screen.reads++
	if screen.reads <= screen.fails {
		return "", errors.New("pane_not_found")
	}
	frame := screen.frames[min(screen.reads-screen.fails, len(screen.frames))-1]
	return frame, nil
}

func (screen *promptScreen) SendKey(_ context.Context, _ pfmcall.Target, key string) error {
	screen.keys = append(screen.keys, key)
	return nil
}

func box(draft string) string {
	return "● answered\n\n────────────────────\n❯ " + draft + "\n────────────────────\n  ? for shortcuts\n"
}

// The typed command Claude Code puts back into the input box is cleared once
// the box holds exactly it; any other text in the box is never touched.
func TestClearTypedPromptClearsOnlyTheTypedCommand(t *testing.T) {
	const typed = `/pfm:space "deep work"`
	for _, test := range []struct {
		name    string
		frames  []string
		fails   int
		keys    []string
		cleared bool
		reads   int
	}{
		{name: "the box holds the command", frames: []string{box(typed)}, keys: []string{"C-e", "C-u"}, cleared: true, reads: 1},
		{
			name: "the box is put back after a read", frames: []string{box(""), box(typed)},
			keys: []string{"C-e", "C-u"}, cleared: true, reads: 2,
		},
		{
			name: "a read fails while the chat moves", frames: []string{box(typed)}, fails: 2,
			keys: []string{"C-e", "C-u"}, cleared: true, reads: 3,
		},
		{name: "the box stays empty", frames: []string{box("")}, reads: 3},
		{name: "the box holds other text", frames: []string{box("hello there")}, reads: 3},
		{name: "the box holds the command and more", frames: []string{box(typed + " now")}, reads: 3},
	} {
		t.Run(test.name, func(t *testing.T) {
			screen := &promptScreen{frames: test.frames, fails: test.fails}
			waits := 0
			wait := func(context.Context) error { waits++; return nil }
			cleared, err := clearTypedPrompt(
				context.Background(),
				screen,
				pfmcall.Target{Pane: "w1:p2"},
				" "+typed+" ",
				3,
				wait,
			)
			if err != nil || cleared != test.cleared || !reflect.DeepEqual(screen.keys, test.keys) ||
				screen.reads != test.reads || waits != test.reads-1 {
				t.Fatalf("clear = %v, %v, keys %q, reads %d, waits %d; want %v, keys %q, reads %d",
					cleared, err, screen.keys, screen.reads, waits, test.cleared, test.keys, test.reads)
			}
		})
	}
}

// A box no read could reach is an error naming the last failure.
func TestClearTypedPromptNamesAnUnreadableBox(t *testing.T) {
	screen := &promptScreen{frames: []string{box("")}, fails: 9}
	cleared, err := clearTypedPrompt(context.Background(), screen, pfmcall.Target{Pane: "w1:p2"}, "/pfm:space", 2,
		func(context.Context) error { return nil })
	want := "read the input box of pane w1:p2: pane_not_found"
	if cleared || err == nil || err.Error() != want || screen.keys != nil {
		t.Fatalf("clear = %v, %v, keys %q; want false, %q, no keys", cleared, err, screen.keys, want)
	}
}

// command-intercept with `clear` is the detached clear, never the hook: a
// clear without its pane is refused with its usage before any read.
func TestCommandInterceptClearRefusesAClearWithoutItsPane(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := CommandIntercept([]string{"clear", "--socket", "/fixture/herdr.sock", "--", "/pfm:space"},
		bytes.NewReader(nil), &stdout, &stderr, config.Runtime{}, nil, nil)
	want := "usage: pfm internal command-intercept clear --backend tmux|herdr --socket path --pane id [--seat id] -- prompt\n"
	if code != 2 || stdout.String() != "" || stderr.String() != want {
		t.Fatalf("clear without a pane = exit %d, stdout %q, stderr %q; want exit 2 and the usage %q",
			code, stdout.String(), stderr.String(), want)
	}
}
