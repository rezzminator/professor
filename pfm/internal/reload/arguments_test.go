package reload

import (
	"strings"
	"testing"
)

// The spellings users actually typed and saw refused: `/reload --1h on`,
// `/reload --model=opus effort=high`, `/reload --model opus effort high`. Every
// setting spelling lands on its flag, every cache spelling on the one canonical
// --cache 1h|5m, and a flag's value is never rewritten.
func TestNormalizeArgumentsSpellings(t *testing.T) {
	for _, test := range []struct {
		args []string
		want []string
	}{
		{args: []string{"--1h", "on"}, want: []string{"--cache", "1h"}},
		{args: []string{"--1h"}, want: []string{"--cache", "1h"}},
		{args: []string{"--5m"}, want: []string{"--cache", "5m"}},
		{args: []string{"--5m", "on"}, want: []string{"--cache", "5m"}},
		{args: []string{"--cache", "on"}, want: []string{"--cache", "1h"}},
		{args: []string{"--cache", "off"}, want: []string{"--cache", "5m"}},
		{args: []string{"--cache", "1H"}, want: []string{"--cache", "1h"}},
		{args: []string{"--account", "2", "--1h", "on", "--new"}, want: []string{"--account", "2", "--cache", "1h", "--new"}},
		{args: []string{"--then", "--1h", "--5m"}, want: []string{"--then", "--1h", "--cache", "5m"}},
		{args: []string{"--account", "2"}, want: []string{"--account", "2"}},
		{args: []string{"--model=opus", "effort=high"}, want: []string{"--model", "opus", "--effort", "high"}},
		{args: []string{"--model", "opus", "effort", "high"}, want: []string{"--model", "opus", "--effort", "high"}},
		{args: []string{"model", "opus", "--effort=high", "account=2", "new", "cache=on"}, want: []string{"--model", "opus", "--effort", "high", "--account", "2", "--new", "--cache", "1h"}},
		{args: []string{"--then", "effort=high", "Model=sonnet"}, want: []string{"--then", "effort=high", "--model", "sonnet"}},
		{args: []string{"2"}, want: []string{"2"}},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			got := NormalizeArguments(test.args)
			if strings.Join(got, "\x00") != strings.Join(test.want, "\x00") {
				t.Fatalf("NormalizeArguments(%q) = %q, want %q", test.args, got, test.want)
			}
		})
	}
}
