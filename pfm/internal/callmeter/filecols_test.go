package callmeter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// fileColsCell renders one pointer column for the table: <NULL> when absent.
func fileColsCell[T any](p *T) string {
	if p == nil {
		return "<NULL>"
	}
	return fmt.Sprint(*p)
}

func TestFileColumns(t *testing.T) {
	cases := []struct {
		name, tool, input, cwd, result string
		// path start lines total before, space-joined
		want, err string
	}{
		{
			"absolute read, no result", "Read", `{"file_path":"/tmp/demo-proj/a.md"}`, "/tmp/demo-proj", "",
			"/tmp/demo-proj/a.md 1 <NULL> <NULL> <NULL>", "",
		},
		{
			"relative ranged read, no result", "Read", `{"file_path":"a.md","offset":7,"limit":3}`,
			"/tmp/demo-proj", "", "/tmp/demo-proj/a.md 7 3 <NULL> <NULL>", "",
		},
		{
			"ranged read, the result wins field by field", "Read",
			`{"file_path":"/tmp/demo-proj/a.md","offset":7,"limit":3}`, "",
			`{"type":"text","file":{"startLine":4,"totalLines":9}}`, "/tmp/demo-proj/a.md 4 3 9 <NULL>", "",
		},
		{
			"read result without file keeps the input", "Read", `{"file_path":"/tmp/demo-proj/a.md","limit":5}`, "",
			`{"type":"text"}`, "/tmp/demo-proj/a.md 1 5 <NULL> <NULL>", "",
		},
		{
			"offset 0 reads from line 1, a fraction truncates", "Read",
			`{"file_path":"/tmp/demo-proj/a.md","offset":0,"limit":2.9}`, "", "",
			"/tmp/demo-proj/a.md 1 2 <NULL> <NULL>", "",
		},
		{
			"relative with no cwd stays relative", "Write", `{"file_path":"a.md","content":"x"}`, "", "",
			"a.md <NULL> <NULL> <NULL> <NULL>", "",
		},
		{
			"write create with null originalFile", "Write", `{"file_path":"/tmp/demo-proj/out.md","content":"x"}`, "",
			`{"type":"create","originalFile":null}`, "/tmp/demo-proj/out.md <NULL> <NULL> <NULL> 0", "",
		},
		{
			"write update", "Write", `{"file_path":"/tmp/demo-proj/out.md","content":"x"}`, "",
			`{"type":"update","originalFile":"abcd"}`, "/tmp/demo-proj/out.md <NULL> <NULL> <NULL> 4", "",
		},
		{
			"edit with originalFile", "Edit", `{"file_path":"n.md","old_string":"a","new_string":"b"}`,
			"/tmp/demo-proj", `{"filePath":"/tmp/demo-proj/n.md","originalFile":"ab"}`,
			"/tmp/demo-proj/n.md <NULL> <NULL> <NULL> 2", "",
		},
		{
			"edit without originalFile", "Edit", `{"file_path":"/tmp/demo-proj/n.md"}`, "",
			`{"filePath":"/tmp/demo-proj/n.md"}`, "/tmp/demo-proj/n.md <NULL> <NULL> <NULL> <NULL>", "",
		},
		{
			"multi edit", "MultiEdit", `{"file_path":"/tmp/demo-proj/a.md","edits":[]}`, "",
			`{"originalFile":"abc"}`, "/tmp/demo-proj/a.md <NULL> <NULL> <NULL> <NULL>", "",
		},
		{
			"notebook", "NotebookEdit", `{"notebook_path":"n.ipynb","new_source":""}`, "/tmp/demo-proj", "",
			"/tmp/demo-proj/n.ipynb <NULL> <NULL> <NULL> <NULL>", "",
		},
		{
			"not a file tool", "Bash", `{"file_path":"/tmp/demo-proj/a.md"}`, "", `{"originalFile":"ab"}`,
			"<NULL> <NULL> <NULL> <NULL> <NULL>", "",
		},
		{
			"no path", "Edit", `{"old_string":"a"}`, "/tmp/demo-proj", "",
			"<NULL> <NULL> <NULL> <NULL> <NULL>", "Edit input has no file_path",
		},
		{
			"notebook naming file_path only", "NotebookEdit", `{"file_path":"n.ipynb"}`, "", "",
			"<NULL> <NULL> <NULL> <NULL> <NULL>", "NotebookEdit input has no notebook_path",
		},
		{
			"undecodable input", "Read", `{"file_path":7}`, "", "",
			"<NULL> <NULL> <NULL> <NULL> <NULL>", "decode Read input",
		},
		{
			"empty input", "Write", ``, "", "",
			"<NULL> <NULL> <NULL> <NULL> <NULL>", "decode Write input",
		},
		{
			"undecodable result", "Read", `{"file_path":"/tmp/demo-proj/a.md"}`, "", `{"file":{"startLine":"x"}}`,
			"/tmp/demo-proj/a.md 1 <NULL> <NULL> <NULL>", "decode Read result",
		},
	}
	for _, tc := range cases {
		var c Call
		err := FileColumnsFromInput(&c, tc.tool, json.RawMessage(tc.input), tc.cwd)
		if err == nil && tc.result != "" {
			err = FileColumnsFromResult(&c, tc.tool, json.RawMessage(tc.result))
		}
		if tc.err == "" && err != nil || tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)) {
			t.Errorf("%s: err = %v, want %q", tc.name, err, tc.err)
		}
		got := strings.Join([]string{
			fileColsCell(c.FilePath), fileColsCell(c.ReadStart), fileColsCell(c.ReadLines),
			fileColsCell(c.ReadTotalLines), fileColsCell(c.FileBytesBefore),
		}, " ")
		if got != tc.want {
			t.Errorf("%s: path/start/lines/total/before = %s, want %s", tc.name, got, tc.want)
		}
		if c.FileBytes != nil {
			t.Errorf("%s: file_bytes = %d, want <NULL>: the caller stats the file", tc.name, *c.FileBytes)
		}
	}
}
