package artifact

import (
	"strings"
	"testing"
)

func TestCoverageIsIndexKeyedAndExpandsPaths(t *testing.T) {
	text := strings.Join([]string{
		"2\tSKIP\tno durable investigation",
		"1\tREAD\tcondensed and checked",
		"CONDUCT\ttechnique\tNONE\tno reusable technique",
		"CONDUCT\tprior\tprior-map\tprior revisited",
		"CONDUCT\tbaseline\tNONE\tbaseline unchanged",
		"END-OF-RUN",
		"",
	}, "\n")

	coverage, err := ParseCoverage(text, 2)
	if err != nil {
		t.Fatalf("ParseCoverage() error = %v", err)
	}
	if len(coverage.Lines) != 2 || len(coverage.Conduct) != 3 {
		t.Fatalf("ParseCoverage() counts = lines %d, conduct %d", len(coverage.Lines), len(coverage.Conduct))
	}
	want := "/corpus/agent-one.jsonl\tREAD\tcondensed and checked\n" +
		"/corpus/agent-two.jsonl\tSKIP\tno durable investigation\n"
	if got := RenderExpandedCoverage(
		coverage,
		[]string{"/corpus/agent-one.jsonl", "/corpus/agent-two.jsonl"},
	); got != want {
		t.Fatalf("RenderExpandedCoverage() = %q, want %q", got, want)
	}
}

func TestCoverageMismatchFailsClosed(t *testing.T) {
	text := "1\tREAD\tfirst\n1\tSKIP\tduplicate\n" + completeConduct + "END-OF-RUN\n"
	_, err := ParseCoverage(text, 2)
	assertErrorContains(t, err, "DUPLICATE INDEXES:\n1")
	assertErrorContains(t, err, "UNRULED INDEXES:\n2")
}

func TestCoverageRequiresEveryConductKindExactlyOnce(t *testing.T) {
	missing := "1\tREAD\tread\n" +
		"CONDUCT\ttechnique\tNONE\tnone\n" +
		"CONDUCT\tprior\tNONE\tnone\n" +
		"END-OF-RUN\n"
	_, err := ParseCoverage(missing, 1)
	assertErrorContains(t, err, "missing CONDUCT accounting for: baseline")

	duplicate := "1\tREAD\tread\n" + completeConduct +
		"CONDUCT\tbaseline\tNONE\tduplicate\nEND-OF-RUN\n"
	_, err = ParseCoverage(duplicate, 1)
	assertErrorContains(t, err, "CONDUCT accounting must occur exactly once for: baseline")
}

func TestCoverageRequiresOneFinalEndOfRun(t *testing.T) {
	_, err := ParseCoverage("1\tREAD\tread\nEND-OF-RUN\n"+completeConduct, 1)
	assertErrorContains(t, err, "missing final END-OF-RUN")

	_, err = ParseCoverage("1\tREAD\tread\n"+completeConduct+"END-OF-RUN\nEND-OF-RUN\n", 1)
	assertErrorContains(t, err, "END-OF-RUN must occur exactly once")
}

const completeConduct = "CONDUCT\ttechnique\tNONE\tnone\n" +
	"CONDUCT\tprior\tNONE\tnone\n" +
	"CONDUCT\tbaseline\tNONE\tnone\n"

func assertErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want it to contain %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to contain %q", err, want)
	}
}
