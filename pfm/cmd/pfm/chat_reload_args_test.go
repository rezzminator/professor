package main

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/reload"
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
		{args: []string{"cache", "off"}, want: "--1h on|off"},
		{args: []string{"--cache", "off"}, want: "--1h on|off"},
		{args: []string{"account", "2"}, want: "--account N"},
		{args: []string{"then", "keep going"}, want: "--then"},
		{args: []string{"socket"}, want: "--sock"},
		{args: []string{"nonsense"}, want: "--account N"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			err := validateReloadArgs(test.args)
			if err == nil {
				t.Fatalf("%v was accepted", test.args)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error %q does not point at %q", err.Error(), test.want)
			}
			if !strings.Contains(err.Error(), test.args[0]) {
				t.Fatalf("error %q does not quote the rejected word %q", err.Error(), test.args[0])
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
		want    int
	}{
		{name: "flag form", args: []string{"--account", "2"}, want: 2},
		{name: "flag form with other flags", args: []string{"--1h", "off", "--account", "3"}, want: 3},
		{name: "bare positional still accepted", args: []string{"2"}, want: 2},
		{name: "no account", args: []string{"--1h", "off"}, want: 0},
		{name: "missing value", args: []string{"--account"}, wantErr: "--account needs an account number"},
		{name: "non-numeric value", args: []string{"--account", "personal"}, wantErr: "account NUMBER"},
		{name: "zero is not an account", args: []string{"--account", "0"}, wantErr: "account NUMBER"},
		{name: "twice", args: []string{"--account", "2", "--account", "3"}, wantErr: "twice"},
		{name: "flag and positional", args: []string{"--account", "2", "3"}, wantErr: "twice"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateReloadArgs(test.args)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("error = %v, want one containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := reloadRequestedAccount(test.args); got != test.want {
				t.Fatalf("account = %d, want %d", got, test.want)
			}
		})
	}
}

// A value that happens to look like a flag name must never be re-read as one.
// `--then "--account 4"` asks to send that text to the reborn chat; it does not
// ask to switch seats.
func TestReloadFlagValuesAreNotReparsedAsFlags(t *testing.T) {
	args := []string{"--then", "--account 4", "--1h", "off"}
	if err := validateReloadArgs(args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := reloadRequestedAccount(args); got != 0 {
		t.Fatalf("account = %d, want 0 — a --then payload was read as a seat switch", got)
	}
}

// --new is a bare flag (no value): accepted once, rejected twice, invisible
// to reloadRequestedAccount (it must never swallow the account that follows
// it), and a caller who spells it as the bare word "fresh" gets pointed at
// the real flag the same way "cache"/"account"/"then"/"sock" already are.
// The retired --fresh spelling is refused by name, like every other rejected
// word, and its refusal names --new as the replacement.
func TestReloadNewFlag(t *testing.T) {
	if err := validateReloadArgs([]string{"--new"}); err != nil {
		t.Fatalf("--new rejected: %v", err)
	}
	if err := validateReloadArgs(
		[]string{"--new", "--new"},
	); err == nil ||
		!strings.Contains(err.Error(), "new specified twice") {
		t.Fatalf("--new --new error=%v, want it to say \"new specified twice\"", err)
	}
	if got := reloadRequestedAccount([]string{"--new", "--account", "2"}); got != 2 {
		t.Fatalf("account=%d, want 2 — --new must not swallow the account flag that follows it", got)
	}
	if err := validateReloadArgs(
		[]string{"fresh"},
	); err == nil ||
		!strings.Contains(err.Error(), "did you mean --new?") {
		t.Fatalf("bare word \"fresh\" error=%v, want the --new hint", err)
	}
	if err := validateReloadArgs(
		[]string{"--fresh"},
	); err == nil ||
		!strings.Contains(err.Error(), "did you mean --new?") {
		t.Fatalf("retired --fresh error=%v, want it refused by name and pointed at --new", err)
	}
}

// --hide is --new's companion: the conversation a fresh reboot leaves
// behind is hidden from the picker instead of lingering as a resumable row.
// A bare flag, accepted once in either order beside --new, meaningless
// without it (a reload that resumes the same conversation cannot hide it),
// invisible to reloadRequestedAccount, and the bare word "hide" points at
// the flag the same way "fresh" does.
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
		!strings.Contains(err.Error(), "--hide needs --new") {
		t.Fatalf("--hide alone error=%v, want it to say \"--hide needs --new\"", err)
	}
	if err := validateReloadArgs(
		[]string{"--new", "--hide", "--hide"},
	); err == nil ||
		!strings.Contains(err.Error(), "hide specified twice") {
		t.Fatalf("--hide --hide error=%v, want it to say \"hide specified twice\"", err)
	}
	if got := reloadRequestedAccount([]string{"--new", "--hide", "--account", "2"}); got != 2 {
		t.Fatalf("account=%d, want 2 — --hide must not swallow the account flag that follows it", got)
	}
	if err := validateReloadArgs(
		[]string{"--new", "hide"},
	); err == nil ||
		!strings.Contains(err.Error(), "did you mean --hide?") {
		t.Fatalf("bare word \"hide\" error=%v, want the --hide hint", err)
	}
}

// The usage line is the last thing a confused caller reads, so it must carry
// the two facts that were missing: every setting has a flag, and the socket
// finds itself.
func TestReloadUsageTeachesTheFlagsAndTheSocketDefault(t *testing.T) {
	haystack := reload.Usage
	for _, needle := range []string{
		"--account N",
		"--1h on|off",
		"--new",
		"--hide",
		"--then",
		"--sock",
		"--model",
		"--effort",
		"detected automatically",
	} {
		if !strings.Contains(haystack, needle) {
			t.Errorf("reload.Usage is missing %q:\n%s", needle, reload.Usage)
		}
	}
}
