package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// Coverage is the line coverage of an instrumented binary's run, merged from every process's counters.
type Coverage struct {
	Missing  string // why no coverage was collected; empty when it was
	Total    string // statement coverage of every reached package, "41.2%"
	Packages []PackageCover
	Writers  []Writer
}

// PackageCover is one package's line of coverage-packages.tsv.
type PackageCover struct{ Package, Percent string }

// Writer is how many processes that wrote counters started with the same first two argv words.
type Writer struct {
	Argv      string
	Processes int
}

var (
	packageLine = regexp.MustCompile(`^\s*(\S+)\s+coverage: ([0-9.]+%) of statements$`)
	funcLine    = regexp.MustCompile(`\t([0-9.]+)%$`)
	totalLine   = regexp.MustCompile(`^total:\s+\(statements\)\s+([0-9.]+%)$`)
	argsLine    = regexp.MustCompile(`^data file \S+ .*program args: \[(.*)\]$`)
)

// noPackage is a package pattern that matches nothing: the debug dump then lists only its data files.
const noPackage = "journey.invalid/none"

// Cover merges the counters every instrumented process wrote under raw, restricted to the packages
// pattern names, and writes coverage.out, coverage.html, coverage-packages.tsv and coverage-funcs.txt
// into dir; moduleDir is the module whose source coverage.html shows. It runs Go's own tools.
func Cover(dir, raw, moduleDir, pattern string) (Coverage, error) {
	percent, err := goTool(moduleDir, "covdata", "percent", "-i", raw, "-pkg", pattern)
	if err != nil {
		return Coverage{}, err
	}
	profile := filepath.Join(dir, "coverage.out")
	if _, err := goTool(moduleDir, "covdata", "textfmt", "-i", raw, "-pkg", pattern, "-o", profile); err != nil {
		return Coverage{}, err
	}
	funcs, err := goTool(moduleDir, "cover", "-func", profile)
	if err != nil {
		return Coverage{}, err
	}
	if _, err := goTool(moduleDir, "cover", "-html", profile, "-o", filepath.Join(dir, "coverage.html")); err != nil {
		return Coverage{}, err
	}
	dump, err := goTool(moduleDir, "covdata", "debugdump", "-i", raw, "-pkg", noPackage)
	if err != nil {
		return Coverage{}, err
	}
	c := Coverage{Packages: parsePackages(percent), Writers: parseWriters(dump)}
	sortedFuncs, total, err := parseFuncs(funcs)
	if err != nil {
		return Coverage{}, err
	}
	c.Total = total
	if len(c.Packages) == 0 || len(c.Writers) == 0 {
		return Coverage{}, fmt.Errorf(
			"cover: no package or no counter file under %s for %s:\n%s",
			raw,
			pattern,
			percent,
		)
	}
	var tsv strings.Builder
	tsv.WriteString("package\tpercent\n")
	for _, p := range c.Packages {
		fmt.Fprintf(&tsv, "%s\t%s\n", p.Package, p.Percent)
	}
	for name, text := range map[string]string{"coverage-packages.tsv": tsv.String(), "coverage-funcs.txt": sortedFuncs} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			return Coverage{}, fmt.Errorf("cover: write %s: %w", name, err)
		}
	}
	return c, nil
}

// goTool runs `go tool {args}` in moduleDir and returns its stdout.
func goTool(moduleDir string, args ...string) (string, error) {
	argv := append([]string{"go", "tool"}, args...)
	result, err := deps.RealRunner{}.Run(context.Background(), argv, deps.RunOptions{Dir: moduleDir})
	if err != nil {
		return "", fmt.Errorf("cover: %s: %w", strings.Join(argv, " "), err)
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("cover: %s: exit %d: %s", strings.Join(argv, " "), result.ExitCode, result.Stderr)
	}
	return string(result.Stdout), nil
}

// parsePackages reads `go tool covdata percent`: highest coverage first, then by package.
func parsePackages(out string) []PackageCover {
	var packages []PackageCover
	for _, line := range strings.Split(out, "\n") {
		if m := packageLine.FindStringSubmatch(line); m != nil {
			packages = append(packages, PackageCover{m[1], m[2]})
		}
	}
	sort.SliceStable(packages, func(i, j int) bool {
		a, b := percentOf(packages[i].Percent), percentOf(packages[j].Percent)
		if a != b {
			return a > b
		}
		return packages[i].Package < packages[j].Package
	})
	return packages
}

// parseFuncs reorders `go tool cover -func`: the least covered functions first (unreached lead), the
// tool's order among equals, the total line last; it returns that text and the total.
func parseFuncs(out string) (sorted, total string, err error) {
	var funcs []string
	for _, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if m := totalLine.FindStringSubmatch(line); m != nil {
			total = m[1]
			continue
		}
		if funcLine.MatchString(line) {
			funcs = append(funcs, line)
		}
	}
	if total == "" {
		return "", "", fmt.Errorf("cover: go tool cover -func printed no total line:\n%s", out)
	}
	sort.SliceStable(funcs, func(i, j int) bool { return funcPercent(funcs[i]) < funcPercent(funcs[j]) })
	return strings.Join(funcs, "\n") + "\ntotal:\t(statements)\t" + total + "\n", total, nil
}

// parseWriters counts the counter files of `go tool covdata debugdump` by their program's first two
// argv words, the binary's base name first; most processes first, then by argv.
func parseWriters(out string) []Writer {
	counts := map[string]int{}
	for _, line := range strings.Split(out, "\n") {
		m := argsLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		words := strings.Fields(m[1])
		if len(words) == 0 {
			continue
		}
		key := filepath.Base(words[0])
		if len(words) > 1 {
			key += " " + words[1]
		}
		counts[key]++
	}
	writers := make([]Writer, 0, len(counts))
	for argv, n := range counts {
		writers = append(writers, Writer{argv, n})
	}
	sort.Slice(writers, func(i, j int) bool {
		if writers[i].Processes != writers[j].Processes {
			return writers[i].Processes > writers[j].Processes
		}
		return writers[i].Argv < writers[j].Argv
	})
	return writers
}

func funcPercent(line string) float64 { return percentOf(funcLine.FindStringSubmatch(line)[1] + "%") }

func percentOf(text string) float64 {
	value, err := strconv.ParseFloat(strings.TrimSuffix(text, "%"), 64)
	if err != nil {
		return -1 // never printed by the tools this reads: their lines matched a number
	}
	return value
}
