package harness

import (
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

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
