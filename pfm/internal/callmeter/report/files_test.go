package report

import (
	"context"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
)

func read(id, agent, path string, age time.Duration, delivered int64) callmeter.Call {
	return callmeter.Call{
		ToolUseID: id, SessionID: callmeter.Ptr("s"), AgentID: callmeter.Ptr(agent), TS: callmeter.Ptr(ms(age)),
		Tool: callmeter.Ptr("Read"), FilePath: callmeter.Ptr(path), BytesDelivered: callmeter.Ptr(delivered),
	}
}

func TestFilesKeepsReadAndBashBytesApartAndCountsRereads(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dir := workDir(t, "f.go")
	path := filepath.Join(dir, "f.go")
	first := read("r1", "A", path, 4*time.Hour, 100)
	first.FileBytes = callmeter.Ptr(int64(500))
	seed(t, store, first)
	ranged := read("r2", "A", path, 3*time.Hour, 50)
	ranged.ReadStart = callmeter.Ptr(int64(10))
	seed(t, store, ranged)
	last := read("r3", "B", path, 2*time.Hour, 30)
	last.FileBytes = callmeter.Ptr(int64(999))
	seed(t, store, last)
	seed(t, store, bash("b1", "s", "A", ms(time.Hour), dir, "cat f.go", 70))
	if _, err := EnsureParsed(ctx, store, "", nil); err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	table, err := Files(ctx, store, Filter{}, nil)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := [][]string{{path, "180", "70", "4", "4", "0", "2", "999", "3", "1", "2"}}
	if !reflect.DeepEqual(table.Rows, want) {
		t.Errorf("rows = %v\nwant   %v (header %v)", table.Rows, want, table.Header)
	}
}

func TestFilesSplitsCertainAndConditionalReads(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dir := workDir(t, "f.go")
	path := filepath.Join(dir, "f.go")
	seed(t, store, bash("b1", "s", "A", ms(3*time.Hour), dir, "cat f.go", 10))
	seed(t, store, bash("b2", "s", "A", ms(2*time.Hour), dir, "true && cat f.go", 20))
	seed(t, store, bash("b3", "s", "A", ms(time.Hour), dir, "true || wc -l f.go; cat f.go", 30))
	if _, err := EnsureParsed(ctx, store, "", nil); err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	table, err := Files(ctx, store, Filter{}, nil)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	// b3 reads f.go both ways in one call: a certain read wins.
	want := [][]string{{path, "0", "60", "3", "2", "1", "1", "-", "3", "0", "2"}}
	if !reflect.DeepEqual(table.Rows, want) {
		t.Errorf("rows = %v\nwant   %v (header %v)", table.Rows, want, table.Header)
	}
}

func TestWritesSumsGrowthAndCountsBashWrites(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dir := workDir(t, "out.txt")
	write := func(id, tool string, before, after *int64) callmeter.Call {
		return callmeter.Call{
			ToolUseID: id, SessionID: callmeter.Ptr("s"), TS: callmeter.Ptr(ms(time.Hour)), Tool: callmeter.Ptr(tool),
			FilePath: callmeter.Ptr("/w/f.go"), FileBytesBefore: before, FileBytes: after,
		}
	}
	seed(t, store, write("w1", "Write", callmeter.Ptr(int64(100)), callmeter.Ptr(int64(150))))
	seed(t, store, write("w2", "Edit", callmeter.Ptr(int64(150)), callmeter.Ptr(int64(140))))
	seed(t, store, write("w3", "Edit", nil, callmeter.Ptr(int64(160))))
	seed(t, store, bash("b1", "s", "", ms(time.Hour), dir, "echo hi > out.txt", 0))
	if _, err := EnsureParsed(ctx, store, "", nil); err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	table, err := Writes(ctx, store, Filter{}, nil)
	if err != nil {
		t.Fatalf("Writes: %v", err)
	}
	want := [][]string{
		{"/w/f.go", "3", "3", "0", "40", "1"},
		{filepath.Join(dir, "out.txt"), "1", "0", "1", "0", "1"},
	}
	if !reflect.DeepEqual(table.Rows, want) {
		t.Errorf("rows = %v\nwant   %v (header %v)", table.Rows, want, table.Header)
	}
}

// bashBytesOf sums the BASH BYTES column and returns it per file.
func bashBytesOf(t *testing.T, table *Table) (map[string]string, int64) {
	t.Helper()
	col := -1
	for i, h := range table.Header {
		if h == "BASH BYTES" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("no BASH BYTES column in %v", table.Header)
	}
	per := map[string]string{}
	var sum int64
	for _, row := range table.Rows {
		per[row[0]] = row[col]
		n, err := strconv.ParseInt(row[col], 10, 64)
		if err != nil {
			t.Fatalf("BASH BYTES %q of %s: %v", row[col], row[0], err)
		}
		sum += n
	}
	return per, sum
}

func TestFilesSharesBashBytesAcrossCreditedFiles(t *testing.T) {
	cases := []struct {
		name      string
		files     []string
		command   string
		delivered int64
		want      map[string]string // file name -> BASH BYTES
	}{
		{
			"three files, remainder to the first",
			[]string{"a.go", "b.go", "c.go"},
			"cat a.go b.go c.go", 1000,
			map[string]string{"a.go": "334", "b.go": "333", "c.go": "333"},
		},
		{
			"a file named twice counts once",
			[]string{"a.go", "b.go"},
			"cat a.go; head -n 2 a.go b.go", 100,
			map[string]string{"a.go": "50", "b.go": "50"},
		},
		{
			"grep -l over a glob",
			[]string{"p.go", "q.go", "r.go", "s.go", "t.go"},
			"grep -l x *.go", 100,
			map[string]string{"p.go": "20", "q.go": "20", "r.go": "20", "s.go": "20", "t.go": "20"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			store := openStore(t)
			dir := workDir(t, c.files...)
			seed(t, store, bash("b1", "s", "A", ms(time.Hour), dir, c.command, c.delivered))
			if _, err := EnsureParsed(ctx, store, "", nil); err != nil {
				t.Fatalf("EnsureParsed: %v", err)
			}
			table, err := Files(ctx, store, Filter{}, nil)
			if err != nil {
				t.Fatalf("Files: %v", err)
			}
			per, sum := bashBytesOf(t, table)
			if sum != c.delivered {
				t.Errorf("BASH BYTES sum = %d, want the call's %d (rows %v)", sum, c.delivered, table.Rows)
			}
			want := map[string]string{}
			for name, v := range c.want {
				want[filepath.Join(dir, name)] = v
			}
			if !reflect.DeepEqual(per, want) {
				t.Errorf("BASH BYTES per file = %v\nwant %v", per, want)
			}
		})
	}
}

// A file the parse found missing (a scratch file since deleted) is a read
// like any other and shares the call's bytes; its SIZE says gone, never a 0
// or the "-" of a size never seen.
func TestFilesMarksAMissingFileGone(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dir := workDir(t, "a.go")
	seed(t, store, bash("b1", "s", "A", ms(time.Hour), dir, "cat a.go gone.go", 100))
	if _, err := EnsureParsed(ctx, store, "", nil); err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	table, err := Files(ctx, store, Filter{}, nil)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	want := [][]string{
		{filepath.Join(dir, "a.go"), "0", "50", "1", "1", "0", "1", "-", "1", "0", "0"},
		{filepath.Join(dir, "gone.go"), "0", "50", "1", "1", "0", "1", "gone", "1", "0", "0"},
	}
	if !reflect.DeepEqual(table.Rows, want) {
		t.Errorf("rows = %v\nwant   %v (header %v)", table.Rows, want, table.Header)
	}
}
