package run

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Each look reads the marker and reports that read's own error to the seam.
func TestReadOpenCodeMarkerReportsEachLook(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "assistants.json")
	if err := os.WriteFile(present, []byte(`{"sessionID":"run"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var looked []error
	previous := openCodeMarkerLooked
	openCodeMarkerLooked = func(err error) { looked = append(looked, err) }
	t.Cleanup(func() { openCodeMarkerLooked = previous })
	for _, row := range []struct {
		name, path, body string
		absent           bool
	}{
		{name: "absent", path: filepath.Join(dir, "missing.json"), absent: true},
		{name: "present", path: present, body: `{"sessionID":"run"}`},
	} {
		looked = nil
		body, err := readOpenCodeMarker(row.path)
		if string(body) != row.body || errors.Is(err, os.ErrNotExist) != row.absent || (err == nil) == row.absent {
			t.Errorf("%s: readOpenCodeMarker(%s) = %q, %v; want %q, absent %t",
				row.name, row.path, body, err, row.body, row.absent)
		}
		if len(looked) != 1 || !errors.Is(looked[0], err) {
			t.Errorf("%s: seam saw %v, want the one read's error %v", row.name, looked, err)
		}
	}
}
