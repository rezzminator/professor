package cmdparse

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestWrappersUnwrap: a wrapper is unwrapped, repeatedly, to the program it
// runs; the part records that program with its own arguments and the
// ordinary dispatch attributes them.
func TestWrappersUnwrap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		command  string
		programs []string
		args     [][]string
		want     func(cwd string) []FileRef
	}{
		{
			name:     "command runs its program",
			command:  `command ls x`,
			programs: []string{"ls"},
			args:     [][]string{{"x"}},
			want:     func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionStat, "")} },
		},
		{
			name:     "command -v is a lookup",
			command:  `command -v cat x; command -V x`,
			programs: []string{"command", "command"},
			args:     [][]string{{"-v", "cat", "x"}, {"-V", "x"}},
			want:     func(string) []FileRef { return nil },
		},
		{
			name:     "builtin and exec",
			command:  `builtin cat x; exec -a name cat y`,
			programs: []string{"cat", "cat"},
			args:     [][]string{{"x"}, {"y"}},
			want: func(cwd string) []FileRef {
				return []FileRef{ref(cwd, "x", ActionReadWhole, ""), ref(cwd, "y", ActionReadWhole, "")}
			},
		},
		{
			name:     "timeout with its duration",
			command:  `timeout 60 grep -n f x`,
			programs: []string{"grep"},
			args:     [][]string{{"-n", "f", "x"}},
			want:     func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionSearch, "")} },
		},
		{
			name:     "timeout flags taking a value",
			command:  `timeout -k 5 --signal KILL 60 cat x`,
			programs: []string{"cat"},
			args:     [][]string{{"x"}},
			want:     func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionReadWhole, "")} },
		},
		{
			name:     "env assignments",
			command:  `env A=1 B=2 cat x`,
			programs: []string{"cat"},
			args:     [][]string{{"x"}},
			want:     func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionReadWhole, "")} },
		},
		{
			name:     "env flags taking a value",
			command:  `env -i -u HOME -C . A=1 -- wc -l x`,
			programs: []string{"wc"},
			args:     [][]string{{"-l", "x"}},
			want:     func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionReadWhole, "")} },
		},
		{
			name:     "nice, nohup and /usr/bin/time",
			command:  `nice -n 10 cat x; nohup cat y; /usr/bin/time -f %e cat z`,
			programs: []string{"cat", "cat", "cat"},
			args:     [][]string{{"x"}, {"y"}, {"z"}},
			want: func(cwd string) []FileRef {
				return []FileRef{
					ref(cwd, "x", ActionReadWhole, ""),
					ref(cwd, "y", ActionReadWhole, ""),
					ref(cwd, "z", ActionReadWhole, ""),
				}
			},
		},
		{
			name:     "sudo with a user",
			command:  `sudo -u u head -n 5 x`,
			programs: []string{"head"},
			args:     [][]string{{"-n", "5", "x"}},
			want:     func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionReadRange, "1,5")} },
		},
		{
			name:     "xargs with a replace string",
			command:  `xargs -I {} cat {}`,
			programs: []string{"cat"},
			args:     [][]string{{"{}"}},
			want:     func(string) []FileRef { return nil },
		},
		{
			name:     "xargs value flags",
			command:  `xargs -n 1 -P 4 -d , wc -l x`,
			programs: []string{"wc"},
			args:     [][]string{{"-l", "x"}},
			want:     func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionReadWhole, "")} },
		},
		{
			name:     "wrappers nest",
			command:  `timeout 60 env X=1 nice sudo -E xargs -0 head -n 3 x`,
			programs: []string{"head"},
			args:     [][]string{{"-n", "3", "x"}},
			want:     func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionReadRange, "1,3")} },
		},
		{
			name:     "a program path matches by base name",
			command:  `/bin/ls x`,
			programs: []string{"/bin/ls"},
			args:     [][]string{{"x"}},
			want:     func(cwd string) []FileRef { return []FileRef{ref(cwd, "x", ActionStat, "")} },
		},
		{
			name:     "a script path under cwd is an exec",
			command:  `./run.sh x; scripts/s.sh; timeout 5 ./run.sh`,
			programs: []string{"./run.sh", "scripts/s.sh", "./run.sh"},
			args:     [][]string{{"x"}, nil, nil},
			want: func(cwd string) []FileRef {
				return []FileRef{
					ref(cwd, "run.sh", ActionExec, ""),
					ref(cwd, "x", ActionUnknown, ""),
					ref(cwd, "scripts/s.sh", ActionExec, ""),
					ref(cwd, "run.sh", ActionExec, ""),
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cwd := fixture(t, "x", "y", "z", "run.sh")
			if err := os.Mkdir(filepath.Join(cwd, "scripts"), 0o700); err != nil {
				t.Fatalf("mkdir scripts: %v", err)
			}
			if err := os.WriteFile(filepath.Join(cwd, "scripts", "s.sh"), []byte("true\n"), 0o600); err != nil {
				t.Fatalf("write scripts/s.sh: %v", err)
			}
			parts := parseOne(t, cwd, tc.command, nil)
			var programs []string
			var args [][]string
			for _, p := range parts {
				if p.Status != StatusOK {
					t.Fatalf("part %+v: status %q, want ok", p, p.Status)
				}
				programs = append(programs, p.Program)
				args = append(args, p.Args)
			}
			if !reflect.DeepEqual(programs, tc.programs) {
				t.Fatalf("programs = %q, want %q", programs, tc.programs)
			}
			for i := range args {
				if len(args[i]) == 0 && len(tc.args[i]) == 0 {
					continue
				}
				if !reflect.DeepEqual(args[i], tc.args[i]) {
					t.Fatalf("part %d args = %q, want %q", i, args[i], tc.args[i])
				}
			}
			assertFiles(t, parts, tc.want(cwd))
		})
	}
}

// TestPythonUnderWrapper: `timeout 5 python3 - <<EOF` is still a Python
// snippet once timeout is unwrapped.
func TestPythonUnderWrapper(t *testing.T) {
	t.Parallel()
	cwd := fixture(t, "s.txt")
	parts := parseOne(t, cwd, "timeout 5 python3 - <<'EOF'\nopen('s.txt').read()\nEOF", nil)
	if len(parts) != 1 || parts[0].Lang != LangPython || parts[0].Program != "python3" {
		t.Fatalf("parts = %+v, want one python3 part", parts)
	}
	assertFiles(t, parts, []FileRef{ref(cwd, "s.txt", ActionReadWhole, "")})
}

// TestProgramPathFollowsCd: a program path resolves against, and must lie
// under, the directory its part runs in after any `cd`; an unknown
// directory attributes nothing.
func TestProgramPathFollowsCd(t *testing.T) {
	t.Parallel()
	top := subFixture(t, []string{"run.sh"}, []string{"run.sh"})
	sub := filepath.Join(top, "sub")
	cases := []struct {
		name    string
		command string
		want    []FileRef
	}{
		{"cd up to the parent", `cd .. && ./run.sh`, []FileRef{ref(top, "run.sh", ActionExec, "")}},
		{
			"cd to an absolute directory",
			`cd ` + top + ` && sub/run.sh`,
			[]FileRef{ref(top, "sub/run.sh", ActionExec, "")},
		},
		{"a path above the current directory", `cd .. && cd sub && ../run.sh`, nil},
		{"an unknown directory", `cd "$D" && ` + filepath.Join(sub, "run.sh"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertFiles(t, parseOne(t, sub, tc.command, nil), tc.want)
		})
	}
}
