package testjail

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

// A plain `go test` sets no PFM_TEST_ARTIFACT_DIR: every package process it starts then profiles
// into one run directory, {system temp}/{project}/unit/{start}-{pid} of the go process that started
// them all, as the journey keeps its runs under /tmp/{project}/journey. The run holds each process's
// directory, packages.tsv (a row per process event) and summary.md over those rows; the base links
// the newest run as latest and keeps unitRunsKept runs. No flight recorder runs there, nor in a
// helper its processes start: it is the one part of the profiler that costs measurable CPU, so its
// trace window stays the gate's.

const (
	unitRunPurpose = "unit"
	// unitRunsKept bounds the base: the process creating a run removes every older run past it.
	unitRunsKept   = 20
	unitRunRows    = "packages.tsv"
	unitRunSummary = "summary.md"
	unitRunLatest  = "latest"
	unitRunStamp   = "20060102T150405Z"
)

// unitRunName is a run directory's name: the go process's start, UTC to the second, then its pid.
var unitRunName = regexp.MustCompile(`^\d{8}T\d{6}Z-\d+$`)

// unitRunColumns heads packages.tsv.
var unitRunColumns = []string{
	"package",
	"pid",
	"event",
	"exit_code",
	"wall_s",
	"cpu_s",
	"maxrss_kb",
	"tests",
	"diagnosis",
}

// errRecorderGateOnly is why a default run's bundle holds no trace window.
var errRecorderGateOnly = errors.New("the flight recorder runs only under " + paths.EnvTestArtifactDir +
	" (the gate); a plain go test keeps no trace window")

// defaultUnitRun is the run directory this process records into when root (PFM_TEST_ARTIFACT_DIR)
// is empty, exported as PFM_TEST_ARTIFACT_DIR so a helper this process starts lands beside it; ""
// when root is set.
func defaultUnitRun(root, moduleDir string) (string, error) {
	if root != "" {
		return "", nil
	}
	base, err := unitRunBase(paths.SystemTempDir(), moduleDir)
	if err != nil {
		return "", err
	}
	parent := os.Getppid()
	start, err := processStart(parent)
	if err != nil {
		return "", fmt.Errorf("unit run: the start of parent process %d: %w", parent, err)
	}
	run, err := openUnitRun(base, parent, start, unitRunsKept)
	if err != nil {
		return "", err
	}
	if err := os.Setenv(paths.EnvTestArtifactDir, run); err != nil {
		return "", fmt.Errorf("unit run: export %s=%s: %w", paths.EnvTestArtifactDir, run, err)
	}
	return run, nil
}

// unitRunBase is {tempDir}/{project}/unit, {project} the name of the directory holding the module
// root minus a leading dot, as scripts/unit-proof.sh names it.
func unitRunBase(tempDir, moduleDir string) (string, error) {
	if moduleDir == "" {
		return "", errors.New("unit run: no go.mod above the launch directory names the project")
	}
	project := strings.TrimPrefix(filepath.Base(filepath.Dir(moduleDir)), ".")
	return filepath.Join(tempDir, project, unitRunPurpose), nil
}

// openUnitRun creates, or joins, the run directory under base of the go process parent started at
// start. The process that creates it points latest at it and removes the runs older than the newest
// keep; a failure of either is a warning, the run itself stands.
func openUnitRun(base string, parent int, start time.Time, keep int) (string, error) {
	name := fmt.Sprintf("%s-%d", start.UTC().Format(unitRunStamp), parent)
	dir := filepath.Join(base, name)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return "", fmt.Errorf("unit run: create %s: %w", base, err)
	}
	if err := os.Mkdir(dir, 0o755); errors.Is(err, os.ErrExist) {
		return dir, nil
	} else if err != nil {
		return "", fmt.Errorf("unit run: create %s: %w", dir, err)
	}
	link := filepath.Join(base, fmt.Sprintf(".%s-%d", unitRunLatest, os.Getpid()))
	if err := os.Symlink(name, link); err != nil {
		warnSetup("unit run: link %s: %v", link, err)
	} else if err := os.Rename(link, filepath.Join(base, unitRunLatest)); err != nil {
		warnSetup("unit run: point %s at %s: %v", unitRunLatest, name, err)
		if err := os.Remove(link); err != nil {
			warnSetup("unit run: remove %s: %v", link, err)
		}
	}
	if err := pruneUnitRuns(base, keep); err != nil {
		warnSetup("unit run: prune %s: %v", base, err)
	}
	return dir, nil
}

// pruneUnitRuns removes every run directory under base but the newest keep; any other entry stays.
func pruneUnitRuns(base string, keep int) error {
	entries, err := os.ReadDir(base)
	if err != nil {
		return err
	}
	var runs []string
	for _, e := range entries {
		if e.IsDir() && unitRunName.MatchString(e.Name()) {
			runs = append(runs, e.Name())
		}
	}
	var errs []error
	for _, name := range runs[:max(len(runs)-keep, 0)] {
		errs = append(errs, os.RemoveAll(filepath.Join(base, name)))
	}
	return errors.Join(errs...)
}

// inUnitRun reports whether root is a default run directory, {base}/unit/{start}-{pid}: a package
// process of a plain go test, or a helper it started, which inherits the run as its root.
func inUnitRun(root string) bool {
	return filepath.Base(filepath.Dir(root)) == unitRunPurpose && unitRunName.MatchString(filepath.Base(root))
}

// testCount is how many top-level tests m holds, read off testing.M's unexported tests slice (the
// testing package exports no count); -1 when that field is gone.
func testCount(m *testing.M) int {
	if m == nil {
		return -1
	}
	f := reflect.ValueOf(m).Elem().FieldByName("tests")
	if !f.IsValid() || f.Kind() != reflect.Slice {
		return -1
	}
	return f.Len()
}

// unitRunRow is the packages.tsv row of the process whose directory is named dirName, from its
// summary s; a counter the summary lacks is "-".
func unitRunRow(dirName string, s map[string]any) []string {
	cell := func(key string) string {
		if v, ok := s[key]; ok && v != nil && v != -1 {
			return fmt.Sprint(v)
		}
		return "-"
	}
	cpu := "-"
	user, uok := s["user_s"].(float64)
	sys, sok := s["sys_s"].(float64)
	if uok && sok {
		cpu = strconv.FormatFloat(round3(user+sys), 'f', -1, 64)
	}
	diagnosis := []string{}
	bundles, _ := s["bundles"].([]string)
	for _, reason := range bundles {
		diagnosis = append(diagnosis, filepath.Join(dirName, reason, "DIAGNOSIS.txt"))
	}
	if len(diagnosis) == 0 {
		diagnosis = []string{"-"}
	}
	return []string{
		cell("package"), cell("pid"), cell("event"), fmt.Sprint(s["exit_code"]), cell("wall_s"), cpu,
		cell("maxrss_kb"), cell("tests"), strings.Join(diagnosis, ","),
	}
}

// recordUnitRun appends row to the run's packages.tsv and rewrites summary.md from every row, under
// an exclusive lock on packages.tsv, so the package processes of one run never interleave.
func recordUnitRun(run string, row []string) error {
	path := filepath.Join(run, unitRunRows)
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		if err := f.Close(); err != nil {
			warnSetup("unit run: close %s: %v", path, err)
		}
	}()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:errcheck // closing the file releases it too
	text := strings.Join(row, "\t") + "\n"
	if info, err := f.Stat(); err != nil {
		return fmt.Errorf("stat %s: %w", path, err)
	} else if info.Size() == 0 {
		text = strings.Join(unitRunColumns, "\t") + "\n" + text
	}
	if _, err := f.WriteString(text); err != nil {
		return fmt.Errorf("append to %s: %w", path, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	summary := filepath.Join(run, unitRunSummary)
	tmp := fmt.Sprintf("%s.%d", summary, os.Getpid())
	if err := os.WriteFile(tmp, []byte(renderUnitRun(filepath.Base(run), string(data))), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, summary)
}
