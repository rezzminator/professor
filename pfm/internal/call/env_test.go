package call

import (
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestApplyEnvironmentNamesEveryRejectedKnob(t *testing.T) {
	options := withDefaults(Options{})
	var warn strings.Builder
	applyEnvironment(&options, &paths.MapEnv{Values: map[string]string{
		"CHAT_CALL_POLL":        "fast",
		"CHAT_CALL_LOCK_POLL":   "0.5",
		"CHAT_CALL_BUSY_TRIES":  "0",
		"CHAT_CALL_ENTER_TRIES": "3",
	}}, &warn)
	if options.Poll != 200*time.Millisecond || options.LockPoll != 500*time.Millisecond ||
		options.BusyTries != 5 || options.EnterTries != 3 {
		t.Fatalf("poll=%s lock poll=%s busy=%d enter=%d, want 200ms 500ms 5 3",
			options.Poll, options.LockPoll, options.BusyTries, options.EnterTries)
	}
	want := `pfm: ignoring CHAT_CALL_POLL="fast": want seconds >= 0; using 200ms
pfm: ignoring CHAT_CALL_BUSY_TRIES="0": want a positive integer; using 5
`
	if warn.String() != want {
		t.Fatalf("warnings:\n%s\nwant:\n%s", warn.String(), want)
	}
}

func TestPositiveIntEnvKeepsFallbackForUnsetAndBadValues(t *testing.T) {
	for _, tc := range []struct {
		value     string
		want      int
		wantWarn  string
		configure bool
	}{
		{want: 20},
		{value: "7", want: 7, configure: true},
		{value: "1", want: 1, configure: true},
		{
			value: "0", want: 20, configure: true,
			wantWarn: "pfm: ignoring PFM_RELOAD_EXIT_TRIES=\"0\": want a positive integer; using 20\n",
		},
		{
			value: "seven", want: 20, configure: true,
			wantWarn: "pfm: ignoring PFM_RELOAD_EXIT_TRIES=\"seven\": want a positive integer; using 20\n",
		},
	} {
		env := &paths.MapEnv{Values: map[string]string{}}
		if tc.configure {
			env.Values["PFM_RELOAD_EXIT_TRIES"] = tc.value
		}
		var warn strings.Builder
		got := PositiveIntEnv(env, "PFM_RELOAD_EXIT_TRIES", 20, &warn)
		if got != tc.want || warn.String() != tc.wantWarn {
			t.Errorf("value %q: got %d warn %q, want %d warn %q", tc.value, got, warn.String(), tc.want, tc.wantWarn)
		}
	}
}
