package digest

import (
	"encoding/json"
	"testing"
)

func TestCodexCommandLineAndNumberReadEveryForm(t *testing.T) {
	commands := []struct{ raw, want string }{
		{`["bash","-lc","ls -la"]`, "ls -la"},
		{`["sh","-c","echo hi"]`, "echo hi"},
		{`["git","status"]`, "git status"},
		{`"make test"`, "make test"},
	}
	for _, row := range commands {
		if got := commandLine(json.RawMessage(row.raw)); got != row.want {
			t.Errorf("commandLine(%s) = %q, want %q", row.raw, got, row.want)
		}
	}
	numbers := []struct {
		raw  string
		want float64
	}{{`3`, 3}, {`0.5`, 0.5}, {`"3"`, 0}, {`null`, 0}, {``, 0}}
	for _, row := range numbers {
		if got := decodeNumber(json.RawMessage(row.raw)); got != row.want {
			t.Errorf("decodeNumber(%q) = %v, want %v", row.raw, got, row.want)
		}
	}
}
