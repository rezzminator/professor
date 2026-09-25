package opencodegen

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestRunCommandRejectsUnknownAction(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := RunCommand(
		[]string{"generate"},
		func() (string, error) { return ".", nil },
		".",
		&stdout,
		&stderr,
	); code != 2 {
		t.Fatalf("unknown action code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunCommandBuildPassLineCountsAndNamesDeletions(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source := filepath.Join(root, ".claude", "commands", "review.md")
	writeTestFile(t, source, "---\ndescription: Review.\n---\nReview.\n")
	build := func() string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := RunCommand(
			[]string{"build", root, "--home", home},
			func() (string, error) { return root, nil },
			home,
			&stdout,
			&stderr,
		); code != 0 {
			t.Fatalf("build code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}

	first := build()
	if !regexp.MustCompile(`(?m)^OPENCODE BUILD PASS wrote=[1-9][0-9]* unchanged=0 deleted=0$`).MatchString(first) {
		t.Fatalf("first build stdout=%q", first)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	second := build()
	deleted := filepath.Join(root, ".opencode", "command", "review.md")
	if !regexp.MustCompile(`(?m)^OPENCODE BUILD PASS wrote=0 unchanged=[1-9][0-9]* deleted=1$`).MatchString(second) ||
		!regexp.MustCompile(`(?m)^pfm opencode: deleted `+regexp.QuoteMeta(deleted)+`$`).MatchString(second) {
		t.Fatalf("second build stdout=%q", second)
	}
}
