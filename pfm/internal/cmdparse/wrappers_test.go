package cmdparse

import (
	"reflect"
	"testing"
)

// TestWrappersUnwrap: a wrapper is unwrapped, repeatedly, to the program it
// runs; the part records that program with its own arguments.
func TestWrappersUnwrap(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		command  string
		programs []string
		args     [][]string
	}{
		{
			name:     "command runs its program",
			command:  `command ls x`,
			programs: []string{"ls"},
			args:     [][]string{{"x"}},
		},
		{
			name:     "command -v is a lookup",
			command:  `command -v cat x; command -V x`,
			programs: []string{"command", "command"},
			args:     [][]string{{"-v", "cat", "x"}, {"-V", "x"}},
		},
		{
			name:     "builtin and exec",
			command:  `builtin cat x; exec -a name cat y`,
			programs: []string{"cat", "cat"},
			args:     [][]string{{"x"}, {"y"}},
		},
		{
			name:     "timeout with its duration",
			command:  `timeout 60 grep -n f x`,
			programs: []string{"grep"},
			args:     [][]string{{"-n", "f", "x"}},
		},
		{
			name:     "timeout flags taking a value",
			command:  `timeout -k 5 --signal KILL 60 cat x`,
			programs: []string{"cat"},
			args:     [][]string{{"x"}},
		},
		{
			name:     "env assignments",
			command:  `env A=1 B=2 cat x`,
			programs: []string{"cat"},
			args:     [][]string{{"x"}},
		},
		{
			name:     "env flags taking a value",
			command:  `env -i -u HOME -C . A=1 -- wc -l x`,
			programs: []string{"wc"},
			args:     [][]string{{"-l", "x"}},
		},
		{
			name:     "nice, nohup and /usr/bin/time",
			command:  `nice -n 10 cat x; nohup cat y; /usr/bin/time -f %e cat z`,
			programs: []string{"cat", "cat", "cat"},
			args:     [][]string{{"x"}, {"y"}, {"z"}},
		},
		{
			name:     "sudo with a user",
			command:  `sudo -u u head -n 5 x`,
			programs: []string{"head"},
			args:     [][]string{{"-n", "5", "x"}},
		},
		{
			name:     "xargs with a replace string",
			command:  `xargs -I {} cat {}`,
			programs: []string{"cat"},
			args:     [][]string{{"{}"}},
		},
		{
			name:     "xargs value flags",
			command:  `xargs -n 1 -P 4 -d , wc -l x`,
			programs: []string{"wc"},
			args:     [][]string{{"-l", "x"}},
		},
		{
			name:     "wrappers nest",
			command:  `timeout 60 env X=1 nice sudo -E xargs -0 head -n 3 x`,
			programs: []string{"head"},
			args:     [][]string{{"-n", "3", "x"}},
		},
		{
			name:     "a program path matches by base name",
			command:  `/bin/ls x`,
			programs: []string{"/bin/ls"},
			args:     [][]string{{"x"}},
		},
		{
			name:     "a script path is its own program",
			command:  `./run.sh x; scripts/s.sh; timeout 5 ./run.sh`,
			programs: []string{"./run.sh", "scripts/s.sh", "./run.sh"},
			args:     [][]string{{"x"}, nil, nil},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parts := parseOne(t, tc.command)
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
		})
	}
}
