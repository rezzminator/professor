package gate

import (
	"testing"

	"hostops/pfm/internal/dream/artifact"
)

func TestVerdictsPreserveRulesAndMarkOmissionsUnruled(t *testing.T) {
	parsed, err := artifact.ParseVerdicts("CONFIRM\tmaps/a.md\tchecked\n")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Verdicts([]string{"maps/b.md", "maps/a.md"}, parsed)
	if err != nil {
		t.Fatal(err)
	}
	if got := artifact.RenderNormalizedVerdicts(
		result.Normalized,
	); got != "CONFIRM\tmaps/a.md\tchecked\nUNRULED\tmaps/b.md\tno verifier verdict; not applied\n" {
		t.Fatalf("normalization = %q", got)
	}
}
