package main

import (
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/herdr"
)

// TestHerdrIndicatorsIsTheVerbThePluginExecs pins the registry name to the
// verb the Herdr plugin manifest execs.
func TestHerdrIndicatorsIsTheVerbThePluginExecs(t *testing.T) {
	if !slices.Contains(internalSubcommands, herdr.IndicatorVerb) {
		t.Fatalf("internalSubcommands lacks %q, the verb the Herdr indicator plugin execs", herdr.IndicatorVerb)
	}
}
