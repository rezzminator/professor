package report

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

// stepCall is a non-Bash call of agent in session s, age minutes back.
func stepCall(id, agent, tool, path string, age int, delivered int64) callmeter.Call {
	c := callmeter.Call{
		ToolUseID: id, SessionID: callmeter.Ptr("s"), AgentID: callmeter.Ptr(agent),
		TS: callmeter.Ptr(ms(time.Duration(age) * time.Minute)), Tool: callmeter.Ptr(tool),
		BytesDelivered: callmeter.Ptr(delivered),
	}
	if path != "" {
		c.FilePath = &path
	}
	return c
}

func TestSequencesListsRecurringRunsAndDropsContainedOnes(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	// A and B: search, read a file, read it again.
	for _, agent := range []string{"A", "B"} {
		seed(t, store, stepCall(agent+"1", agent, "Grep", "", 30, 100))
		seed(t, store, stepCall(agent+"2", agent, "Read", "/"+agent+"/x.go", 29, 1000))
		seed(t, store, stepCall(agent+"3", agent, "Read", "/"+agent+"/x.go", 28, 1000))
	}
	// C: search then read once, so "Grep → Read new" occurs more often than
	// the longer run and stays listed.
	seed(t, store, stepCall("C1", "C", "Grep", "", 30, 100))
	seed(t, store, stepCall("C2", "C", "Read", "/C/y.go", 29, 1000))
	// D alone: a costly run seen in one agent only, never listed; its Bash
	// call was never parsed.
	seed(t, store, stepCall("D1", "D", "Glob", "", 30, 100))
	seed(t, store, stepCall("D2", "D", "Write", "/D/z.go", 29, 900000))
	seed(t, store, bash("D3", "s", "D", ms(28*time.Minute), "/D", "go build ./...", 5))
	table, err := Sequences(ctx, store, Filter{}, nil)
	if err != nil {
		t.Fatalf("Sequences: %v", err)
	}
	// "Grep → Read new": 3 occurrences, 3300 bytes, score 9900; the
	// three-step run: 2 occurrences, 4200 bytes, score 8400. "Read new → Read
	// same" occurs only inside the three-step run, twice: dropped.
	want := [][]string{
		{"Grep → Read new", "3", "3", "3300", "1100"},
		{"Grep → Read new → Read same", "2", "2", "4200", "2100"},
	}
	if !reflect.DeepEqual(table.Rows, want) {
		t.Errorf("rows = %v\nwant   %v (header %v)", table.Rows, want, table.Header)
	}
	out := render(t, table)
	if !strings.Contains(out, `note: 1 Bash calls have no parsed parts: stepped as "Bash (unparsed)"`) {
		t.Errorf("report lacks the unparsed-Bash note:\n%s", out)
	}
}

func TestSequencesBashStepAndFileRole(t *testing.T) {
	cases := []struct {
		name  string
		parts []storedPart
		shape string
	}{
		{"no parts", nil, UnparsedBashStep},
		{
			"trivial parts skipped",
			[]storedPart{{program: "cd"}, {program: "/usr/bin/grep", args: []string{"-n"}}},
			"Bash grep",
		},
		{"only trivial parts", []storedPart{{program: "cd"}}, "Bash cd"},
		{
			"literal echo skipped",
			[]storedPart{{program: "echo", args: []string{"---"}}, {program: "grep", args: []string{"x"}}},
			"Bash grep",
		},
		{
			"echo of a variable kept",
			[]storedPart{{program: "echo", args: []string{"$X"}}, {program: "grep", args: []string{"x"}}},
			"Bash echo",
		},
	}
	for _, c := range cases {
		if shape, _ := bashStepOf(c.parts); shape != c.shape {
			t.Errorf("%s: shape = %q, want %q", c.name, shape, c.shape)
		}
	}
	key := runKeyOf([]seqStep{{shape: "Grep"}, {shape: "Read", file: "/a"}, {shape: "Bash sed", file: "/a"}})
	if want := "Grep → Read new → Bash sed same"; key != want {
		t.Errorf("run key = %q, want %q", key, want)
	}
}

func TestSequencesEmptyWindow(t *testing.T) {
	table, err := Sequences(context.Background(), openStore(t), Filter{}, nil)
	if err != nil {
		t.Fatalf("Sequences: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(render(t, table)), "\n")
	if len(lines) != 2 || lines[1] != EmptyLine {
		t.Errorf("empty report = %q, want heading plus %q", lines, EmptyLine)
	}
}
