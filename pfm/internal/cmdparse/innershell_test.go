package cmdparse

import (
	"reflect"
	"testing"

	"mvdan.cc/sh/v3/syntax"
)

func TestInnerShellAndArrays(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		command string
		want    []step
	}{
		{
			name:    "bash -c is parsed as shell",
			command: `bash -c 'cat x'`,
			want:    []step{{"bash", []string{"-c", "cat x"}}, {"cat", []string{"x"}}},
		},
		{
			name:    "sh -c with a cd inside",
			command: `sh -c "cd sub && head -5 y"`,
			want: []step{
				{"sh", []string{"-c", "cd sub && head -5 y"}},
				{"cd", []string{"sub"}},
				{"head", []string{"-5", "y"}},
			},
		},
		{
			name:    "wrapped bash with a combined -lc flag",
			command: `timeout 30 bash -lc 'grep -n f x'`,
			want:    []step{{"bash", []string{"-lc", "grep -n f x"}}, {"grep", []string{"-n", "f", "x"}}},
		},
		{
			name:    "an inner shell's variables end with it",
			command: `bash -c 'G=git'; $G push`,
			want:    []step{{"bash", []string{"-c", "G=git"}}, {"$G", []string{"push"}}},
		},
		{
			name:    "an inner shell sees no outer unexported variable",
			command: `G=git; bash -c '$G push'`,
			want:    []step{{"bash", []string{"-c", "$G push"}}, {"$G", []string{"push"}}},
		},
		{
			name:    "dash and ksh -c are parsed as shell",
			command: `dash -c 'cat x'; ksh -c 'cat y'`,
			want: []step{
				{"dash", []string{"-c", "cat x"}},
				{"cat", []string{"x"}},
				{"ksh", []string{"-c", "cat y"}},
				{"cat", []string{"y"}},
			},
		},
		{
			name:    "eval parses its words joined as shell",
			command: `eval "git commit -m x"; eval git status '&&' cat y`,
			want: []step{
				{"eval", []string{"git commit -m x"}},
				{"git", []string{"commit", "-m", "x"}},
				{"eval", []string{"git", "status", "&&", "cat", "y"}},
				{"git", []string{"status"}},
				{"cat", []string{"y"}},
			},
		},
		{
			name:    "eval runs in the calling shell",
			command: `eval 'G=git'; $G push`,
			want:    []step{{"eval", []string{"G=git"}}, {"git", []string{"push"}}},
		},
		{
			name:    "a non-literal -c string is not parsed",
			command: `bash -c "$CMD"`,
			want:    []step{{"bash", []string{"-c", `"$CMD"`}}},
		},
		{
			name:    "an array expands each element",
			command: `FILES=(x z); wc -l "${FILES[@]}"`,
			want:    []step{{"wc", []string{"-l", "x", "z"}}},
		},
		{
			name:    "array star, index and bare name",
			command: `FILES=(x "z"); cat ${FILES[*]}; head -1 ${FILES[1]}; tail -1 $FILES`,
			want: []step{
				{"cat", []string{"x", "z"}},
				{"head", []string{"-1", "z"}},
				{"tail", []string{"-1", "x"}},
			},
		},
		{
			name:    "an array element with a glob keeps it as written",
			command: `FILES=(sub/*); cat "${FILES[@]}"`,
			want:    []step{{"cat", []string{"sub/*"}}},
		},
		{
			name:    "a non-literal element drops the array",
			command: `FILES=(x "$Q"); cat "${FILES[@]}"`,
			want:    []step{{"cat", []string{`"${FILES[@]}"`}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertSteps(t, parseOne(t, tc.command), tc.want)
		})
	}
}

func TestInnerShellParts(t *testing.T) {
	t.Parallel()
	assertSteps(
		t,
		parseOne(t, `true && bash -c 'cat x'`),
		[]step{{"true", nil}, {"bash", []string{"-c", "cat x"}}, {"cat", []string{"x"}}},
	)
}

func TestInnerShellParseErrorIsOneErrorPart(t *testing.T) {
	t.Parallel()
	parts := parseOne(t, `bash -c 'echo "open'; cat x`)
	if len(parts) != 3 || parts[0].Program != "bash" || parts[1].Status != StatusError ||
		parts[2].Program != "cat" || !reflect.DeepEqual(parts[2].Args, []string{"x"}) {
		t.Fatalf("parts = %+v, want bash, one error part, then cat x", parts)
	}
}

// A -c string the parse cannot know marks its shell part Unread; a literal
// one, parsed in place, does not.
func TestInnerShellUnknownStringIsUnread(t *testing.T) {
	t.Parallel()
	for _, command := range []string{
		`bash -c "rm x $X"`, `sh -lc "$CMD"`, `zsh -c "echo $Z"`, `dash -c "$C"`, `eval "$CMD"`, `eval git $X`,
	} {
		parts := parseOne(t, command)
		if len(parts) != 1 || !parts[0].Unread || parts[0].Status != StatusOK {
			t.Errorf("%s: parts = %+v, want one ok part marked Unread", command, parts)
		}
	}
	for _, command := range []string{`bash -c 'rm x'`, `X=a; bash -c "rm x $X"`, `echo "$X"`, `X=a; eval rm $X`} {
		for _, part := range parseOne(t, command) {
			if part.Unread {
				t.Errorf("%s: part %+v is marked Unread", command, part)
			}
		}
	}
}

func TestDoubleQuotedEscapes(t *testing.T) {
	t.Parallel()
	assertSteps(
		t,
		parseOne(t, `bash -c "cat \"x y\""`),
		[]step{{"bash", []string{"-c", `cat "x y"`}}, {"cat", []string{"x y"}}},
	)
}

// nestShell wraps command in levels of `bash -c '…'`.
func nestShell(t *testing.T, command string, levels int) string {
	t.Helper()
	for range levels {
		quoted, err := syntax.Quote(command, syntax.LangBash)
		if err != nil {
			t.Fatalf("quote %q: %v", command, err)
		}
		command = "bash -c " + quoted
	}
	return command
}

// A literal -c string nested more than maxShellDepth levels is one unparsed,
// Bounded part; the levels around it parse as before.
func TestInnerShellNestingBound(t *testing.T) {
	t.Parallel()
	at := parseOne(t, nestShell(t, "cat x", maxShellDepth))
	if len(at) != maxShellDepth+1 || at[maxShellDepth].Program != "cat" ||
		!reflect.DeepEqual(at[maxShellDepth].Args, []string{"x"}) {
		t.Fatalf("parts = %+v, want %d bash parts then cat x", at, maxShellDepth)
	}
	over := parseOne(t, nestShell(t, "cat x", maxShellDepth+1))
	if len(over) != maxShellDepth+2 {
		t.Fatalf("parts = %+v, want %d bash parts then one unparsed part", over, maxShellDepth+1)
	}
	for i, part := range over[:maxShellDepth+1] {
		if part.Program != "bash" || part.Status != StatusOK || part.Bounded {
			t.Errorf("part %d = %+v, want an ok bash part", i, part)
		}
	}
	want := Part{Status: StatusUnparsed, Bounded: true}
	if got := over[maxShellDepth+1]; !reflect.DeepEqual(got, want) {
		t.Fatalf("level %d part = %+v\nwant %+v", maxShellDepth+1, got, want)
	}
}
