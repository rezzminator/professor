package cli

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestParseFlagsAnywhere(t *testing.T) {
	var stderr bytes.Buffer
	flags := NewFlagSet("test", "usage: test", &stderr)
	jsonOutput := flags.Bool("json", false, "json output")
	positional, code, ok := ParseFlagsAnywhere(flags, []string{"seat", "--json", "tail"})
	if !ok || code != 0 || !*jsonOutput || !reflect.DeepEqual(positional, []string{"seat", "tail"}) {
		t.Fatalf("ParseFlagsAnywhere() = (%v, %d, %t, json=%t)", positional, code, ok, *jsonOutput)
	}
}

func TestCloseResourceRecordsFirstFailure(t *testing.T) {
	exitCode := 0
	var stderr bytes.Buffer
	CloseResource(failingCloser{}, "close resource", &stderr, &exitCode)
	if exitCode != 1 || stderr.String() != "close resource: failed\n" {
		t.Fatalf("CloseResource() = code %d, stderr %q", exitCode, stderr.String())
	}
}

type failingCloser struct{}

func (failingCloser) Close() error { return errors.New("failed") }

var (
	_ io.Closer = failingCloser{}
	_           = flag.ErrHelp
)

// A prompt written after a positional name is prompt text, even where a word
// looks like a flag: parsing it as one printed usage and exited 0 for `-h`,
// failed for an unknown flag, and silently armed --await after `--`.
func TestParseFlagsAroundNameKeepsFlagLookingPromptWords(t *testing.T) {
	for _, test := range []struct {
		name       string
		args       []string
		wantName   string
		wantPrompt []string
		wantEngine string
		wantAwait  bool
	}{
		{
			name:       "flags after the name are read",
			args:       []string{"seat", "--engine", "cx", "do", "it"},
			wantName:   "seat",
			wantPrompt: []string{"do", "it"},
			wantEngine: "cx",
		},
		{
			name:       "a help-looking prompt word stays prompt",
			args:       []string{"seat", "explain", "the", "-h", "output"},
			wantName:   "seat",
			wantPrompt: []string{"explain", "the", "-h", "output"},
		},
		{
			name:       "an unknown-flag-looking prompt word stays prompt",
			args:       []string{"seat", "fix", "the", "-v", "handling"},
			wantName:   "seat",
			wantPrompt: []string{"fix", "the", "-v", "handling"},
		},
		{
			name:       "a terminator keeps every later word prompt",
			args:       []string{"--name", "x", "--", "review", "--await", "behavior"},
			wantName:   "x",
			wantPrompt: []string{"review", "--await", "behavior"},
		},
		{
			name:       "a terminator after the positional name",
			args:       []string{"seat", "--", "-v", "--await"},
			wantName:   "seat",
			wantPrompt: []string{"-v", "--await"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			flags := NewFlagSet("chat new", "usage: pfm chat new", &stderr)
			name := flags.String("name", "", "")
			engine := flags.String("engine", "", "")
			await := flags.Bool("await", false, "")
			prompt, code, ok := ParseFlagsAroundName(flags, name, test.args)
			if !ok {
				t.Fatalf(
					"ParseFlagsAroundName(%q) = exit %d, stderr=%q; want a parse",
					test.args,
					code,
					stderr.String(),
				)
			}
			if *name != test.wantName || *engine != test.wantEngine || *await != test.wantAwait ||
				strings.Join(prompt, "\x00") != strings.Join(test.wantPrompt, "\x00") {
				t.Fatalf(
					"ParseFlagsAroundName(%q) = name %q engine %q await %t prompt %q; want %q %q %t %q",
					test.args, *name, *engine, *await, prompt,
					test.wantName, test.wantEngine, test.wantAwait, test.wantPrompt,
				)
			}
		})
	}
}

func TestWriteLinesPreservesUsageLines(t *testing.T) {
	var out bytes.Buffer
	WriteLines(&out, []string{"usage", "", "command"})
	if out.String() != "usage\n\ncommand\n" {
		t.Fatalf("usage = %q", out.String())
	}
}
