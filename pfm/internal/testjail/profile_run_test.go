package testjail

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUnitRunBase(t *testing.T) {
	cases := []struct{ name, moduleDir, want, err string }{
		{"a leading dot is dropped", "/work/.professor/pfm", "/tmp/professor/unit", ""},
		{"a worktree is its own project", "/work/.professor/.worktrees/unit-net/pfm", "/tmp/unit-net/unit", ""},
		{"a plain checkout name stays", "/src/professor/pfm", "/tmp/professor/unit", ""},
		{"no module names no project", "", "", "unit run: no go.mod above the launch directory names the project"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := unitRunBase("/tmp", tc.moduleDir)
			gotErr := ""
			if err != nil {
				gotErr = err.Error()
			}
			if got != tc.want || gotErr != tc.err {
				t.Fatalf("unitRunBase(/tmp, %q) = %q, %q; want %q, %q", tc.moduleDir, got, gotErr, tc.want, tc.err)
			}
		})
	}
}

// TestOpenUnitRunKeepsTheNewestRuns: the process creating a run links latest at it and keeps the
// newest keep runs, never an entry that is not a run; a process joining a run changes neither.
func TestOpenUnitRunKeepsTheNewestRuns(t *testing.T) {
	start := time.Date(2026, 11, 13, 17, 2, 36, 0, time.UTC)
	older := []string{"20261113T170000Z-11", "20261113T170100Z-12", "20261113T170200Z-13", "20261113T170300Z-14"}
	const created = "20261113T170236Z-4242"
	cases := []struct {
		existing int
		want     []string
	}{
		{1, []string{"20261113T170000Z-11", created}},
		{2, []string{"20261113T170000Z-11", "20261113T170100Z-12", created}},
		{3, []string{"20261113T170100Z-12", "20261113T170200Z-13", created}},
		{4, []string{"20261113T170200Z-13", created, "20261113T170300Z-14"}},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("%d older runs", tc.existing), func(t *testing.T) {
			base := t.TempDir()
			for _, name := range append(slices.Clone(older[:tc.existing]), "notes-dir") {
				if err := os.Mkdir(filepath.Join(base, name), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			dir, err := openUnitRun(base, 4242, start, 3)
			if err != nil || dir != filepath.Join(base, created) {
				t.Fatalf("openUnitRun = %q, %v; want %s", dir, err, filepath.Join(base, created))
			}
			want := append(append([]string{}, tc.want...), "latest", "notes-dir")
			slices.Sort(want)
			if got := entryNames(t, base); !reflect.DeepEqual(got, want) {
				t.Fatalf("entries of %s = %v, want %v", base, got, want)
			}
			if got, err := os.Readlink(filepath.Join(base, "latest")); err != nil || got != created {
				t.Fatalf("latest -> %q (%v), want %q", got, err, created)
			}
			if err := os.Remove(filepath.Join(base, "latest")); err != nil {
				t.Fatal(err)
			}
			if again, err := openUnitRun(base, 4242, start, 1); err != nil || again != dir {
				t.Fatalf("joining openUnitRun = %q, %v; want %q", again, err, dir)
			}
			want = slices.DeleteFunc(want, func(name string) bool { return name == "latest" })
			if got := entryNames(t, base); !reflect.DeepEqual(got, want) {
				t.Fatalf(
					"after a join, entries of %s = %v, want %v: a joining process prunes or links",
					base,
					got,
					want,
				)
			}
		})
	}
}

func TestUnitRunRow(t *testing.T) {
	cases := []struct {
		name    string
		summary map[string]any
		want    []string
	}{
		{
			"a red exit names its diagnosis",
			map[string]any{
				"package": "internal_store", "pid": 77, "event": "exit", "exit_code": 1, "wall_s": 2.5,
				"user_s": 1.25, "sys_s": 0.5, "maxrss_kb": int64(9000), "tests": 12, "bundles": []string{"exit"},
			},
			[]string{
				"internal_store",
				"77",
				"exit",
				"1",
				"2.5",
				"1.75",
				"9000",
				"12",
				"internal_store.77/exit/DIAGNOSIS.txt",
			},
		},
		{
			"a start without rusage or a test count",
			map[string]any{
				"package": "cmd_pfm", "pid": 78, "event": "started-no-exit-recorded", "exit_code": -1,
				"wall_s": 0.0, "rusage_error": "x", "tests": -1, "bundles": []string{},
			},
			[]string{"cmd_pfm", "78", "started-no-exit-recorded", "-1", "0", "-", "-", "-", "-"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := fmt.Sprintf("%s.%d", tc.summary["package"], tc.summary["pid"])
			if got := unitRunRow(dir, tc.summary); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("unitRunRow = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRecordUnitRunSerializesProcesses: concurrent writers each land one whole row under the one
// header, and summary.md is rewritten over them.
func TestRecordUnitRunSerializesProcesses(t *testing.T) {
	run := filepath.Join(t.TempDir(), "20261113T170236Z-4242")
	if err := os.Mkdir(run, 0o755); err != nil {
		t.Fatal(err)
	}
	const writers = 16
	var wg sync.WaitGroup
	for i := range writers {
		wg.Go(func() {
			row := []string{fmt.Sprintf("pkg%02d", i), fmt.Sprint(1000 + i), "exit", "0", "1", "1", "1", "1", "-"}
			if err := recordUnitRun(run, row); err != nil {
				t.Errorf("recordUnitRun %d: %v", i, err)
			}
		})
	}
	wg.Wait()
	data := readFile(t, filepath.Join(run, "packages.tsv"))
	lines := strings.Split(strings.TrimSuffix(data, "\n"), "\n")
	slices.Sort(lines[1:])
	want := []string{"package\tpid\tevent\texit_code\twall_s\tcpu_s\tmaxrss_kb\ttests\tdiagnosis"}
	for i := range writers {
		want = append(want, fmt.Sprintf("pkg%02d\t%d\texit\t0\t1\t1\t1\t1\t-", i, 1000+i))
	}
	if !reflect.DeepEqual(lines, want) {
		t.Fatalf("%s/packages.tsv = %q, want %q", run, lines, want)
	}
	head := "# Unit run profile\n\n- run: 20261113T170236Z-4242 · package processes: 16 · passed 16 · failed 0 · near timeout 0 · without an exit 0\n"
	if got := readFile(t, filepath.Join(run, "summary.md")); !strings.HasPrefix(got, head) {
		t.Fatalf("%s/summary.md opens %q, want %q", run, got[:min(len(got), len(head))], head)
	}
	if got := entryNames(t, run); !reflect.DeepEqual(got, []string{"packages.tsv", "summary.md"}) {
		t.Fatalf("entries of %s = %v, want packages.tsv and summary.md alone", run, got)
	}
}

func TestTestCountWithoutAMainIsUnknown(t *testing.T) {
	if got := testCount(nil); got != -1 {
		t.Fatalf("testCount(nil) = %d, want -1", got)
	}
}

// entryNames lists dir's entries by name, sorted.
func entryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	names := []string{}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// TestProcessStartOfThisProcessIsBeforeNow reads this very process on either kernel: started no
// later than now and within the hour a test binary lives.
func TestProcessStartOfThisProcessIsBeforeNow(t *testing.T) {
	got, err := processStart(os.Getpid())
	if err != nil {
		t.Fatalf("processStart(own pid): %v", err)
	}
	if now := time.Now(); got.After(now) || got.Before(now.Add(-time.Hour)) {
		t.Fatalf("processStart(own pid) = %v, want within the hour before %v", got, now)
	}
}
