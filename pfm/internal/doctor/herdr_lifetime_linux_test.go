package doctor

import "testing"

func TestHerdrLifetimeVerdictIsThePlatforms(t *testing.T) {
	if herdrLifetimeUnverified != false {
		t.Fatalf("herdrLifetimeUnverified on linux = %v, want false", herdrLifetimeUnverified)
	}
}
