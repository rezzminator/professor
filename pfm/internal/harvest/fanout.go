package harvest

import "fmt"

// recoverItem recovers a panic inside a per-item fan-out goroutine and hands
// the recovered value to set as that item's own error, so one bad input's
// crash cannot take the whole batch — or, since nothing here wraps the
// goroutine in a boundary of its own, the whole process — down with it. Call
// `defer recoverItem(func(e error) { ... })` as the goroutine's FIRST
// deferred statement after `defer wg.Done()` (LIFO means it then runs BEFORE
// Done, so the recorded error is visible to whatever reads it once Wait
// returns); set assigns into the exact slot the goroutine's normal path
// would have written its own error into. A non-panicking run never calls
// set.
func recoverItem(set func(error)) {
	if r := recover(); r != nil {
		set(fmt.Errorf("recovered from panic: %v", r))
	}
}
