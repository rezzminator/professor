package deps

import "testing"

// TestAllowInTestCountsUntilEveryReleaseRan: two holders keep the name
// allowed until both released; a second release of one holder counts once.
func TestAllowInTestCountsUntilEveryReleaseRan(t *testing.T) {
	const name = "allowance-probe"
	if AllowedInTest(name) {
		t.Fatal("AllowedInTest before any allowance = true, want false")
	}
	first, second := AllowInTest(name), AllowInTest(name)
	first()
	first()
	if !AllowedInTest(name) {
		t.Fatal("AllowedInTest with one holder left = false, want true")
	}
	second()
	if AllowedInTest(name) {
		t.Fatal("AllowedInTest after every release = true, want false")
	}
}
