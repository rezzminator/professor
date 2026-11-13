package harness

import (
	"os"
	"path/filepath"
	"testing"
)

func TestUniqueNamesNeverRepeatInARun(t *testing.T) {
	var names []string
	Run(t, Do("root", func(c *Ctx, in int) int {
		names = append(names, c.Unique("chat"), c.Unique("chat"), c.Unique("pane"))
		return in
	}), 0, Hooks{})
	id := names[0][len("chat-"):]
	want := []string{"chat-" + id, "chat-" + id + "-2", "pane-" + id}
	if id == "" || names[1] != want[1] || names[2] != want[2] {
		t.Fatalf("Unique = %q, want %q", names, want)
	}
}

func TestArtifactLandsUnderTheNodesPathAndExpectIsKept(t *testing.T) {
	dir := t.TempDir()
	var expected []string
	Run(t, Do("root", nil, Do("step", func(c *Ctx, in int) int {
		c.Artifact("screen.txt", []byte("hello"))
		c.Expect("ERROR seat gone")
		expected = c.Expected()
		return in
	})), 0, Hooks{Artifacts: dir})
	file := filepath.Join(dir, "root", "step", "screen.txt")
	data, err := os.ReadFile(file)
	if err != nil || string(data) != "hello" {
		t.Fatalf("artifact %s = %q (%v), want %q", file, data, err, "hello")
	}
	if len(expected) != 1 || expected[0] != "ERROR seat gone" {
		t.Fatalf("Expected = %q, want [ERROR seat gone]", expected)
	}
	tsv, err := os.ReadFile(filepath.Join(dir, "timeline.tsv"))
	if err != nil ||
		string(
			tsv[:len("path\tverdict\tstart_s\tseconds\nroot\tPASS\t")],
		) != "path\tverdict\tstart_s\tseconds\nroot\tPASS\t" {
		t.Fatalf("timeline.tsv = %q (%v), want the header then root PASS", tsv, err)
	}
}
