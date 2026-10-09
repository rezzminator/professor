package tokens

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCallCapByAgentType(t *testing.T) {
	for agentType, want := range map[string]float64{"flights-foreman": 250, "Flights-Lander": 200, "tracer": 150, "": 150} {
		if got := callCap(agentType); got != want {
			t.Errorf("callCap(%q) = %v, want %v", agentType, got, want)
		}
	}
}

func TestRoleMatchNamesOneJob(t *testing.T) {
	cases := []struct {
		left, right string
		want        bool
	}{
		{"flights-lander", "Flights Lander", true},
		{"code reviewer", "reviewer", true},
		{"pr reviewer", "plan reviewer", true},
		{"tracer", "flights-foreman", false},
		{"", "tracer", false},
		{"--", "tracer", false},
	}
	for _, c := range cases {
		if got := roleMatch(c.left, c.right); got != c.want {
			t.Errorf("roleMatch(%q, %q) = %v, want %v", c.left, c.right, got, c.want)
		}
	}
}

func TestWriteOutputReplacesTheFileAndCreatesItsDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a", "b", "metrics.md")
	if n, err := writeOutput(path, "old content"); err != nil || n != 11 {
		t.Fatalf("writeOutput = %d, %v", n, err)
	}
	if n, err := writeOutput(path, "new"); err != nil || n != 3 {
		t.Fatalf("writeOutput = %d, %v", n, err)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "new" {
		t.Errorf("file holds %q, %v; want the whole file replaced", content, err)
	}
	blocked := filepath.Join(path, "under-a-file.md")
	if _, err := writeOutput(blocked, "x"); err == nil || !strings.HasPrefix(err.Error(), "write "+blocked+": ") {
		t.Errorf("writeOutput under a file = %v, want a write error naming the path", err)
	}
}
