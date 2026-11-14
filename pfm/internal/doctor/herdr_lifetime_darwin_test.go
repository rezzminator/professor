package doctor

import "testing"

func TestHerdrLifetimeVerdictIsThePlatforms(t *testing.T) {
	if herdrLifetimeUnverified != true {
		t.Fatalf("herdrLifetimeUnverified on darwin = %v, want true", herdrLifetimeUnverified)
	}
}
