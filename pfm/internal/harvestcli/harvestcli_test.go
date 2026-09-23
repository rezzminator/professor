package harvestcli

import (
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// TestRenderReadNamesAPartialArtifact pins the CLI receipt header: a
// known-incomplete artifact says so on the cache_status line, before the blank
// line and the content; a complete one carries no PARTIAL notice.
func TestRenderReadNamesAPartialArtifact(t *testing.T) {
	result := harvest.Result{
		Source:      "https://fixture.example/source",
		CacheStatus: "miss",
		Bytes:       12,
		Tokens:      3,
		Path:        "/cache/source.md",
		Content:     "body text",
	}
	complete := renderRead(result, false)
	if strings.Contains(complete, "PARTIAL:") {
		t.Fatalf("complete receipt names a PARTIAL notice:\n%s", complete)
	}
	result.Partial = "page 3 of 9 failed to convert"
	want := "cache_status: miss / bytes: 12 / tokens: 3 / path: /cache/source.md" +
		" / PARTIAL: page 3 of 9 failed to convert\n\nbody text"
	if got := renderRead(result, false); !strings.Contains(got, want) {
		t.Fatalf("partial receipt header:\n%s\nwant it to contain:\n%s", got, want)
	}
	// The size probe is a receipt too: a caller budgeting a read must learn
	// the artifact is incomplete before it reads it.
	if got := renderRead(result, true); !strings.Contains(got, " / PARTIAL: page 3 of 9 failed to convert") {
		t.Fatalf("size-only receipt hides the PARTIAL notice:\n%s", got)
	}
	result.Partial = ""
	if got := renderRead(result, true); strings.Contains(got, "PARTIAL:") {
		t.Fatalf("complete size-only receipt names a PARTIAL notice:\n%s", got)
	}
}
