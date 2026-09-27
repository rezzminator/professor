package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/cli"
)

// A seat named positionally keeps every flag written after its name: the
// flag parser must not stop at the name and silently birth a default chat.
func TestChatNewReadsFlagsAfterAPositionalName(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"chat", "new", "seat-x", "--timeout", "-1"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf(
			"chat new seat-x --timeout -1 = %d, want 2 (usage: the negative timeout after the name was read); stderr=%q",
			code,
			stderr.String(),
		)
	}
	if !strings.Contains(stderr.String(), "usage: pfm chat new") {
		t.Fatalf("stderr = %q, want the chat new usage line", stderr.String())
	}
}

// A prompt written after a positional name is prompt text, even where a word
// looks like a flag: parsing it as one printed usage and exited 0 for `-h`,
// failed for an unknown flag, and silently armed --await after `--`.
func TestParseRunFlagsKeepsFlagLookingPromptWords(t *testing.T) {
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
			flags := cli.NewFlagSet("chat new", "usage: pfm chat new", &stderr)
			name := flags.String("name", "", "")
			engine := flags.String("engine", "", "")
			await := flags.Bool("await", false, "")
			prompt, code, ok := parseRunFlags(flags, name, test.args)
			if !ok {
				t.Fatalf("parseRunFlags(%q) = exit %d, stderr=%q; want a parse", test.args, code, stderr.String())
			}
			if *name != test.wantName || *engine != test.wantEngine || *await != test.wantAwait ||
				strings.Join(prompt, "\x00") != strings.Join(test.wantPrompt, "\x00") {
				t.Fatalf(
					"parseRunFlags(%q) = name %q engine %q await %t prompt %q; want %q %q %t %q",
					test.args, *name, *engine, *await, prompt,
					test.wantName, test.wantEngine, test.wantAwait, test.wantPrompt,
				)
			}
		})
	}
}
