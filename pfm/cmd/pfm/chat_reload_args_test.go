package main

import (
	"cmp"
	"fmt"
	"strings"
	"testing"
)

// The exact call that failed on a real host: a caller told "reload the cache
// off" wrote the words it was given into the positional slot. The command must
// reject it AND say what the right flag is — an error that only restates the
// grammar leaves the caller to guess a second time.
func TestReloadRejectsProseAndNamesTheRightFlag(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{args: []string{"cache", "off"}, want: "\"cache\" is not a reload argument — did you mean --cache 1h|5m?\nusage: pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then \"prompt\"] [--sock socket]\n       with no --sock, the calling chat's own pane is detected automatically;\n       --hide (with --new) hides the conversation left behind from the picker"},
		{args: []string{"1h"}, want: "\"1h\" is not a reload argument — did you mean --cache 1h|5m?\nusage: pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then \"prompt\"] [--sock socket]\n       with no --sock, the calling chat's own pane is detected automatically;\n       --hide (with --new) hides the conversation left behind from the picker"},
		{args: []string{"ttl"}, want: "\"ttl\" is not a reload argument — did you mean --cache 1h|5m?\nusage: pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then \"prompt\"] [--sock socket]\n       with no --sock, the calling chat's own pane is detected automatically;\n       --hide (with --new) hides the conversation left behind from the picker"},
		{args: []string{"--cache", "weekly"}, want: "--cache must be 1h|5m"},
		{args: []string{"account", "2"}, want: "\"account\" is not a reload argument — did you mean --account N?\nusage: pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then \"prompt\"] [--sock socket]\n       with no --sock, the calling chat's own pane is detected automatically;\n       --hide (with --new) hides the conversation left behind from the picker"},
		{args: []string{"then", "keep going"}, want: "\"then\" is not a reload argument — did you mean --then \"prompt\"?\nusage: pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then \"prompt\"] [--sock socket]\n       with no --sock, the calling chat's own pane is detected automatically;\n       --hide (with --new) hides the conversation left behind from the picker"},
		{args: []string{"socket"}, want: "\"socket\" is not a reload argument — did you mean --sock socket? (omit it and the calling chat is detected automatically)\nusage: pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then \"prompt\"] [--sock socket]\n       with no --sock, the calling chat's own pane is detected automatically;\n       --hide (with --new) hides the conversation left behind from the picker"},
		{args: []string{"nonsense"}, want: "\"nonsense\" is not a reload argument — an account is passed as --account N, and every other setting has its own flag\nusage: pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then \"prompt\"] [--sock socket]\n       with no --sock, the calling chat's own pane is detected automatically;\n       --hide (with --new) hides the conversation left behind from the picker"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			if got := fmt.Sprint(validateReloadArgs(test.args)); got != test.want {
				t.Fatalf("error = %q, want %q", got, test.want)
			}
		})
	}
}

// --account is the documented spelling and must work everywhere the bare
// positional did.
func TestReloadAccountFlag(t *testing.T) {
	for _, test := range []struct {
		name    string
		args    []string
		wantErr string
	}{
		{name: "flag form", args: []string{"--account", "2"}},
		{name: "flag form with other flags", args: []string{"--cache", "1h", "--account", "3"}},
		{name: "bare positional still accepted", args: []string{"2"}},
		{name: "no account", args: []string{"--cache", "5m"}},
		{name: "missing value", args: []string{"--account"}, wantErr: "--account needs an account number, as in --account 2"},
		{name: "non-numeric value", args: []string{"--account", "personal"}, wantErr: "--account takes an account NUMBER, not \"personal\" — see `pfm config show` for the configured accounts"},
		{name: "zero is not an account", args: []string{"--account", "0"}, wantErr: "--account takes an account NUMBER, not \"0\" — see `pfm config show` for the configured accounts"},
		{name: "twice", args: []string{"--account", "2", "--account", "3"}, wantErr: "account specified twice"},
		{name: "flag and positional", args: []string{"--account", "2", "3"}, wantErr: "account specified twice"},
	} {
		t.Run(test.name, func(t *testing.T) {
			want := cmp.Or(test.wantErr, "<nil>")
			if got := fmt.Sprint(validateReloadArgs(test.args)); got != want {
				t.Fatalf("error = %q, want %q", got, want)
			}
		})
	}
}

// A value that happens to look like a flag name must never be re-read as one.
// TestChatReloadWorkerNeverReadsAThenPayloadAsASeat proves that
// `--then "--account 4"` reaches the reborn chat as text.
func TestReloadFlagValuesAreNotReparsedAsFlags(t *testing.T) {
	args := []string{"--then", "--account 4", "--cache", "5m"}
	if err := validateReloadArgs(args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --new is a bare flag (no value): accepted once, rejected twice, and a caller
// who spells it as the bare word "fresh" gets pointed at the real flag the
// same way "cache"/"account"/"then"/"sock" already are. The worker test covers
// the account after --new and --hide.
// The retired --fresh spelling is refused by name, like every other rejected
// word, and its refusal names --new as the replacement.
func TestReloadNewFlag(t *testing.T) {
	if err := validateReloadArgs([]string{"--new"}); err != nil {
		t.Fatalf("--new rejected: %v", err)
	}
	if err := validateReloadArgs(
		[]string{"--new", "--new"},
	); err == nil ||
		err.Error() != "new specified twice" {
		t.Fatalf("--new --new error=%v, want it to say \"new specified twice\"", err)
	}
	if err := validateReloadArgs(
		[]string{"fresh"},
	); err == nil ||
		err.Error() != `"fresh" is not a reload argument — did you mean --new?
usage: pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then "prompt"] [--sock socket]
       with no --sock, the calling chat's own pane is detected automatically;
       --hide (with --new) hides the conversation left behind from the picker` {
		t.Fatalf("bare word \"fresh\" error=%v, want the --new hint", err)
	}
	if err := validateReloadArgs(
		[]string{"--fresh"},
	); err == nil ||
		err.Error() != `"--fresh" is not a reload argument — did you mean --new?
usage: pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then "prompt"] [--sock socket]
       with no --sock, the calling chat's own pane is detected automatically;
       --hide (with --new) hides the conversation left behind from the picker` {
		t.Fatalf("retired --fresh error=%v, want it refused by name and pointed at --new", err)
	}
}

// --hide is --new's companion: the conversation a fresh reboot leaves
// behind is hidden from the picker instead of lingering as a resumable row.
// A bare flag, accepted once in either order beside --new, meaningless
// without it (a reload that resumes the same conversation cannot hide it),
// and the bare word "hide" points at the flag the same way "fresh" does.
func TestReloadHideFlag(t *testing.T) {
	if err := validateReloadArgs([]string{"--new", "--hide"}); err != nil {
		t.Fatalf("--new --hide rejected: %v", err)
	}
	if err := validateReloadArgs([]string{"--hide", "--new"}); err != nil {
		t.Fatalf("--hide --new rejected: %v — the two flags must be order-free", err)
	}
	if err := validateReloadArgs(
		[]string{"--hide"},
	); err == nil ||
		err.Error() != "--hide needs --new — a reload that resumes the same conversation cannot hide it" {
		t.Fatalf("--hide alone error=%v, want it to say \"--hide needs --new\"", err)
	}
	if err := validateReloadArgs(
		[]string{"--new", "--hide", "--hide"},
	); err == nil ||
		err.Error() != "hide specified twice" {
		t.Fatalf("--hide --hide error=%v, want it to say \"hide specified twice\"", err)
	}
	if err := validateReloadArgs(
		[]string{"--new", "hide"},
	); err == nil ||
		err.Error() != `"hide" is not a reload argument — did you mean --hide? (beside --new: hides the conversation left behind)
usage: pfm chat reload [--account N] [--model M] [--effort E] [--cache 1h|5m (or --1h, --5m, --cache on|off)] [--new [--hide]] [--then "prompt"] [--sock socket]
       with no --sock, the calling chat's own pane is detected automatically;
       --hide (with --new) hides the conversation left behind from the picker` {
		t.Fatalf("bare word \"hide\" error=%v, want the --hide hint", err)
	}
}

// The spelling a user actually typed, twice, before the refusal was traced:
// `/reload --1h on`. Every cache spelling a person reaches for lands on the
// one canonical --cache 1h|5m before validation, the worker included.
func TestReloadNormalizesCacheSpellings(t *testing.T) {
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
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			got := normalizeReloadArgs(test.args)
			if strings.Join(got, "\x00") != strings.Join(test.want, "\x00") {
				t.Fatalf("normalizeReloadArgs(%q) = %q, want %q", test.args, got, test.want)
			}
			if err := validateReloadArgs(got); err != nil {
				t.Fatalf("normalized %q still refused: %v", got, err)
			}
		})
	}
}
