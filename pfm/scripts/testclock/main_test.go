package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestMain(m *testing.M) { os.Exit(testjail.Run(m)) }

// tree copies testdata/{from}.src to {to} under a fresh root for every pair.
func tree(t *testing.T, pairs map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for from, to := range pairs {
		data, err := os.ReadFile(filepath.Join("testdata", from+".src"))
		if err != nil {
			t.Fatalf("read fixture %s: %v", from, err)
		}
		path := filepath.Join(root, to)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRunListsEveryDoorByLine(t *testing.T) {
	root := tree(t, map[string]string{
		"wait_test.go": "internal/loud/wait_test.go",
		"engine":       "internal/loud/testdata/engine.sh",
		"corpus.json":  "internal/loud/testdata/corpus.json",
		"testwait.go":  "internal/testwait/testwait.go",
		"fixture.py":   "internal/loud/testdata/fixture.py",
		"fixture.mjs":  "internal/loud/testdata/fixture.mjs",
	})
	list := strings.Join([]string{
		"internal/loud/wait_test.go",
		"internal/loud/testdata/engine.sh",
		"internal/loud/testdata/corpus.json",
		"internal/loud/testdata/fixture.py",
		"internal/loud/testdata/fixture.mjs",
		"internal/testwait/testwait.go",
	}, "\n") + "\n"
	var stdout, stderr bytes.Buffer
	code := listFindings([]string{"-root", root}, bytes.NewBufferString(list), &stdout, &stderr)
	// The expected rows name door 11's spellings, so they live in a data file, never in a Go string.
	wantData, err := os.ReadFile(filepath.Join("testdata", "want.txt"))
	if err != nil {
		t.Fatal(err)
	}
	want := string(wantData)
	if code != 0 || stdout.String() != want || stderr.String() != "" {
		t.Errorf(
			"run = %d\nstdout:\n%s\nwant (testdata/want.txt):\n%s\nstderr: %q",
			code,
			stdout.String(),
			want,
			stderr.String(),
		)
	}
}

func TestRunCouldNotLookIsExitTwo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "bad_test.go"), []byte("package loud\nfunc {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ list, wantErr string }{
		{"bad_test.go\n", "testclock: bad_test.go:2:6: expected 'IDENT', found '{'\n"},
		{"gone_test.go\n", "testclock: open " + filepath.Join(root, "gone_test.go") + ": no such file or directory\n"},
	}
	for _, tc := range cases {
		var stdout, stderr bytes.Buffer
		code := listFindings([]string{"-root", root}, bytes.NewBufferString(tc.list), &stdout, &stderr)
		if code != 2 || stdout.String() != "" || stderr.String() != tc.wantErr {
			t.Errorf(
				"listFindings(%q) = %d, stdout %q, stderr %q; want 2, \"\", %q",
				tc.list,
				code,
				stdout.String(),
				stderr.String(),
				tc.wantErr,
			)
		}
	}
}
