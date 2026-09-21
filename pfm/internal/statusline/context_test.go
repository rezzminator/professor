package statusline

import (
	"strings"
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

func TestContextFloorKeyMatchesRetiredOverlay(t *testing.T) {
	if got := sanitizeProject(""); got != rootProjectKey {
		t.Fatalf("empty project key = %q, want %s", got, rootProjectKey)
	}
	if got := sanitizeProject("/work/acme.api_v2"); got != "work-acme-api-v2" {
		t.Fatalf("sanitized project key = %q", got)
	}
	if got := sanitizeProject("/" + strings.Repeat("a", 100)); len(got) != 80 {
		t.Fatalf("long project key length = %d, want 80", len(got))
	}
	window := contextWindow(
		Runtime{Env: map[string]string{}, Engine: pfmengine.Claude},
		input{Model: struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		}{ID: "claude-haiku-4", DisplayName: "Claude"}},
		transcript.Meta{},
	)
	if window != 200_000 {
		t.Fatalf("Haiku model-id window = %d, want 200000", window)
	}
}
