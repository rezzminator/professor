package professor

import (
	"bytes"
	"testing"

	"hostops/pfm/internal/config"
	"hostops/pfm/internal/obs"
)

// TestRunProjectUpdateRecordsATransition: RunProjectUpdate walks the state
// door (spec § Middleware, `state`) — requested to updated on a successful
// run, comp=state, kind=professor — never the project root path it read.
func TestRunProjectUpdateRecordsATransition(t *testing.T) {
	_, recorder := obs.Test(t)
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := RunProjectUpdate("", []string{"--root", root}, &stdout, &stderr, config.Runtime{}); code != 0 {
		t.Fatalf("RunProjectUpdate() code = %d, stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	var found bool
	for _, record := range recorder.Records() {
		if record.Message != "state.transition" {
			continue
		}
		if kind, _ := record.Field("kind"); kind != "professor" {
			continue
		}
		if next, _ := record.Field("next"); next == "updated" {
			found = true
		}
	}
	if !found {
		t.Fatalf("RunProjectUpdate() wrote no professor->updated transition: %s", recorder.Raw())
	}
}
