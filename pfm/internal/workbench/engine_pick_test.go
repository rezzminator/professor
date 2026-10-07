package workbench

import (
	"testing"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestPickEngine(t *testing.T) {
	for _, tc := range []struct {
		name                         string
		preferred, unavailable, want pfmengine.ID
	}{
		{"preferred enabled", pfmengine.Claude, "", pfmengine.Claude},
		{"preferred disabled", pfmengine.OpenCode, "", pfmengine.Codex},
		{"preferred unusable", pfmengine.Codex, pfmengine.Codex, pfmengine.Claude},
		{"none usable", pfmengine.Claude, "all", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bench := Bench{Engines: []pfmengine.ID{pfmengine.Codex, pfmengine.Claude}}
			got, ok := PickEngine(bench, tc.preferred, func(id pfmengine.ID) bool {
				if !bench.Enables(id) {
					t.Fatalf("checked disabled engine %q", id)
				}
				return tc.unavailable != "all" && id != tc.unavailable
			})
			if got != tc.want || ok != (tc.want != "") {
				t.Fatalf("pick = %q, %v; want %q", got, ok, tc.want)
			}
		})
	}
}
