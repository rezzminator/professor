package installer

import (
	"encoding/json"
	"testing"
)

func TestSetTopLevelKey(t *testing.T) {
	for _, row := range []struct {
		name, raw, want, err string
	}{
		{
			name: "an absent key goes after the last member, indented like it",
			raw:  "{\n  \"a\": 1,\n  \"b\": {\"k\": 2}\n}\n",
			want: "{\n  \"a\": 1,\n  \"b\": {\"k\": 2},\n  \"k\": false\n}\n",
		},
		{
			name: "every member of the name is replaced by one",
			raw:  "{\"k\": true, \"a\": 1, \"k\": 3}",
			want: "{\"a\": 1,\"k\": false}",
		},
		{
			name: "a nested member of the name is left alone",
			raw:  "{\"a\": {\"k\": true}}",
			want: "{\"a\": {\"k\": true},\"k\": false}",
		},
		{name: "an empty object gains the member", raw: "{}", want: "{\"k\": false}"},
		{name: "a top level that is not an object is refused", raw: "[1]", err: "decode: top level is [, not an object"},
	} {
		t.Run(row.name, func(t *testing.T) {
			got, err := setTopLevelKey([]byte(row.raw), "k", json.RawMessage("false"))
			if row.err != "" {
				if err == nil || err.Error() != row.err {
					t.Fatalf("setTopLevelKey(%q) error = %v, want %q", row.raw, err, row.err)
				}
				return
			}
			if err != nil || string(got) != row.want {
				t.Fatalf("setTopLevelKey(%q) = %q, %v, want %q", row.raw, got, err, row.want)
			}
		})
	}
}
