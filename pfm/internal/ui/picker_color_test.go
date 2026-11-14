package ui

import (
	"bytes"
	"context"
	"io"
	"regexp"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
)

// TestInteractiveColorProfileIgnoresNoninteractiveColorOptOuts pins the
// capability-vs-preference line from interactiveColorProfileFrom's doc
// comment. Every environ here is constructed inline — no t.Setenv, so no
// ambient variable (this host's TMUX included) can reach the decision.
func TestInteractiveColorProfileIgnoresNoninteractiveColorOptOuts(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		environ []string
		want    colorprofile.Profile
	}{
		{
			// TERM=dumb is a terminal CAPABILITY statement, never overridden:
			// the honest sub-ANSI answer must survive even with NO_COLOR and
			// CLICOLOR=0 also present.
			name: "dumb terminal reports its honest capability",
			environ: []string{
				"TERM=dumb",
				"TTY_FORCE=0",
				"COLORTERM=",
				"NO_COLOR=1",
				"CLICOLOR=0",
			},
			want: colorprofile.NoTTY,
		},
		{
			// NO_COLOR and CLICOLOR=0 are shell PREFERENCES and are
			// overridden by design: a color-capable TERM keeps its
			// advertised profile despite both opt-outs being set.
			name: "color-capable terminal keeps its advertised profile despite the opt-outs",
			environ: []string{
				"TERM=xterm-256color",
				"TTY_FORCE=1",
				"COLORTERM=",
				"NO_COLOR=1",
				"CLICOLOR=0",
			},
			want: colorprofile.ANSI256,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := interactiveColorProfileFrom(io.Discard, testCase.environ); got != testCase.want {
				t.Fatalf("interactiveColorProfileFrom() = %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestInteractiveColorProfileKeepsAdvertisedCapabilities(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		term      string
		colorTerm string
		want      colorprofile.Profile
	}{
		{name: "256 color", term: "xterm-256color", want: colorprofile.ANSI256},
		{name: "true color", term: "xterm-256color", colorTerm: "truecolor", want: colorprofile.TrueColor},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			environ := []string{
				"TTY_FORCE=1",
				"NO_COLOR=",
				"CLICOLOR=",
				"TERM=" + testCase.term,
				"COLORTERM=" + testCase.colorTerm,
			}
			if got := interactiveColorProfileFrom(io.Discard, environ); got != testCase.want {
				t.Fatalf("interactiveColorProfileFrom() = %s, want %s", got, testCase.want)
			}
		})
	}
}

func TestBubblePickerRendererStaysColoredWhenParentDisablesColor(t *testing.T) {
	// A color-capable TERM, not a dumb one: NO_COLOR/CLICOLOR=0 are shell
	// PREFERENCES this test proves get overridden. TERM=dumb would instead
	// be a terminal CAPABILITY statement (see interactiveColorProfileFrom),
	// which is never overridden and belongs to a different test.
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TTY_FORCE", "1")
	t.Setenv("COLORTERM", "")
	t.Setenv("NO_COLOR", "1")
	t.Setenv("CLICOLOR", "0")

	terminal := &pickerTestTerminal{input: bytes.NewReader([]byte{'\x1b'})}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	outcome, err := (BubblePicker{
		OpenTTY: func() (ReadWriteCloser, error) { return terminal, nil },
	}).Pick(ctx, fixtureSnapshot(80))
	if err != nil {
		t.Fatalf("BubblePicker.Pick: %v\noutput=%q", err, terminal.output.String())
	}
	if outcome.Kind != OutcomeCancelled {
		t.Fatalf("BubblePicker outcome = %v, want cancelled", outcome.Kind)
	}
	if output := terminal.output.String(); !hasANSIColor(output) {
		t.Fatalf("Bubble Tea renderer emitted no ANSI color SGR with NO_COLOR=1 CLICOLOR=0: %q", output)
	}
}

func TestPlainAndTSVPickersRemainUncolored(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("TTY_FORCE", "1")
	t.Setenv("COLORTERM", "truecolor")
	snapshot := fixtureSnapshot(120)
	plainOutput := &bytes.Buffer{}
	tsvOutput := &bytes.Buffer{}

	for _, testCase := range []struct {
		name, golden string
		pick         Picker
		out          *bytes.Buffer
	}{
		{name: "plain", golden: "ui_plain.txt", pick: PlainPicker{Writer: plainOutput}, out: plainOutput},
		{name: "TSV", golden: "ui.tsv", pick: TSVPicker{Writer: tsvOutput}, out: tsvOutput},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := testCase.pick.Pick(context.Background(), snapshot); err != nil {
				t.Fatal(err)
			}
			golden(t, testCase.golden, testCase.out.String())
		})
	}
}

type pickerTestTerminal struct {
	input  *bytes.Reader
	output bytes.Buffer
}

func (terminal *pickerTestTerminal) Read(value []byte) (int, error) {
	return terminal.input.Read(value)
}

func (terminal *pickerTestTerminal) Write(value []byte) (int, error) {
	return terminal.output.Write(value)
}

func (*pickerTestTerminal) Close() error { return nil }

var ansiColorSGR = regexp.MustCompile(`\x1b\[[0-9;]*(?:3[0-7]|4[0-7]|9[0-7]|10[0-7]|38|48)[0-9;]*m`)

func hasANSIColor(value string) bool { return ansiColorSGR.MatchString(value) }
