package testwait

import (
	"testing"
	"time"
)

// The unexported halves of the door, for its own tests: the waits with their
// bound handed in, and the bound's arithmetic.
var (
	UntilWithin    = untilBounded
	UntilErrWithin = untilErrBounded
	LimitAt        = limitAt
	DriveWithin    = driveBounded
	Process        = processClock
	DriveLooks     = &driveLooks
)

func ReceiveWithin[T any](t testing.TB, condition string, ch <-chan T, within time.Duration) (T, bool) {
	return receiveBounded(t, condition, ch, within)
}

// FamilyWorking is driveFamily.working over a dump, the family seeded with members.
func FamilyWorking(members []int64, all []Goroutine) string {
	family := &driveFamily{members: map[int64]bool{}}
	for _, id := range members {
		family.members[id] = true
	}
	return family.working(all)
}
