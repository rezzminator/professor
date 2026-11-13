package statusline

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestStatuslineLaunchReadErrorShowsWarning(t *testing.T) {
	home := t.TempDir()
	testjail.DatabaseFiles(t, home)
	path := paths.DefaultStateDB(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = writer
	t.Cleanup(func() { os.Stderr = previous })
	got := cacheWindowSegment(Runtime{Home: home, Env: map[string]string{}}, time.Now(), "", -1, nil, "S")
	// The same unread record keeps the window the transcript names beside the marker.
	now := time.Date(2026, 9, 2, 10, 2, 0, 0, time.UTC)
	transcript := writeTranscript(
		t,
		`{"type":"user","timestamp":"2026-09-02T10:00:00Z","message":{"content":"go"}}`,
		`{"type":"assistant","timestamp":"2026-09-02T10:00:30Z","message":{"usage":{"cache_creation":{"ephemeral_1h_input_tokens":100}}}}`,
	)
	beside := stripANSICodes(cacheWindowSegment(
		Runtime{
			Home:     home,
			CacheDir: filepath.Join(home, "cache"),
			Env:      map[string]string{},
		},
		now,
		transcript,
		94,
		nil,
		"S",
	))
	if want := " │ 💾⚠ 💾1h✓58m:0s 94%"; beside != want {
		t.Fatalf("window beside an unread launch record = %q, want %q", beside, want)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stderr = previous
	logged, err := io.ReadAll(reader)
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "💾⚠") || !strings.Contains(got, cBad) {
		t.Fatalf("read error window = %q, want bad-colour warning", got)
	}
	if !strings.Contains(string(logged), "launch record S") || !strings.Contains(string(logged), "not a database") {
		t.Fatalf("stderr = %q, want the launch read cause", logged)
	}
}
