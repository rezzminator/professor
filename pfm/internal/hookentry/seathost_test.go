package hookentry

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

func TestSeatHostRejectsMissingSeat(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if rc := SeatHost(
		nil,
		&stdout,
		&stderr,
		config.Runtime{},
	); rc != 1 ||
		!strings.Contains(stderr.String(), "expected one seat id") {
		t.Fatalf("SeatHost = %d, %q", rc, stderr.String())
	}
}
