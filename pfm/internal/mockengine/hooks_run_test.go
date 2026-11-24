package mockengine

import (
	"os"
	"testing"
)

// TestHookOutputsAreUnlinkedFilesTheAnswerIsReadFrom: what a hook writes to its two output files is read
// back whole, and neither file has a name left on disk once they are open.
func TestHookOutputsAreUnlinkedFilesTheAnswerIsReadFrom(t *testing.T) {
	stdout, stderr, err := hookOutputs()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stdout.Close(); _ = stderr.Close() }()
	for _, file := range []*os.File{stdout, stderr} {
		if _, err := os.Stat(file.Name()); !os.IsNotExist(err) {
			t.Fatalf("the hook output %s still has a name on disk: stat %v", file.Name(), err)
		}
	}
	if _, err := stdout.WriteString(`{"decision":"block"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := stderr.WriteString("warned\n"); err != nil {
		t.Fatal(err)
	}
	output, errOutput, err := readHookOutputs(stdout, stderr)
	if err != nil || output != `{"decision":"block"}`+"\n" || errOutput != "warned\n" {
		t.Fatalf("readHookOutputs = %q, %q, %v, want the two writes whole", output, errOutput, err)
	}
}
