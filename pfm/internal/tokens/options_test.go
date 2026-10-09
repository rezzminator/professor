package tokens

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestReadFlagsReadsTheWindowAndRepeatables(t *testing.T) {
	parsed, err := readFlags([]string{"--since", "3d", "--root", "/r1", "--top", "0", "--root", "/r2", "--codex"}, "h")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.hours != 72 || parsed.top != 0 || !parsed.codex ||
		!reflect.DeepEqual(parsed.roots, []string{"/r1", "/r2"}) {
		t.Errorf("parsed = %+v", parsed)
	}
	if parsed.hostLabel != "h" || parsed.familyRxSource != "." || !parsed.familyRx.MatchString("ANY") {
		t.Errorf("defaults: host label %q, family-rx %q", parsed.hostLabel, parsed.familyRxSource)
	}
	parsed, err = readFlags([]string{"--since", "1.5h", "--family-rx", "lander"}, "h")
	if err != nil || parsed.hours != 1.5 || !parsed.familyRx.MatchString("Flights-LANDER") {
		t.Errorf("1.5h, case-insensitive family-rx: %+v, %v", parsed, err)
	}
}

func TestReadFlagsRefusals(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"-h"}} {
		if _, err := readFlags(args, "h"); !errors.Is(err, errHelp) {
			t.Errorf("readFlags(%v) = %v, want help", args, err)
		}
	}
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"--timeline"}, "--timeline wants a transcript path"},
		{[]string{"--since", "5x"}, "--since wants a number plus h or d"},
		{[]string{"--top", "-1"}, "--top wants a whole number of rows"},
		{[]string{"stray"}, `unexpected argument "stray"`},
		{[]string{"--family-rx", "("}, "--family-rx (: "},
		{[]string{"--timeline", "t.jsonl", "--since", "24h"}, "--timeline reads whole files"},
		{[]string{"--timeline", "t.jsonl", "--codex"}, "--timeline reads whole files"},
		{[]string{"--nope"}, "nope"},
	}
	for _, c := range cases {
		_, err := readFlags(c.args, "h")
		if err == nil || errors.Is(err, errHelp) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("readFlags(%v) = %v, want a usage error holding %q", c.args, err, c.want)
		}
	}
}

func TestUsageLineIsTheFirstLine(t *testing.T) {
	if got := usageLine(
		"\n  flag provided but not defined: -x\nUsage: …\n",
	); got != "flag provided but not defined: -x" {
		t.Errorf("usageLine = %q", got)
	}
}
