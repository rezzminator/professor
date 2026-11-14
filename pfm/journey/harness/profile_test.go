package harness

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

func TestRunWritesEachStepsTimesAndEachWait(t *testing.T) {
	dir := t.TempDir()
	fake := clock.NewFake(time.Unix(1_700_000_000, 0))
	advance := func(d time.Duration) func(*Ctx, int) int {
		return func(_ *Ctx, in int) int { fake.Advance(d); return in }
	}
	waits := func(c *Ctx, in int) int {
		fake.Advance(3 * time.Second)
		c.Poll("ready", 5*time.Second, func() (bool, string) { fake.Advance(500 * time.Millisecond); return true, "" })
		return in
	}
	// A together node's branch runs as a parallel subtest: its wall ends with its own subtree too.
	Run(t, Do("root", advance(time.Second), Do("a", waits),
		Together("b", advance(2*time.Second), Do("c", advance(time.Second)))), 0,
		Hooks{Artifacts: dir, Clock: fake})
	for name, want := range map[string]string{
		"steps.tsv": "path\tverdict\twall_s\town_s\n" +
			"root/a\tPASS\t3.500\t3.500\nroot/b\tPASS\t3.000\t2.000\nroot\tPASS\t7.500\t1.000\n" +
			"root/b/c\tPASS\t1.000\t1.000\n",
		"waits.tsv": "path\twait\tseconds\tmet\nroot/a\tready\t0.500\tyes\n",
	} {
		file := filepath.Join(dir, name)
		got, err := os.ReadFile(file)
		if err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want %q", file, got, err, want)
		}
	}
}

func TestWriteSummaryIsOneScreenOfTheRun(t *testing.T) {
	collected := Coverage{
		Total: "41.2%",
		Packages: []PackageCover{
			{"p1", "90.0%"},
			{"p2", "80.0%"},
			{"p3", "70.0%"},
			{"p4", "60.0%"},
			{"p5", "50.0%"},
			{"p6", "40.0%"},
			{"p7", "0.0%"},
		},
		Writers: []Writer{
			{"pfm w01", 12},
			{"pfm w02", 11},
			{"pfm w03", 10},
			{"pfm w04", 9},
			{"pfm w05", 8},
			{"pfm w06", 7},
			{"pfm w07", 6},
			{"pfm w08", 5},
			{"pfm w09", 4},
			{"pfm w10", 3},
			{"pfm w11", 2},
			{"pfm w12", 1},
		},
	}
	for _, row := range []struct {
		name     string
		coverage Coverage
		want     string
	}{
		{"coverage collected", collected, `# Journey run profile

- wall: 75.3 s · steps: 9 passed, 1 failed, 1 blocked
- machine: 120.5 CPU s, peak memory 512.0 MiB (the journey machine's cgroup)
- coverage: 41.2% of pfm's statements, 7 packages (coverage.html, coverage-packages.tsv, coverage-funcs.txt)

## Slowest steps (own time, without children)

| own s | wall s | result | step |
|---|---|---|---|
| 11.000 | 20.000 | PASS | root/s01 |
| 10.000 | 19.000 | PASS | root/s02 |
| 9.000 | 18.000 | FAIL | root/s03 |
| 8.000 | 17.000 | PASS | root/s04 |
| 7.000 | 16.000 | PASS | root/s05 |
| 6.000 | 15.000 | PASS | root/s06 |
| 5.000 | 14.000 | PASS | root/s07 |
| 4.000 | 13.000 | PASS | root/s08 |
| 3.000 | 12.000 | PASS | root/s09 |
| 2.000 | 11.000 | PASS | root/s10 |

## Slowest waits

| s | met | step | wait |
|---|---|---|---|
| 4.000 | no | root/a | terminal plain: "x\|y" on the screen |
| 1.000 | yes | root/b | ready |

## Coverage by package (first and last 3 of 7)

| % | package |
|---|---|
| 90.0% | p1 |
| 80.0% | p2 |
| 70.0% | p3 |
| … | … |
| 50.0% | p5 |
| 40.0% | p6 |
| 0.0% | p7 |

## pfm processes that wrote counters: 78

| processes | argv |
|---|---|
| 12 | pfm w01 |
| 11 | pfm w02 |
| 10 | pfm w03 |
| 9 | pfm w04 |
| 8 | pfm w05 |
| 7 | pfm w06 |
| 6 | pfm w07 |
| 5 | pfm w08 |
| 4 | pfm w09 |
| 3 | pfm w10 |
| 3 | (2 more argv) |

A process killed by SIGKILL writes no counters, so every coverage figure is a lower bound.
`},
		{"coverage not collected", Coverage{Missing: "GOCOVERDIR is unset"}, `# Journey run profile

- wall: 75.3 s · steps: 9 passed, 1 failed, 1 blocked
- machine: 120.5 CPU s, peak memory 512.0 MiB (the journey machine's cgroup)
- coverage: not collected: GOCOVERDIR is unset

## Slowest steps (own time, without children)

| own s | wall s | result | step |
|---|---|---|---|
| 11.000 | 20.000 | PASS | root/s01 |
| 10.000 | 19.000 | PASS | root/s02 |
| 9.000 | 18.000 | FAIL | root/s03 |
| 8.000 | 17.000 | PASS | root/s04 |
| 7.000 | 16.000 | PASS | root/s05 |
| 6.000 | 15.000 | PASS | root/s06 |
| 5.000 | 14.000 | PASS | root/s07 |
| 4.000 | 13.000 | PASS | root/s08 |
| 3.000 | 12.000 | PASS | root/s09 |
| 2.000 | 11.000 | PASS | root/s10 |

## Slowest waits

| s | met | step | wait |
|---|---|---|---|
| 4.000 | no | root/a | terminal plain: "x\|y" on the screen |
| 1.000 | yes | root/b | ready |
`},
	} {
		t.Run(row.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, text := range map[string]string{"steps.tsv": `path	verdict	wall_s	own_s
root/s01	PASS	20.000	11.000
root/s02	PASS	19.000	10.000
root/s03	FAIL	18.000	9.000
root/s04	PASS	17.000	8.000
root/s05	PASS	16.000	7.000
root/s06	PASS	15.000	6.000
root/s07	PASS	14.000	5.000
root/s08	PASS	13.000	4.000
root/s09	PASS	12.000	3.000
root/s10	PASS	11.000	2.000
root/s11	BLOCKED	0.000	0.000
`, "waits.tsv": `path	wait	seconds	met
root/a	terminal plain: "x|y" on the screen	4.000	no
root/b	ready	1.000	yes
`} {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			summary := Summary{
				Wall: 75300 * time.Millisecond,
				Machine: Machine{
					CPU:   120500 * time.Millisecond,
					Peak:  512 << 20,
					Scope: "the journey machine's cgroup",
				},
				Coverage: row.coverage,
			}
			if err := WriteSummary(dir, summary); err != nil {
				t.Fatalf("WriteSummary: %v", err)
			}
			file := filepath.Join(dir, "summary.md")
			if got, err := os.ReadFile(file); err != nil || string(got) != row.want {
				t.Errorf("%s (%v) =\n%s\nwant\n%s", file, err, got, row.want)
			}
		})
	}
}
