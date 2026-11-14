package hookentry

import (
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
)

// TestPcmGuardCodeReadsEveryInlineCodeForm: the inline code an interpreter runs is the value of its
// code flag (alone, in a short cluster, joined to it), --eval and --print; a shell's -c string is no
// code here, and a program that is no interpreter has none.
func TestPcmGuardCodeReadsEveryInlineCodeForm(t *testing.T) {
	for _, testCase := range []struct {
		base string
		args []string
		want []string
	}{
		{"python3", []string{"-c", "open('a')"}, []string{"open('a')"}},
		{"python", []string{"-c'open(1)'"}, []string{"'open(1)'"}},
		{"perl", []string{"-pe", "s/a/b/", "f"}, []string{"s/a/b/"}},
		{"node", []string{"--eval=x()", "--print", "y()"}, []string{"x()", "y()"}},
		{"ruby", []string{"-w", "-e", "File.write"}, []string{"File.write"}},
		{"bash", []string{"-c", "echo hi", "--eval", "z"}, nil},
		{"cat", []string{"-c", "x"}, nil},
	} {
		got := pcmGuardCode(&cmdparse.Part{Program: testCase.base, Args: testCase.args}, testCase.base)
		if !slices.Equal(got, testCase.want) {
			t.Errorf("pcmGuardCode(%s %q) = %q, want %q", testCase.base, testCase.args, got, testCase.want)
		}
	}
}
