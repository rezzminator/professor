package gate

import (
	"testing"

	"hostops/pfm/internal/dream/artifact"
)

func TestCoverageExpandsIndicesBackToPinnedPaths(t *testing.T) {
	parsed, err := artifact.ParseCoverage(
		"2\tSKIP\ttwo\n1\tREAD\tone\nCONDUCT\ttechnique\tNONE\tnone\nCONDUCT\tprior\tNONE\tnone\nCONDUCT\tbaseline\tNONE\tnone\nEND-OF-RUN\n",
		2,
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Coverage(PinnedPaths{Paths: []string{"/a", "/b"}}, parsed)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Expanded) != 2 || result.Expanded[0].Path != "/a" || result.Expanded[1].Path != "/b" ||
		len(result.Conduct) != 3 {
		t.Fatalf("Coverage() = %#v", result)
	}
}
