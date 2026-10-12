package statusline

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestSubagentTaskIDs(t *testing.T) {
	cases := []struct {
		name, raw string
		want      []string
	}{
		{"whole payload", `{"session_id":"s","tasks":[{"id":"a"},{"id":"b","tokenCount":3}]}`, []string{"a", "b"}},
		{"torn after a task", `{"tasks":[{"id":"a"},{"id":"b"},{"id":"c","label":"half`, []string{"a", "b"}},
		{"tasks before other keys", `{"tasks":[{"id":"a"}],"columns":"wide"`, []string{"a"}},
		{"another key first", `{"transcript_path":"/x","cwd":"/y","tasks":[{"id":"a"}`, []string{"a"}},
		{
			"another field of the wrong type keeps the row",
			`{"tasks":[{"id":"a","tokenCount":"x"},{"id":"b"}]}`,
			[]string{"a", "b"},
		},
		{"an id of the wrong type skips only its task", `{"tasks":[{"id":7},"x",{"id":"b"}]}`, []string{"b"}},
		{"no id, no row", `{"tasks":[{"label":"x"},{"id":""},{"id":"b"}]}`, []string{"b"}},
		{"a repeated id shows once", `{"tasks":[{"id":"a"},{"id":"a"}]}`, []string{"a"}},
		{"no tasks yet", `{"tasks":`, nil},
		{"not an object", `[1,2`, nil},
		{"empty", ``, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := subagentTaskIDs([]byte(tc.raw))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("subagentTaskIDs(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}

// The cap is a fault only past it: a panel of exactly maxSubagentInput bytes
// reads whole, and one past the old 1 MiB cap reads whole.
func TestReadSubagentInputCap(t *testing.T) {
	cases := []struct {
		name    string
		size    int
		wantErr string
	}{
		{"past the old 1 MiB cap", 2 << 20, ""},
		{"exactly the cap", maxSubagentInput, ""},
		{"one byte past the cap", maxSubagentInput + 1, "read input: over 16 MiB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := readSubagentInput(bytes.NewReader(bytes.Repeat([]byte("x"), tc.size)))
			if tc.wantErr == "" {
				if err != nil || len(raw) != tc.size {
					t.Fatalf("read %d of %d bytes, err %v; want all, no error", len(raw), tc.size, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) || len(raw) != maxSubagentInput {
				t.Fatalf("read %d bytes, err %v; want the cap's %d and %q", len(raw), err, maxSubagentInput, tc.wantErr)
			}
		})
	}
}

// failingReader yields its data, then the error.
type failingReader struct {
	data []byte
	err  error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}

// A fault never renders as absence: every task row whose id can still be read
// shows the cause, the cause goes to stderr, and the exit stays the fail-open 0.
// Input with no readable id has no row to carry it, and stderr says so.
func TestServeSubagentsShowsAFaultOnEveryRow(t *testing.T) {
	const rowA = `{"id":"t1","model":"claude-opus-5-5","contextWindowSize":1000,"tokenCount":10}`
	const rowB = `{"id":"t2","model":"claude-opus-5-5","contextWindowSize":1000,"tokenCount":20}`
	overflow := `{"tasks":[` + rowA + `,` + rowB + `,{"id":"t3","label":"` +
		strings.Repeat("x", 17<<20) + `"}]}`

	cases := []struct {
		name    string
		input   io.Reader
		rows    []string
		logged  string
		inError string
	}{
		{
			"torn payload", strings.NewReader(`{"tasks":[` + rowA + `,` + rowB),
			[]string{"t1", "t2"},
			"--subagents: render (rows show the error)", "pfm statusline: parse subagent row context",
		},
		{
			"wrong field type", strings.NewReader(`{"tasks":[{"id":"t1","tokenCount":"many"},` + rowB + `]}`),
			[]string{"t1", "t2"},
			"--subagents: render (rows show the error)", "pfm statusline: parse subagent row context",
		},
		{
			"read error", &failingReader{
				data: []byte(`{"tasks":[` + rowA + `,` + rowB + `,{"id":"t3"`),
				err:  errors.New("pipe broke"),
			},
			[]string{"t1", "t2"},
			"--subagents: input (rows show the error)", "pfm statusline: read input: pipe broke",
		},
		{
			"input past the cap", strings.NewReader(overflow),
			[]string{"t1", "t2"},
			"--subagents: input (rows show the error)", "pfm statusline: read input: over 16 MiB",
		},
		{
			"empty input has no row to show it on", strings.NewReader(""),
			nil,
			"--subagents: render (fail-open): no task id to show it on: parse subagent row context", "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			machine := paths.Values{Home: t.TempDir(), SIDDir: t.TempDir()}
			if code := ServeSubagents(tc.input, &stdout, &stderr, machine, noEnv, ""); code != 0 {
				t.Fatalf("code=%d, want 0 (fail-open exit, visible rows)", code)
			}
			if !strings.Contains(stderr.String(), tc.logged) {
				t.Fatalf("stderr=%q, want %q", stderr.String(), tc.logged)
			}
			rows := map[string]string{}
			for _, line := range strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n") {
				if line == "" {
					continue
				}
				var row subagentRow
				if err := json.Unmarshal([]byte(line), &row); err != nil {
					t.Fatalf("line %q is not an {id,content} row: %v", line, err)
				}
				rows[row.ID] = row.Content
			}
			if len(rows) != len(tc.rows) {
				t.Fatalf("rows=%v, want exactly the ids %v", rows, tc.rows)
			}
			for _, id := range tc.rows {
				if !strings.Contains(rows[id], tc.inError) {
					t.Fatalf("row %s=%q, want %q", id, rows[id], tc.inError)
				}
			}
		})
	}
}
