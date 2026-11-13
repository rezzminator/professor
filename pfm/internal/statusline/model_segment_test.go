package statusline

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

// Every level's bar shows only its filled blocks, in the level's own colour
// and no other level's, then the level word in the effort colour — on the main
// line and on a sub-agent row alike. The expected colours are literal on purpose.
func TestEffortBarWearsItsLevelsColourAndOnlyItsFilledBlocks(t *testing.T) {
	levels := []struct{ level, colour, bar string }{
		{"low", "\x1b[38;5;117m", "▰"},
		{"medium", "\x1b[38;5;118m", "▰▰"},
		{"high", "\x1b[38;5;226m", "▰▰▰"},
		{"xhigh", "\x1b[38;5;215m", "▰▰▰▰"},
		{"max", "\x1b[1;38;5;203m", "▰▰▰▰▰"},
	}
	var data input
	data.Thinking.Enabled = true
	seen := map[string]string{}
	for _, want := range levels {
		data.Effort.Level = want.level
		paint := want.colour + want.bar + reset + cEffort + " " + want.level + reset
		surfaces := map[string]struct{ got, want string }{
			"main line": {effortSuffix(data, autoEffortState{}), cMuted + "·" + reset + paint},
			"sub-agent row": {
				subagentModel("claude-opus-5-5", want.level, "", sessionEffortRecord{}),
				cModel + "opus" + reset + cMuted + "·" + reset + paint,
			},
		}
		for surface, pair := range surfaces {
			if pair.got != pair.want {
				t.Errorf("%s at %s = %q, want %q", surface, want.level, pair.got, pair.want)
			}
			plain := styleANSI.ReplaceAllString(pair.got, "")
			if strings.Contains(plain, "▱") || strings.Contains(plain, "effort") ||
				strings.Count(plain, "▰") != strings.Count(want.bar, "▰") {
				t.Errorf("%s at %s = %q, want the filled blocks only and no effort word", surface, want.level, plain)
			}
		}
		if other, dup := seen[want.colour]; dup {
			t.Errorf("levels %s and %s share the bar colour %q", other, want.level, want.colour)
		}
		seen[want.colour] = want.level
	}
	data.Effort.Level = "turbo"
	if got, want := effortSuffix(data, autoEffortState{}),
		cMuted+"·"+reset+cEffort+"effort turbo (unknown)"+reset; got != want {
		t.Errorf("an unknown level = %q, want %q: no bar, no level colour", got, want)
	}
}

// The plugin rewrites the file in place, so a read can meet a torn write: the
// reader reads once more before it calls the file broken.
func TestAutoEffortStateReadsATornWriteOnceMore(t *testing.T) {
	good := []byte(
		`{"version":3,"mode":"auto","level":"medium","reason":"r","keeper":42,"updatedAt":"2026-10-17T09:30:00.000Z"}` + "\n",
	)
	for _, testcase := range []struct {
		name      string
		reads     [][]byte
		wantMode  string
		wantLevel string
		wantErr   string
	}{
		{name: "torn then whole", reads: [][]byte{[]byte(`{"version":3,"mo`), good}, wantMode: "auto", wantLevel: "medium"},
		{name: "torn twice", reads: [][]byte{[]byte(`{"version":3,"mo`), []byte(`{"vers`)}, wantErr: "parse"},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			calls := 0
			got := parseAutoEffortState("/state/state.s.json", func(string) ([]byte, error) {
				calls++
				return testcase.reads[calls-1], nil
			}, func(pid int) (bool, error) { return pid == 42, nil })
			if calls != 2 {
				t.Fatalf("reads = %d, want 2", calls)
			}
			if testcase.wantErr != "" {
				if got.err == nil || !strings.Contains(got.err.Error(), testcase.wantErr) {
					t.Fatalf("state = %+v, want an error containing %q", got, testcase.wantErr)
				}
				return
			}
			if got.err != nil || got.mode != testcase.wantMode || got.level != testcase.wantLevel {
				t.Fatalf("state = %+v, want mode %q level %q", got, testcase.wantMode, testcase.wantLevel)
			}
		})
	}
	if got := parseAutoEffortState(
		"/state/none.json",
		func(string) ([]byte, error) { return nil, os.ErrNotExist },
		func(int) (bool, error) { return true, nil },
	); got.found ||
		got.err != nil {
		t.Fatalf("missing file state = %+v, want absent", got)
	}
	if got := parseAutoEffortState(
		"/state/big.json",
		func(string) ([]byte, error) { return nil, errors.New("denied") },
		func(int) (bool, error) { return true, nil },
	); got.err == nil {
		t.Fatalf("unreadable file state = %+v, want an error", got)
	}
}

// The keeper runs as the status line's own user, so a live pid owned by another
// user is a stale line whose pid was reused, never the keeper — whether that
// process's argv cannot be read (macOS refuses another user's argv) or shows
// the keeper mark (Linux shows it).
func TestKeeperProbeCallsAnotherUsersProcessNoKeeper(t *testing.T) {
	const pid = 4242
	for name, proc := range map[string]keepwarmProcFixture{
		"argv refused": {argvErr: syscall.EPERM, foreign: true},
		"argv shown":   {argv: []string{"/bin/sh", keeperMark}, foreign: true},
	} {
		probe := keeperProbe(proc, func(int) (bool, error) { return true, nil })
		if live, err := probe(pid); live || err != nil {
			t.Fatalf(
				"%s: probe(%d) = %v, %v; want false, nil: another user's process is no keeper",
				name,
				pid,
				live,
				err,
			)
		}
	}
}

// A keeper probe that cannot tell whether the pid lives is an error, never "no
// keeper", and a zombie is no keeper.
func TestKeeperProbeSeparatesFailureFromAbsence(t *testing.T) {
	const pid = 4242
	failing := keeperProbe(keepwarmProcFixture{argv: []string{keeperMark}}, func(int) (bool, error) {
		return false, syscall.EIO
	})
	if live, err := failing(pid); live || err == nil || !strings.Contains(err.Error(), "4242") {
		t.Fatalf("failed probe = %v, %v; want an error naming pid %d", live, err, pid)
	}
	zombie := keeperProbe(keepwarmProcFixture{argv: []string{keeperMark}, zombie: true}, func(int) (bool, error) {
		return true, nil
	})
	if live, err := zombie(pid); live || err != nil {
		t.Fatalf("zombie probe = %v, %v; want false, nil", live, err)
	}
}

// A keeper the reader refuses reaches the warn line quoted and clipped, as the
// mode and the parse text do: the file's bytes never reach stderr raw.
func TestAutoEffortStateQuotesTheKeeperItRefuses(t *testing.T) {
	keeper := "\"" + strings.Repeat("x", 200) + "\u009b[2J\""
	got := parseAutoEffortState("/state/state.s.json", func(string) ([]byte, error) {
		return []byte(`{"version":3,"mode":"auto","level":"medium","reason":"r","keeper":` + keeper + `}`), nil
	}, func(int) (bool, error) { return true, nil })
	if got.err == nil {
		t.Fatalf("state = %+v, want an error", got)
	}
	text := got.err.Error()
	if strings.ContainsRune(text, '\u009b') || !strings.Contains(text, "…") || len(text) > 200 ||
		!strings.Contains(text, "is no pid") {
		t.Fatalf("err = %q, want the keeper quoted and clipped", text)
	}
}

// A state file with no path (the home is unknown) is named once, by its cause:
// the warn line holds no empty path slot.
func TestAutoEffortWarnNamesAStateFileWithNoHome(t *testing.T) {
	var warn bytes.Buffer
	data := input{SessionID: "sess1"}
	data.Thinking.Enabled = true
	got := sessionAutoEffort(Runtime{Env: map[string]string{}, Warn: &warn}, data)
	if got.err == nil {
		t.Fatalf("state = %+v, want an error for the unknown home", got)
	}
	if line := warn.String(); !strings.HasPrefix(line, "statusline: auto-effort state file") ||
		strings.Contains(line, "  ") || !strings.Contains(line, "the home directory is unknown") {
		t.Fatalf("warn = %q, want one line naming the state file by its cause", line)
	}
}
