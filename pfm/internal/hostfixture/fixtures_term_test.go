package hostfixture

import (
	"os"
	"testing"
)

func TestBareTermClearsTERMAndForcesThePOSIXLocale(t *testing.T) {
	base := BareTerm(t)

	if got := os.Getenv("TERM"); got != "" {
		t.Fatalf("real process TERM = %q, want empty", got)
	}
	if _, ok := base.Env.Lookup("TERM"); ok {
		t.Fatal("Env mirror still holds TERM after BareTerm")
	}
	if got := os.Getenv("LANG"); got != "C" {
		t.Fatalf("real process LANG = %q, want %q", got, "C")
	}
	if got := base.Env.Get("LANG"); got != "C" {
		t.Fatalf("Env mirror LANG = %q, want %q", got, "C")
	}
	if got := os.Getenv("LC_ALL"); got != "C" {
		t.Fatalf("real process LC_ALL = %q, want %q", got, "C")
	}
}
