package ui

import "testing"

// The rail must stay a visible line all day and shine while a conversation
// is live: a fresh message lights the halo, a day-old edge keeps a readable
// floor, and an edge that carried a storm stays haloed after it passes.
func TestCosmosEdgeLightStaysVisibleAndShinesOnTraffic(t *testing.T) {
	fresh, freshHeat := cosmosEdgeLight(0, 1)
	if fresh != 1 || freshHeat != 1 {
		t.Fatalf("fresh edge: fade %.2f heat %.2f, want 1, 1", fresh, freshHeat)
	}
	old, oldHeat := cosmosEdgeLight(24*3600, 1)
	if old < 0.40 {
		t.Fatalf("day-old edge fades to %.2f — below the readable floor", old)
	}
	if oldHeat >= cosmosEdgeHaloHeat {
		t.Fatalf("day-old single-message edge keeps heat %.2f — it should run as a thread", oldHeat)
	}
	_, stormHeat := cosmosEdgeLight(24*3600, 12)
	if stormHeat < cosmosEdgeHaloHeat {
		t.Fatalf("edge with 12 messages has heat %.2f — a storm must keep its halo", stormHeat)
	}
	if stormHeat > 0.45 {
		t.Fatalf("traffic heat %.2f exceeds its 0.45 cap", stormHeat)
	}
	if _, neg := cosmosEdgeLight(-5, 1); neg != 1 {
		t.Fatalf("a message from the future (clock skew) has heat %.2f, want 1", neg)
	}
}
