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

const libSource = `package lib

import "testing"

func Render(s string) string { return s }

func Equal(t *testing.T, got, want string) {
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}
`

const testSource = `package a

import (
	"regexp"
	"strings"
	"testing"

	"example.test/m/lib"
)

func mustContain(t *testing.T, s, sub string) {
	if !strings.Contains(s, sub) {
		t.Fatalf("missing %q", sub)
	}
}

func TestSubstring(t *testing.T) {
	mustContain(t, lib.Render("x"), "x")
	if err := error(nil); err != nil {
		t.Fatal(err)
	}
}

func TestRegexp(t *testing.T) {
	if !regexp.MustCompile("x").MatchString(lib.Render("x")) {
		t.Error("no match")
	}
}

func TestMixed(t *testing.T) {
	mustContain(t, "ab", "a")
	lib.Equal(t, lib.Render("a"), "a")
}

func TestExact(t *testing.T) {
	if diff := cmp.Diff("a", "a"); diff != "" {
		t.Error(diff)
	}
}

func TestWeak(t *testing.T) {
	if got := lib.Render(""); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestNone(t *testing.T) { t.Run("x", func(t *testing.T) {}) }

func TestMain(m *testing.M) {}

func FuzzRender(f *testing.F) {
	f.Fuzz(func(t *testing.T, s string) {
		if strings.HasPrefix(s, "z") {
			t.Skip()
		} else if lib.Render(s) != s {
			t.Errorf("changed")
		}
	})
}
`

func TestRunClassifiesEveryTest(t *testing.T) {
	root := t.TempDir()
	for path, body := range map[string]string{
		"go.mod":          "module example.test/m\n",
		"lib/lib.go":      libSource,
		"a/a_test.go":     testSource,
		"bad/bad_test.go": "package bad\nfunc {\n",
	} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, path)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, list, wantOut, wantErr string
		args                         []string
		wantCode                     int
	}{
		{
			name:    "the ratchet lists substring tests",
			list:    "lib/lib.go\n\nREADME.md\na/a_test.go\n",
			wantOut: "a/a_test.go TestRegexp\na/a_test.go TestSubstring\n",
		},
		{
			name: "the census rows every test",
			args: []string{"-census"},
			list: "lib/lib.go\na/a_test.go\n",
			wantOut: "dir\tfile\tline\tfunc\tclass\tsubstr\texact\tweak\tdrives\n" +
				"a\ta_test.go\t51\tFuzzRender\texact\t0\t1\t0\tlib.Render\n" +
				"a\ta_test.go\t35\tTestExact\texact\t0\t1\t0\t\n" +
				"a\ta_test.go\t30\tTestMixed\tmixed\t1\t1\t0\tlib.Render\n" +
				"a\ta_test.go\t47\tTestNone\tnone\t0\t0\t0\t\n" +
				"a\ta_test.go\t24\tTestRegexp\tsubstring\t1\t0\t0\tlib.Render\n" +
				"a\ta_test.go\t17\tTestSubstring\tsubstring\t1\t0\t1\tlib.Render\n" +
				"a\ta_test.go\t41\tTestWeak\tweak\t0\t0\t1\tlib.Render\n",
		},
		{
			name:     "a root without go.mod could not look, never fewer tests",
			args:     []string{"-root", filepath.Join(root, "lib")},
			list:     "lib.go\n",
			wantErr:  "testassert: read go.mod: open ROOT/lib/go.mod: no such file or directory\n",
			wantCode: 2,
		},
		{
			name:     "an unparsable file is an error, never zero",
			list:     "a/a_test.go\nbad/bad_test.go\n",
			wantErr:  "testassert: parse bad/bad_test.go: ROOT/bad/bad_test.go:2:6: expected 'IDENT', found '{'\n",
			wantCode: 2,
		},
	} {
		var out, errOut bytes.Buffer
		code := censusRun(append([]string{"-root", root}, tc.args...), strings.NewReader(tc.list), &out, &errOut)
		gotErr := strings.ReplaceAll(errOut.String(), root, "ROOT")
		if code != tc.wantCode || out.String() != tc.wantOut || gotErr != tc.wantErr {
			t.Errorf("%s: code %d stdout %q stderr %q\nwant code %d stdout %q stderr %q",
				tc.name, code, out.String(), gotErr, tc.wantCode, tc.wantOut, tc.wantErr)
		}
	}
}
