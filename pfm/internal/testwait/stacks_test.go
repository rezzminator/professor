package testwait_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testwait"
)

const stacksPackage = "github.com/rezzminator/professor/pfm/internal/testwait_test."

// stacksBlocked parks on release at line 16 of this file, the frame the dump must name.
//
//go:noinline
func stacksBlocked(release <-chan struct{}) { <-release }

// stacksStart starts f from a named function, so the go statement the dump prints is a literal.
//
//go:noinline
func stacksStart(f func()) { go f() }

func TestGoroutinesParsesEachGoroutineWithItsCreatorAndFrames(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	stacksStart(func() { stacksBlocked(release) })
	testwait.Until(t, "the started goroutine blocked in stacksBlocked", func() (bool, string) {
		return testwait.Blocked("testwait_test.stacksBlocked")
	})
	self := testwait.Goroutines(false)
	test := stacksPackage + "TestGoroutinesParsesEachGoroutineWithItsCreatorAndFrames"
	if len(self) != 1 || len(self[0].Frames) < 2 || self[0].Frames[1].Function != test {
		t.Fatalf("Goroutines(false) = %+v, want the calling goroutine alone, %s one frame above it", self, test)
	}
	var found []testwait.Goroutine
	all := testwait.Goroutines(true)
	for i := range all {
		if len(all[i].Frames) > 0 && all[i].Frames[0].Function == stacksPackage+"stacksBlocked" {
			found = append(found, all[i])
		}
	}
	if len(found) != 1 {
		t.Fatalf("goroutines in stacksBlocked = %d, want 1: %+v", len(found), found)
	}
	g := found[0]
	header := "goroutine " + strconv.FormatInt(g.ID, 10) + " [chan receive]"
	if g.Header != header || g.Status != "chan receive" || g.Creator != self[0].ID ||
		g.CreatedBy.Function != stacksPackage+"stacksStart" ||
		!strings.HasSuffix(g.Frames[0].File, "/internal/testwait/stacks_test.go") || g.Frames[0].Line != 16 {
		t.Fatalf("parsed goroutine = %+v, want header %q, created by %sstacksStart in goroutine %d, "+
			"innermost frame stacks_test.go:16", g, header, stacksPackage, self[0].ID)
	}
}

func TestDriveFamilyWorkingNamesEveryGoroutineThatGoesOnWithoutAnOutsideEvent(t *testing.T) {
	look := testwait.Goroutine{ID: 99, Header: "goroutine 99 [running]", Status: "running"}
	g := func(id, creator int64, status string) testwait.Goroutine {
		return testwait.Goroutine{
			ID: id, Creator: creator, Status: status,
			Header: "goroutine " + strconv.FormatInt(id, 10) + " [" + status + "]",
			Frames: []testwait.Frame{{Function: "work.loop"}},
		}
	}
	cases := []struct {
		name string
		dump []testwait.Goroutine
		want string
	}{
		{"a member blocked on a channel is idle", []testwait.Goroutine{look, g(1, 0, "chan receive")}, ""},
		{
			"a member waiting for an assist to the collector works",
			[]testwait.Goroutine{look, g(1, 0, "GC assist wait")},
			"goroutine 1 [GC assist wait]",
		},
		{"a preempted member works", []testwait.Goroutine{look, g(1, 0, "preempted")}, "goroutine 1 [preempted]"},
		{
			"a member's child is a member",
			[]testwait.Goroutine{look, g(1, 0, "select"), g(2, 1, "runnable")},
			"goroutine 2 [runnable]",
		},
		{
			"a running orphan works",
			[]testwait.Goroutine{look, g(1, 0, "select"), g(5, 4, "running")},
			"goroutine 5 [running]",
		},
		{
			"a running child of a blocked orphan works",
			[]testwait.Goroutine{look, g(1, 0, "select"), g(5, 4, "chan receive"), g(6, 5, "running")},
			"goroutine 6 [running]",
		},
		{
			"an orphan in a system call is not seen",
			[]testwait.Goroutine{look, g(1, 0, "select"), g(5, 4, "syscall")},
			"",
		},
		{
			"a running goroutine of a present outsider is not the family's",
			[]testwait.Goroutine{look, g(1, 0, "select"), g(7, 0, "select"), g(8, 7, "running")},
			"",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := testwait.FamilyWorking([]int64{1}, tc.dump); got != tc.want {
				t.Fatalf("working = %q, want %q; dump %+v", got, tc.want, tc.dump)
			}
		})
	}
}
