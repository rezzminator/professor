package launchnotice

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func stage(t *testing.T, terminal bool, input io.Reader) {
	t.Helper()
	t.Cleanup(Replace(input, terminal))
}

type countingReader struct {
	reads int
	data  *strings.Reader
}

func (reader *countingReader) Read(p []byte) (int, error) {
	reader.reads++
	return reader.data.Read(p)
}

func TestHold(t *testing.T) {
	for _, test := range []struct {
		name      string
		terminal  bool
		warnings  []string
		wantOut   string
		wantReads bool
	}{
		{"terminal waits for Enter", true, []string{"no prompt"}, "pfm: no prompt\n" + EnterPrompt + "\n", true},
		{"no terminal only prints", false, []string{"no prompt"}, "pfm: no prompt\n", false},
		{"no warning on a terminal says nothing and never waits", true, nil, "", false},
		{"every warning is printed before one wait", true, []string{"a", "b"}, "pfm: a\npfm: b\n" + EnterPrompt + "\n", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := &countingReader{data: strings.NewReader("\nrest\n")}
			stage(t, test.terminal, input)
			var out bytes.Buffer
			Hold(&out, "pfm", test.warnings)
			if out.String() != test.wantOut {
				t.Fatalf("output = %q, want %q", out.String(), test.wantOut)
			}
			if (input.reads > 0) != test.wantReads {
				t.Fatalf("stdin reads = %d, want a read: %t", input.reads, test.wantReads)
			}
		})
	}
}

func TestHoldContinuesWhenStdinIsClosed(t *testing.T) {
	stage(t, true, strings.NewReader(""))
	var out bytes.Buffer
	Hold(&out, "pfm", []string{"no prompt"})
	if out.String() != "pfm: no prompt\n"+EnterPrompt+"\n" {
		t.Fatalf("closed stdin output = %q", out.String())
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestHoldSaysWhenStdinCannotBeRead(t *testing.T) {
	stage(t, true, failingReader{})
	var out bytes.Buffer
	Hold(&out, "pfm", []string{"no prompt"})
	if !strings.Contains(out.String(), "read the acknowledgement") || !strings.Contains(out.String(), "closed pipe") {
		t.Fatalf("a failed read said nothing: %q", out.String())
	}
}

func TestPrintNeverWaits(t *testing.T) {
	input := &countingReader{data: strings.NewReader("\n")}
	stage(t, true, input)
	var out bytes.Buffer
	Print(&out, "pfm chat branch", []string{"no prompt"})
	if out.String() != "pfm chat branch: no prompt\n" || input.reads != 0 {
		t.Fatalf("Print output = %q reads = %d", out.String(), input.reads)
	}
}

func TestReplaceRestoresTheRealStdin(t *testing.T) {
	before := stdin
	restore := Replace(strings.NewReader(""), true)
	restore()
	if stdin != before {
		t.Fatal("Replace's undo left the replaced stdin in place")
	}
}
