package harness

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/naming"
)

// WaitTime is one named wait the run made (Poll, WaitScreen and every wait built on them): the step that
// waited, what it waited for, how long, and whether the condition held before the bound.
type WaitTime struct {
	Path, What string
	Took       time.Duration
	Met        bool
}

// Machine is what the run cost the machine: CPU time, peak memory, and the scope they were read over.
type Machine struct {
	CPU   time.Duration
	Peak  int64 // bytes
	Scope string
}

// Summary is everything summary.md carries beyond steps.tsv and waits.tsv.
type Summary struct {
	Wall     time.Duration
	Machine  Machine
	Coverage Coverage
}

// summaryRows is how many of the slowest steps and waits summary.md lists; packageEnds how many of the
// package table's first and last lines it shows; writerRows how many argv kinds of counter writers.
const (
	summaryRows = 10
	packageEnds = 3
	writerRows  = 10
)

// writeTimes writes steps.tsv (each step's path, verdict, wall time with its subtree and its own time,
// slowest own time first) and waits.tsv (each wait, slowest first) into dir.
func writeTimes(dir string, records []record, waits []WaitTime) error {
	steps := append([]record(nil), records...)
	sort.SliceStable(steps, func(i, j int) bool {
		if steps[i].own != steps[j].own {
			return steps[i].own > steps[j].own
		}
		return steps[i].path < steps[j].path
	})
	var st strings.Builder
	st.WriteString("path\tverdict\twall_s\town_s\n")
	for _, r := range steps {
		fmt.Fprintf(&st, "%s\t%s\t%.3f\t%.3f\n", r.path, r.verdict, r.wall, r.own)
	}
	ordered := append([]WaitTime(nil), waits...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Took != ordered[j].Took {
			return ordered[i].Took > ordered[j].Took
		}
		return ordered[i].Path < ordered[j].Path
	})
	var wt strings.Builder
	wt.WriteString("path\twait\tseconds\tmet\n")
	for _, w := range ordered {
		fmt.Fprintf(
			&wt,
			"%s\t%s\t%.3f\t%s\n",
			w.Path,
			naming.SquashTabsAndNewlines(w.What),
			w.Took.Seconds(),
			yesNo(w.Met),
		)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("profile: create %s: %w", dir, err)
	}
	for name, text := range map[string]string{"steps.tsv": st.String(), "waits.tsv": wt.String()} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			return fmt.Errorf("profile: write %s: %w", name, err)
		}
	}
	return nil
}

// WriteSummary reads steps.tsv and waits.tsv in dir and writes summary.md beside them: one screen
// with the run's wall time, its step counts, the slowest steps and waits, the machine's cost and the
// coverage of pfm's source.
func WriteSummary(dir string, s Summary) error {
	steps, err := readTSV(filepath.Join(dir, "steps.tsv"), 4)
	if err != nil {
		return err
	}
	waits, err := readTSV(filepath.Join(dir, "waits.tsv"), 4)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, row := range steps {
		counts[row[1]]++
	}
	var b strings.Builder
	b.WriteString("# Journey run profile\n\n")
	fmt.Fprintf(&b, "- wall: %.1f s · steps: %d passed, %d failed, %d blocked\n",
		s.Wall.Seconds(), counts["PASS"], counts["FAIL"], counts["BLOCKED"])
	fmt.Fprintf(&b, "- machine: %.1f CPU s, peak memory %.1f MiB (%s)\n",
		s.Machine.CPU.Seconds(), float64(s.Machine.Peak)/(1<<20), s.Machine.Scope)
	c := s.Coverage
	if c.Missing != "" {
		fmt.Fprintf(&b, "- coverage: not collected: %s\n", c.Missing)
	} else {
		fmt.Fprintf(&b, "- coverage: %s of pfm's statements, %d packages (coverage.html, coverage-packages.tsv, "+
			"coverage-funcs.txt)\n", c.Total, len(c.Packages))
	}
	b.WriteString(
		"\n## Slowest steps (own time, without children)\n\n| own s | wall s | result | step |\n|---|---|---|---|\n",
	)
	for _, row := range head(steps, summaryRows) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", row[3], row[2], row[1], row[0])
	}
	b.WriteString("\n## Slowest waits\n\n| s | met | step | wait |\n|---|---|---|---|\n")
	for _, row := range head(waits, summaryRows) {
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", row[2], row[3], row[0], strings.ReplaceAll(row[1], "|", `\|`))
	}
	if c.Missing == "" {
		writeCoverage(&b, c)
	}
	return os.WriteFile(filepath.Join(dir, "summary.md"), []byte(b.String()), 0o644)
}

// writeCoverage is summary.md's coverage part: the package table's ends, the processes that wrote
// counters by their first two argv words, and the footnote on what no counter reaches.
func writeCoverage(b *strings.Builder, c Coverage) {
	fmt.Fprintf(b, "\n## Coverage by package (first and last %d of %d)\n\n| %% | package |\n|---|---|\n",
		packageEnds, len(c.Packages))
	for i, p := range c.Packages {
		if i == packageEnds && len(c.Packages) > 2*packageEnds {
			b.WriteString("| … | … |\n")
		}
		if i < packageEnds || i >= len(c.Packages)-packageEnds {
			fmt.Fprintf(b, "| %s | %s |\n", p.Percent, p.Package)
		}
	}
	total := 0
	for _, w := range c.Writers {
		total += w.Processes
	}
	fmt.Fprintf(b, "\n## pfm processes that wrote counters: %d\n\n| processes | argv |\n|---|---|\n", total)
	for i, w := range c.Writers {
		if i == writerRows {
			fmt.Fprintf(b, "| %d | (%d more argv) |\n", total-sumWriters(c.Writers[:i]), len(c.Writers)-i)
			break
		}
		fmt.Fprintf(b, "| %d | %s |\n", w.Processes, w.Argv)
	}
	b.WriteString("\nA process killed by SIGKILL writes no counters, so every coverage figure is a lower bound.\n")
}

func sumWriters(writers []Writer) int {
	n := 0
	for _, w := range writers {
		n += w.Processes
	}
	return n
}

// readTSV reads a profile table, header dropped, every row exactly width fields.
func readTSV(file string, width int) ([][]string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("profile: read %s: %w", file, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	var rows [][]string
	for n, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) != width {
			return nil, fmt.Errorf(
				"profile: %s line %d has %d fields, want %d: %q",
				file,
				n+2,
				len(fields),
				width,
				line,
			)
		}
		rows = append(rows, fields)
	}
	return rows, nil
}

func head(rows [][]string, n int) [][]string {
	if len(rows) > n {
		return rows[:n]
	}
	return rows
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
