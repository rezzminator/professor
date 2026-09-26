package report

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
	"github.com/rezzminator/professor/pfm/internal/callmeter/cmdparse"
)

func TestShape(t *testing.T) {
	cases := []struct {
		name  string
		parts []storedPart
		want  string
	}{
		{"no parts", nil, UnparsedShape},
		{"cd and a filter drop", []storedPart{
			{program: "cd", args: []string{"x"}}, {program: "go", args: []string{"test", "./..."}}, {program: "tail"},
		}, "go test"},
		{"path first argument", []storedPart{{program: "/usr/bin/cat", args: []string{"./a.go"}}}, "cat"},
		{
			"script base name",
			[]storedPart{{program: ".claude/scripts/dev.sh", args: []string{"test", "pfm"}}},
			"dev.sh test",
		},
		{"flag first argument", []storedPart{{program: "grep", args: []string{"-rn", "x", "."}}}, "grep"},
		{"dedup and cap at three", []storedPart{
			{program: "git", args: []string{"diff"}},
			{program: "export", args: []string{"A=1"}},
			{program: "git", args: []string{"diff"}},
			{program: "git", args: []string{"log"}},
			{program: "wc", args: []string{"-l"}, files: []cmdparse.FileRef{{Path: "/a"}}},
			{program: "ls"},
		}, "git diff ; git log ; wc"},
		{"only trivial parts", []storedPart{{program: "cd", args: []string{"x"}}}, "cd x"},
		{"literal echo separator drops", []storedPart{
			{program: "sed", args: []string{"-n", "1,5p", "a"}, files: []cmdparse.FileRef{{Path: "/a"}}},
			{program: "echo", args: []string{"---"}},
			{program: "grep", args: []string{"x", "b"}, files: []cmdparse.FileRef{{Path: "/b"}}},
		}, "sed ; grep x"},
		{"literal printf, true and colon drop", []storedPart{
			{program: "printf", args: []string{"%s\\n", "done"}},
			{program: ":"},
			{program: "true", args: []string{"x"}},
			{program: "git", args: []string{"status"}},
		}, "git status"},
		{"echo of a variable stays", []storedPart{
			{program: "echo", args: []string{"$X"}}, {program: "git", args: []string{"status"}},
		}, "echo $X ; git status"},
		{"printf of a command substitution stays", []storedPart{
			{program: "printf", args: []string{"%s", "$(cat f)"}}, {program: "git", args: []string{"status"}},
		}, "printf %s ; git status"},
		{"echo of a backquote substitution stays", []storedPart{
			{program: "echo", args: []string{"`date`"}}, {program: "git", args: []string{"status"}},
		}, "echo `date` ; git status"},
		{
			"echo redirected to a file stays",
			[]storedPart{
				{
					program: "echo",
					args:    []string{"hi"},
					files:   []cmdparse.FileRef{{Path: "/o", Action: cmdparse.ActionWrite}},
				},
				{program: "git", args: []string{"status"}},
			},
			"echo hi ; git status",
		},
	}
	for _, c := range cases {
		if got := callShape(c.parts); got != c.want {
			t.Errorf("%s: shape = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCommandsShapeBandPersistedAndFollowUps(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dir := workDir(t)
	banded := bash("b1", "s", "A", ms(3*time.Hour), dir, "cd x && go test ./... | tail", 25000)
	banded.BytesReal = callmeter.Ptr(int64(25000))
	seed(t, store, banded)
	persisted := bash("b2", "s", "A", ms(2*time.Hour), dir, "go test ./internal/...", 2000)
	persisted.BytesReal = callmeter.Ptr(int64(40000))
	persisted.PersistedPath = callmeter.Ptr("/h/s/tool-results/b2.txt")
	seed(t, store, persisted)
	seed(t, store, read("r1", "A", "/h/s/tool-results/b2.txt", time.Hour, 9000))
	seed(t, store, read("r2", "B", "/h/s/tool-results/b2.txt", time.Hour, 9000))
	if _, err := EnsureParsed(ctx, store, "", nil); err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	table, err := Commands(ctx, store, Filter{}, nil)
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	want := [][]string{{"go test", "2", "27000", "2000", "25000", "1", "1", "1"}}
	if !reflect.DeepEqual(table.Rows, want) {
		t.Errorf("rows = %v\nwant   %v (header %v)", table.Rows, want, table.Header)
	}
}

func TestCommandsShapesParsedEchoAndPrintf(t *testing.T) {
	ctx := context.Background()
	store := openStore(t)
	dir := workDir(t, "a", "b", "out.txt")
	seed(t, store, bash("b1", "s", "A", ms(3*time.Hour), dir, "sed -n 1,5p a ; echo --- ; grep x b", 500))
	seed(t, store, bash("b2", "s", "A", ms(2*time.Hour), dir, "echo $X > out.txt ; git status", 400))
	seed(t, store, bash("b3", "s", "A", ms(time.Hour), dir, `printf '%s' "$(cat a)" ; git log`, 300))
	if _, err := EnsureParsed(ctx, store, "", nil); err != nil {
		t.Fatalf("EnsureParsed: %v", err)
	}
	table, err := Commands(ctx, store, Filter{}, nil)
	if err != nil {
		t.Fatalf("Commands: %v", err)
	}
	var shapes []string
	for _, row := range table.Rows {
		shapes = append(shapes, row[0])
	}
	want := []string{"sed ; grep x", "echo $X ; git status", "cat a ; printf %s ; git log"}
	if !reflect.DeepEqual(shapes, want) {
		t.Errorf("shapes = %q\nwant     %q", shapes, want)
	}
}
