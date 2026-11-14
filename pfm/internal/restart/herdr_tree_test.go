package restart

import (
	"context"
	"slices"
	"strings"
	"testing"
)

// TestHerdrTreeIsTheRestartedSessionsServerAlone: with a second Herdr session's server running beside
// the one the restart's environment names, the plan counts and stops only that one's tree; the other
// server and what it runs are never signalled, whichever session the restart was typed in.
func TestHerdrTreeIsTheRestartedSessionsServerAlone(t *testing.T) {
	const other = 50
	for _, row := range []struct {
		name    string
		env     []string
		marks   map[int]map[string]string
		signals []string
	}{
		{
			name:    "the default session, another session's server beside it",
			marks:   map[int]map[string]string{other: {herdrSessionEnv: "work"}},
			signals: []string{"31 terminated"},
		},
		{
			name: "a named session, the default server beside it",
			env:  []string{herdrSessionEnv + "=work"},
			marks: map[int]map[string]string{
				herdrServerPID: {herdrSessionEnv: "work"}, other: {},
			},
			signals: []string{"31 terminated"},
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			finder, _, kernel := herdrHost(t, &herdrModel{running: true},
				process{PID: 31, Parent: herdrServerPID, Argv: []string{"-zsh"}},
				process{PID: other, Parent: 1, Argv: []string{"/opt/bin/herdr", "server"}},
				process{PID: 51, Parent: other, Argv: []string{"-zsh"}},
			)
			finder.deps.Env = row.env
			for pid, environment := range row.marks {
				kernel.marks[pid] = environment
			}
			table, err := finder.readTable()
			finder.findHerdr(context.Background(), table, err)
			if want := "2 process(es), the server and every one under it"; len(finder.plan.Units) != 1 ||
				!strings.Contains(finder.plan.Units[0].FoundBy, want) {
				t.Fatalf("units %v found by %q, want one counting %q", unitNames(finder.plan.Units),
					finder.plan.Units[0].FoundBy, want)
			}
			detail, err := finder.plan.Units[0].Stop(context.Background())
			if want := "1 of its 2 process(es) outlived it, each stopped: pid 31"; err != nil ||
				!strings.Contains(detail, want) {
				t.Fatalf("stop = %q, %v; want %q", detail, err, want)
			}
			if got := kernel.sent(); !slices.Equal(got, row.signals) {
				t.Fatalf("signals = %v, want %v: the other session's server %d and its shell 51 untouched",
					got, row.signals, other)
			}
		})
	}
}

// TestHerdrTreeOfASessionWithNoServerIsUnverified: a restart typed in a session whose server is not
// in the table names that session, never another session's server as its tree.
func TestHerdrTreeOfASessionWithNoServerIsUnverified(t *testing.T) {
	finder, _, _ := herdrHost(t, &herdrModel{running: true})
	finder.deps.Env = []string{herdrSessionEnv + "=gone"}
	table, err := finder.readTable()
	finder.findHerdr(context.Background(), table, err)
	_, stopErr := finder.plan.Units[0].Stop(context.Background())
	if want := "no herdr server process of session gone in the process table"; stopErr == nil ||
		stopErr.Error() != "server stopped; herdr status reads not running, but the processes it ran are unverified: no herdr server process of session gone in the process table (1 listed)" {
		t.Fatalf("stop = %v, want %q", stopErr, want)
	}
}
