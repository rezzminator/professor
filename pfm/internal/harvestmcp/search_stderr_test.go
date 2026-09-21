package harvestmcp

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// TestSearchRoutesTheFullDiagnosticToTheLogAndOnlyTheSafeMessageToStderr is
// L2-F17: fmt.Fprintf(os.Stderr, "harvester search: %v\n", err) printed
// SearchBackendError.Error() — the FULL diagnostic (search.go:56-58), which
// can carry a backend URL — raw to stderr, bypassing obs entirely. The fix:
// the full error goes through the scrubbed activity log; stderr gets only
// renderSearchFailure's safe rendering, the same text the tool result
// carries, never the raw net/http dial detail.
func TestSearchRoutesTheFullDiagnosticToTheLogAndOnlyTheSafeMessageToStderr(t *testing.T) {
	ctx, recorder := obs.Test(t)
	service, err := NewConfiguredHarvester("test", Runtime{
		Home:     t.TempDir(),
		CacheDir: filepath.Join(t.TempDir(), "cache"),
		// Nothing listens here: a fast, deterministic connection failure.
		SearXNGURL: "http://127.0.0.1:1/search",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = service.Close() }()

	realStderr := os.Stderr
	read, write, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = write
	result, _, callErr := service.search(ctx, nil, SearchInput{Query: "test query"})
	_ = write.Close()
	os.Stderr = realStderr
	captured, err := io.ReadAll(read)
	if err != nil {
		t.Fatal(err)
	}

	if callErr != nil {
		t.Fatalf("search returned a Go error instead of an IsError result: %v", callErr)
	}
	if result == nil || !result.IsError {
		t.Fatalf("a backend failure did not become a tool error: %+v", result)
	}
	// The raw net/http diagnostic ("Get http://...", "dial tcp ...") stays
	// off stderr; only the classified, backend-named safe message reaches it.
	for _, raw := range []string{"dial tcp", "Get http", "connect:"} {
		if strings.Contains(string(captured), raw) {
			t.Fatalf("the raw diagnostic reached stderr: %q", captured)
		}
	}
	if !strings.Contains(string(captured), "searxng") {
		t.Fatalf("stderr lost the safe, backend-named rendering entirely: %q", captured)
	}

	written := recorder.Raw()
	if !strings.Contains(written, "dial tcp") {
		t.Fatalf("the full diagnostic never reached the activity log: %s", written)
	}
}
