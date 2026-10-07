package naming

import "testing"

func TestWorkbenchPrefix(t *testing.T) {
	for _, tc := range []struct{ title, want string }{{"Scribe", "SCRIBE"}, {"Lab 2", "LAB2"}, {"§§", "WORKBENCH"}, {"étude ２", "ÉTUDE２"}} {
		t.Run(tc.title, func(t *testing.T) {
			if got := WorkbenchPrefix(tc.title); got != tc.want {
				t.Fatalf("WorkbenchPrefix(%q) = %q, want %q", tc.title, got, tc.want)
			}
		})
	}
}

func TestNextNumbered(t *testing.T) {
	if got := NextNumbered("_SCRIBE", []string{"_SCRIBE:1", "_SCRIBE:3", "_SCRIBE:10x"}); got != "_SCRIBE:2" {
		t.Fatalf("NextNumbered = %q, want _SCRIBE:2", got)
	}
}
