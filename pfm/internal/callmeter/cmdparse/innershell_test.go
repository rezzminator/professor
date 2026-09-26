package cmdparse

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// subFixture makes a cwd holding the named files, with sub/ holding the
// names in sub.
func subFixture(t *testing.T, top, sub []string) string {
	t.Helper()
	cwd := fixture(t, top...)
	if err := os.Mkdir(filepath.Join(cwd, "sub"), 0o700); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	for _, name := range sub {
		if err := os.WriteFile(filepath.Join(cwd, "sub", name), []byte("line\n"), 0o600); err != nil {
			t.Fatalf("write fixture sub/%s: %v", name, err)
		}
	}
	return cwd
}

func TestInnerShellAndCd(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		command string
		want    func(cwd string) []FileRef
	}{
		{
			name:    "bash -c is parsed as shell",
			command: `bash -c 'cat x'`,
			want:    func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionReadWhole, "")} },
		},
		{
			name:    "sh -c with a cd inside",
			command: `sh -c "cd sub && head -5 y"`,
			want:    func(cwd string) []FileRef { return []FileRef{ref(cwd, "sub/y", ActionReadRange, "1,5")} },
		},
		{
			name:    "wrapped bash with a combined -lc flag",
			command: `timeout 30 bash -lc 'grep -n f x'`,
			want:    func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionSearch, "")} },
		},
		{
			name:    "an inner cd ends with the inner shell",
			command: `bash -c 'cd sub'; cat x`,
			want:    func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionReadWhole, "")} },
		},
		{
			name:    "a non-literal -c string is not parsed",
			command: `bash -c "$CMD"`,
			want:    func(string) []FileRef { return nil },
		},
		{
			name:    "cd moves later parts",
			command: `cd sub && cat y`,
			want:    func(cwd string) []FileRef { return []FileRef{ref(cwd, "sub/y", ActionReadWhole, "")} },
		},
		{
			name:    "a subshell cd ends with the subshell",
			command: `(cd sub && cat y); cat z`,
			want: func(cwd string) []FileRef {
				return []FileRef{ref(cwd, "sub/y", ActionReadWhole, ""), ref(cwd, "z", ActionReadWhole, "")}
			},
		},
		{
			name:    "a cd in a branch moves the directory statically",
			command: `true && cd sub; cat y`,
			want:    func(cwd string) []FileRef { return []FileRef{ref(cwd, "sub/y", ActionReadWhole, "")} },
		},
		{
			name:    "a non-literal cd stops relative attribution",
			command: `cd "$D" && cat y && cat z`,
			want:    func(string) []FileRef { return nil },
		},
		{
			name:    "cd with no argument is non-literal",
			command: `cd; cat z`,
			want:    func(string) []FileRef { return nil },
		},
		{
			name:    "a glob after cd expands in the new directory",
			command: `cd sub && wc -l y*`,
			want:    func(cwd string) []FileRef { return []FileRef{ref(cwd, "sub/y", ActionReadWhole, "")} },
		},
		{
			name:    "an array expands each element",
			command: `FILES=(x z); wc -l "${FILES[@]}"`,
			want: func(cwd string) []FileRef {
				return []FileRef{ref(cwd, "x", ActionReadWhole, ""), ref(cwd, "z", ActionReadWhole, "")}
			},
		},
		{
			name:    "array star, index and bare name",
			command: `FILES=(x "z"); cat ${FILES[*]}; head -1 ${FILES[1]}; tail -1 $FILES`,
			want: func(cwd string) []FileRef {
				return []FileRef{
					ref(cwd, "x", ActionReadWhole, ""), ref(cwd, "z", ActionReadWhole, ""),
					ref(cwd, "z", ActionReadRange, "1,1"),
					ref(cwd, "x", ActionReadRange, "-1"),
				}
			},
		},
		{
			name:    "an array element with a glob expands it",
			command: `FILES=(sub/*); cat "${FILES[@]}"`,
			want:    func(cwd string) []FileRef { return []FileRef{ref(cwd, "sub/y", ActionReadWhole, "")} },
		},
		{
			name:    "a non-literal element drops the array",
			command: `FILES=(x "$Q"); cat "${FILES[@]}"`,
			want:    func(string) []FileRef { return nil },
		},
		{
			name:    "ssh never attributes a local file",
			command: `ssh host cat x`,
			want:    func(string) []FileRef { return nil },
		},
		{
			name:    "docker exec never attributes a local file",
			command: `docker exec -it box cat x; kubectl -n ns exec pod -- cat z`,
			want:    func(string) []FileRef { return nil },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cwd := subFixture(t, []string{"x", "y", "z"}, []string{"y"})
			parts := parseOne(t, cwd, tc.command, nil)
			for _, p := range parts {
				if p.Status != StatusOK {
					t.Fatalf("part %+v is not ok; parts = %+v", p, parts)
				}
			}
			assertFiles(t, parts, tc.want(cwd))
		})
	}
}

func TestAbsoluteCdAfterUnknownCd(t *testing.T) {
	t.Parallel()
	cwd := subFixture(t, []string{"x"}, []string{"y"})
	parts := parseOne(
		t,
		cwd,
		`cd "$D"; cat x; cd `+filepath.Join(cwd, "sub")+`; cat y; cat `+filepath.Join(cwd, "x"),
		nil,
	)
	assertFiles(t, parts, []FileRef{ref(cwd, "sub/y", ActionReadWhole, ""), ref(cwd, "x", ActionReadWhole, "")})
}

func TestInnerShellParts(t *testing.T) {
	t.Parallel()
	cwd := fixture(t, "x")
	parts := parseOne(t, cwd, `true && bash -c 'cat x'`, nil)
	type shape struct {
		program     string
		conditional bool
		files       int
	}
	var got []shape
	for _, p := range parts {
		got = append(got, shape{p.Program, p.Conditional, len(p.Files)})
	}
	want := []shape{{"true", false, 0}, {"bash", true, 0}, {"cat", true, 1}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parts = %+v\nwant    %+v", got, want)
	}
}

func TestInnerShellParseErrorIsOneErrorPart(t *testing.T) {
	t.Parallel()
	cwd := fixture(t, "x")
	parts := parseOne(t, cwd, `bash -c 'echo "open'; cat x`, nil)
	if len(parts) != 3 || parts[0].Program != "bash" || parts[1].Status != StatusError || parts[1].Error == "" ||
		parts[2].Program != "cat" || len(parts[2].Files) != 1 {
		t.Fatalf("parts = %+v, want bash, one error part, then cat x", parts)
	}
}

func TestPythonAfterCdResolvesInItsDirectory(t *testing.T) {
	t.Parallel()
	cwd := subFixture(t, []string{"y"}, []string{"y"})
	parts := parseOne(t, cwd, "cd sub && python3 - <<'EOF'\nopen('y').read()\nEOF\ncd ..", nil)
	assertFiles(t, parts, []FileRef{ref(cwd, "sub/y", ActionReadWhole, "")})
}

func TestInnerShellPythonSnippetIDsStayUnique(t *testing.T) {
	t.Parallel()
	cwd := fixture(t, "x", "z")
	cmd := `python3 -c "open('x')"; bash -c "python3 -c \"open('z')\""`
	parts := parseOne(t, cwd, cmd, nil)
	assertFiles(t, parts, []FileRef{ref(cwd, "x", ActionReadWhole, ""), ref(cwd, "z", ActionReadWhole, "")})
}

func TestDoubleQuotedEscapes(t *testing.T) {
	t.Parallel()
	cwd := fixture(t, "x y", "z")
	parts := parseOne(t, cwd, `bash -c "cat \"x y\""; python3 -c "open(\"z\")"`, nil)
	assertFiles(t, parts, []FileRef{ref(cwd, "x y", ActionReadWhole, ""), ref(cwd, "z", ActionReadWhole, "")})
}
