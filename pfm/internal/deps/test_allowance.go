package deps

import "sync"

// testAllowances counts, per command name, the tests that asked for the real
// one: a door that refuses a real child in a test process (internal/tmux for
// a tmux server) lets it through while the count is above zero. The count is
// process-wide, so a test that forgot its fake while another test holds the
// allowance is let through: the refusal is a net, the measure is the check.
var testAllowances = struct {
	sync.Mutex
	count map[string]int
}{count: map[string]int{}}

// AllowInTest lets a test process run the real name until the returned
// release is called (testjail.TmuxServer registers it as a t.Cleanup).
func AllowInTest(name string) (release func()) {
	testAllowances.Lock()
	testAllowances.count[name]++
	testAllowances.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			testAllowances.Lock()
			testAllowances.count[name]--
			testAllowances.Unlock()
		})
	}
}

// AllowedInTest reports whether some test of this process holds an
// AllowInTest for name.
func AllowedInTest(name string) bool {
	testAllowances.Lock()
	defer testAllowances.Unlock()
	return testAllowances.count[name] > 0
}
