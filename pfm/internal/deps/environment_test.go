package deps

import (
	"strings"
	"testing"
)

func TestEnvironmentWithReplacesKeyOnce(t *testing.T) {
	t.Setenv("PFM_ENVIRONMENT_TEST", "old")
	environment := EnvironmentWith("PFM_ENVIRONMENT_TEST", "new")
	found := 0
	for _, entry := range environment {
		if strings.HasPrefix(entry, "PFM_ENVIRONMENT_TEST=") {
			found++
			if entry != "PFM_ENVIRONMENT_TEST=new" {
				t.Fatalf("entry = %q, want replacement", entry)
			}
		}
	}
	if found != 1 {
		t.Fatalf("replacement count = %d, want 1", found)
	}
}
