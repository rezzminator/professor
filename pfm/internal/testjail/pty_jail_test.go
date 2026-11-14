//go:build jail

package testjail

import (
	"slices"
	"testing"
)

// Both script(1) argv forms, byte for byte: util-linux quotes the command into
// one -c word before the typescript file; BSD takes the typescript file first
// and the command as it is.
func TestPTYScriptArgvForms(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(string, []string) []string
		argv  []string
		want  []string
	}{
		{"util-linux", utilLinuxScriptArgv, []string{"cat"}, []string{"/usr/bin/script", "-qefc", "'cat'", "/dev/null"}},
		{
			"util-linux quoting", utilLinuxScriptArgv,
			[]string{"printf", "a b"},
			[]string{"/usr/bin/script", "-qefc", "'printf' 'a b'", "/dev/null"},
		},
		{"bsd", bsdScriptArgv, []string{"cat"}, []string{"/usr/bin/script", "-qe", "/dev/null", "cat"}},
		{
			"bsd arguments", bsdScriptArgv,
			[]string{"printf", "a b"},
			[]string{"/usr/bin/script", "-qe", "/dev/null", "printf", "a b"},
		},
	} {
		if got := tc.build("/usr/bin/script", tc.argv); !slices.Equal(got, tc.want) {
			t.Errorf("%s: argv = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// A script(1) that cannot be resolved is PTYCommand's error, never a bare
// name left to fail at exec.
func TestPTYCommandRefusesAnUnresolvableScript(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	command, err := PTYCommand("cat")
	const want = `exec: "script": executable file not found in $PATH`
	if command != nil || err == nil || err.Error() != want {
		t.Fatalf("PTYCommand(cat) = %v, %v; want nil, %q", command, err, want)
	}
}
