package harness

import (
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

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
	return string(output), err != nil, true
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

// trail records which step ran with which input state.
type trail struct {
	mu   sync.Mutex
	seen []string
}

func (tr *trail) step(tag string) func(*Ctx, string) string {
	return func(c *Ctx, in string) string {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		tr.seen = append(tr.seen, c.Path()+"<"+in)
		return in + tag
	}
}

func TestSerialChildrenRunInOrderEachFromItsParentsOutput(t *testing.T) {
	tr := &trail{}
	root := Do("root", tr.step("r"),
		Do("one", tr.step("1"), Do("deep", tr.step("d"))),
		Do("two", tr.step("2")),
	)
	Run(t, root, "", Hooks{})
	want := []string{"root<", "root/one<r", "root/one/deep<r1", "root/two<r"}
	if !reflect.DeepEqual(tr.seen, want) {
		t.Fatalf("steps ran as %q, want %q", tr.seen, want)
	}
}

func TestTogetherBranchesOverlapAndTheNextSiblingWaitsForAll(t *testing.T) {
	tr := &trail{}
	var mu sync.Mutex
	arrived := 0
	latch := func(c *Ctx, in string) string {
		mu.Lock()
		arrived++
		mu.Unlock()
		c.Poll("both branches inside their step at once", 5*time.Second, func() (bool, string) {
			mu.Lock()
			defer mu.Unlock()
			return arrived == 2, strings.Repeat("arrived ", arrived)
		})
		return tr.step("")(c, in)
	}
	root := Do("root", nil,
		Together("day", tr.step("d"), Do("a", latch), Do("b", latch)),
		Do("after", tr.step("x")),
	)
	Run(t, root, "s", Hooks{})
	if len(tr.seen) != 4 || tr.seen[0] != "root/day<s" || tr.seen[3] != "root/after<s" {
		t.Fatalf("steps ran as %q, want root/day<s first, both branches, root/after<s last", tr.seen)
	}
	branches := []string{tr.seen[1], tr.seen[2]}
	if order := branches[0] + "," + branches[1]; order != "root/day/a<sd,root/day/b<sd" &&
		order != "root/day/b<sd,root/day/a<sd" {
		t.Fatalf("branches ran as %q, want root/day/a<sd and root/day/b<sd", branches)
	}
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
		"root/day/bad:": `root/day/bad: the screen never showed "ready"`,
		"--- PASS: TestFailedStepBlocksOnlyItsDescendants/root/day/good/kid": "--- PASS: TestFailedStepBlocksOnlyItsDescendants/root/day/good/kid",
		"--- PASS: TestFailedStepBlocksOnlyItsDescendants/root/after":        "--- PASS: TestFailedStepBlocksOnlyItsDescendants/root/after",
	} {
		if got := lineAfter(out, marker); !strings.HasPrefix(got, want) {
			t.Errorf("line %q, want %q\n%s", got, want, out)
		}
	}
	if !failed || strings.Contains(out, "root/day/bad/x<") {
		t.Fatalf("child failed=%v, want true and no step under root/day/bad run:\n%s", failed, out)
	}
}

func TestValidateRefusesABadName(t *testing.T) {
	step := func(_ *Ctx, in int) int { return in }
	for _, row := range []struct {
		name string
		tree Node[int]
		want string
	}{
		{"upper case", Do("root", step, Do("Bad", step)), `node "Bad" at root/Bad: a name is [a-z0-9-]+`},
		{"slash", Do("root", step, Do("a/b", step)), `node "a/b" at root/a/b: a name is [a-z0-9-]+`},
		{"empty", Do("", step), `node "" at : a name is [a-z0-9-]+`},
		{"twin siblings", Do("root", step, Do("x", step), Do("x", step)), `node "x" under root: two siblings share the name`},
		{"twins deeper", Do("root", step, Do("a", step, Do("x", step), Do("x", step))), `node "x" under root/a: two siblings share the name`},
		{"cousins may share", Do("root", step, Do("a", step, Do("x", step)), Do("b", step, Do("x", step))), ""},
	} {
		t.Run(row.name, func(t *testing.T) {
			got := ""
			if err := validate(row.tree, row.tree.Name); err != nil {
				got = err.Error()
			}
			if got != row.want {
				t.Fatalf("validate = %q, want %q", got, row.want)
			}
		})
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
