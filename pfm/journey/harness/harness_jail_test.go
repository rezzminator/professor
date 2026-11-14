//go:build jail

package harness

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// childEnv names, in a re-exec of this test binary, the one test whose body runs there for real.
const childEnv = "HARNESS_CHILD"

// inChild runs body in a re-exec of this test binary limited to t, whose tree is meant to fail; the
// parent gets the child's whole output and whether it exited non-zero. In the child, body runs and
// inChild reports parent=false so the caller returns.
func inChild(t *testing.T, body func(t *testing.T)) (out string, failed, parent bool) {
	t.Helper()
	if os.Getenv(childEnv) == t.Name() {
		body(t)
		return "", false, false
	}
	parts := strings.Split(t.Name(), "/")
	for i, part := range parts {
		parts[i] = "^" + regexp.QuoteMeta(part) + "$"
	}
	cmd := exec.Command(os.Args[0], "-test.run="+strings.Join(parts, "/"), "-test.v", "-test.parallel=4")
	cmd.Env = append(os.Environ(), childEnv+"="+t.Name())
	output, err := cmd.CombinedOutput()
	if _, exited := err.(*exec.ExitError); err != nil && !exited {
		t.Fatalf("re-exec %s: %v", t.Name(), err)
	}
	return testjail.ChildOutput(output), err != nil, true
}

// lineAfter is the text of the first output line holding marker, from marker on.
func lineAfter(out, marker string) string {
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, marker); i >= 0 {
			return line[i:]
		}
	}
	return ""
}

func TestFailedStepBlocksOnlyItsDescendants(t *testing.T) {
	tr := &trail{}
	out, failed, parent := inChild(t, func(t *testing.T) {
		bad := func(c *Ctx, in string) string { c.Failf("the screen never showed %q", "ready"); return in }
		root := Do("root", tr.step("r"),
			Together("day", nil,
				Do("bad", bad, Do("x", tr.step("x"), Do("y", tr.step("y")))),
				Do("good", tr.step("g"), Do("kid", tr.step("k"))),
			),
			Do("after", tr.step("a")),
		)
		Run(t, root, "", Hooks{})
	})
	if !parent {
		return
	}
	for marker, want := range map[string]string{
		"BLOCKED":       "BLOCKED 2 steps under root/day/bad: root/day/bad/x, root/day/bad/x/y",
		"PASS 5":        "PASS 5 · FAIL 1 · BLOCKED 2",
		"root/day/bad:": "root/day/bad: the screen never showed \"ready\"",
		"--- PASS: TestFailedStepBlocksOnlyItsDescendants/root/day/good/kid": "--- PASS: TestFailedStepBlocksOnlyItsDescendants/root/day/good/kid ({d})",
		"--- PASS: TestFailedStepBlocksOnlyItsDescendants/root/after":        "--- PASS: TestFailedStepBlocksOnlyItsDescendants/root/after ({d})",
	} {
		if got := testDuration.ReplaceAllString(lineAfter(out, marker), "{d}"); got != want {
			t.Errorf("line = %q, want %q\n%s", got, want, out)
		}
	}
	if !failed || strings.Contains(out, "root/day/bad/x<") {
		t.Fatalf("child failed=%v, want true and no step under root/day/bad run:\n%s", failed, out)
	}
}

func TestBudgetFailsOnlyARunOverIt(t *testing.T) {
	for _, row := range []struct {
		name    string
		elapsed time.Duration
		want    string // the budget line, empty when the run passes
	}{
		{"under", 9999 * time.Millisecond, ""},
		{"at the bound", 10 * time.Second, ""},
		{"over", 10001 * time.Millisecond, "budget: the tree took 10.0s, allowed 10s"},
	} {
		t.Run(row.name, func(t *testing.T) {
			out, failed, parent := inChild(t, func(t *testing.T) {
				fake := clock.NewFake(time.Unix(1_700_000_000, 0))
				step := func(_ *Ctx, in int) int { fake.Advance(row.elapsed); return in }
				Run(t, Do("root", step), 0, Hooks{Budget: 10 * time.Second, Clock: fake})
			})
			if !parent {
				return
			}
			if got := lineAfter(out, "budget:"); got != row.want || failed != (row.want != "") {
				t.Fatalf("budget line %q failed=%v, want %q failed=%v\n%s", got, failed, row.want, row.want != "", out)
			}
		})
	}
}

// testDuration matches a go test duration, which differs per run.
var testDuration = regexp.MustCompile(`\d+\.\d+s`)
