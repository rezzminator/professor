package cmdparse

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

//go:embed pyscan.py
var pyscanScript string

// Snippet is one piece of Python source found inside a Bash call.
type Snippet struct {
	ID   string `json:"id"`
	Code string `json:"code"`
}

// PyCall is one call expression of a snippet. Args holds each positional
// argument's string constant, nil where the argument is not a string
// literal. Recv holds the positional arguments of the call a method is
// invoked on (`Path("x").read_text()` → Recv ["x"]). Argv is a
// `subprocess.*` call's first argument as an argv, when it is literal.
type PyCall struct {
	Func   string            `json:"func"`
	Args   []*string         `json:"args"`
	Kwargs map[string]string `json:"kwargs"`
	Recv   []*string         `json:"recv"`
	Argv   []string          `json:"argv"`
}

// PyResult is the scan of one snippet. Error is the parser's message when the
// snippet did not parse, empty otherwise.
type PyResult struct {
	ID      string   `json:"id"`
	Calls   []PyCall `json:"calls"`
	Strings []string `json:"strings"`
	Error   string   `json:"error"`
}

// PythonRunner scans a batch of snippets with Python's own ast module. An
// error means the batch was not scanned at all (no interpreter, a crash,
// unreadable output); ParseBatch then marks every Python part of the batch
// python-unavailable with that error as the cause.
type PythonRunner interface {
	Analyze(ctx context.Context, snippets []Snippet) ([]PyResult, error)
}

// Python3 runs the embedded scanner as `{Program} -c {script}`, one process
// per batch. A nil Runner is deps.RealRunner; an empty Program is "python3".
type Python3 struct {
	Runner  deps.Runner
	Program string
}

// Analyze implements PythonRunner.
func (p Python3) Analyze(ctx context.Context, snippets []Snippet) ([]PyResult, error) {
	if len(snippets) == 0 {
		return nil, nil
	}
	runner := p.Runner
	if runner == nil {
		runner = deps.RealRunner{}
	}
	program := p.Program
	if program == "" {
		program = "python3"
	}
	path, err := runner.LookPath(program)
	if err != nil {
		return nil, fmt.Errorf("cmdparse: %s not found on PATH: %w", program, err)
	}
	input, err := json.Marshal(snippets)
	if err != nil {
		return nil, fmt.Errorf("cmdparse: encode %d python snippets: %w", len(snippets), err)
	}
	res, err := runner.Run(ctx, []string{path, "-c", pyscanScript}, deps.RunOptions{Stdin: input})
	if err != nil {
		return nil, fmt.Errorf("cmdparse: run %s: %w", path, err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("cmdparse: %s exited %d: %s", path, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	var out []PyResult
	if err := json.Unmarshal(res.Stdout, &out); err != nil {
		return nil, fmt.Errorf("cmdparse: decode %s output (%d bytes): %w", path, len(res.Stdout), err)
	}
	if len(out) != len(snippets) {
		return nil, fmt.Errorf("cmdparse: %s answered %d results for %d snippets", path, len(out), len(snippets))
	}
	for i := range out {
		if out[i].ID != snippets[i].ID {
			return nil, fmt.Errorf("cmdparse: %s result %d has id %q, want %q", path, i, out[i].ID, snippets[i].ID)
		}
	}
	return out, nil
}

var errNoResult = errors.New("no scan result for snippet")
