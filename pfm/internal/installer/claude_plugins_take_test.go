package installer

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestClaudeRecordEditTake: a take deletes and names, sorted, only the keys
// its edit owns, from its member or the document itself; an emptied member
// goes only when the edit says so, and a record holding none is left as it is.
func TestClaudeRecordEditTake(t *testing.T) {
	ours := func(key string) bool { return strings.HasSuffix(key, "@professor") }
	for _, tc := range []struct {
		name, document, want string
		edit                 claudeRecordEdit
		taken                []string
	}{
		{
			name:     "the owned keys of a member go and the owner's stay",
			edit:     claudeRecordEdit{member: "enabledPlugins", ours: ours, dropEmpty: true},
			document: `{"enabledPlugins":{"zeta@professor":false,"mine@other":true,"alpha@professor":true},"theme":"dark"}`,
			want:     `{"enabledPlugins":{"mine@other":true},"theme":"dark"}`,
			taken:    []string{"alpha@professor", "zeta@professor"},
		},
		{
			name:     "an emptied member goes when the edit drops it",
			edit:     claudeRecordEdit{member: "enabledPlugins", ours: ours, dropEmpty: true},
			document: `{"enabledPlugins":{"alpha@professor":true},"theme":"dark"}`,
			want:     `{"theme":"dark"}`,
			taken:    []string{"alpha@professor"},
		},
		{
			name:     "an emptied member stays when the edit keeps it",
			edit:     claudeRecordEdit{member: "plugins", ours: ours},
			document: `{"version":2,"plugins":{"alpha@professor":[]}}`,
			want:     `{"version":2,"plugins":{}}`,
			taken:    []string{"alpha@professor"},
		},
		{
			name:     "an edit with no member takes from the document itself",
			edit:     claudeRecordEdit{ours: func(key string) bool { return key == "professor" }},
			document: `{"professor":{},"other":{}}`,
			want:     `{"other":{}}`,
			taken:    []string{"professor"},
		},
		{
			name:     "a record without the member is left as it is",
			edit:     claudeRecordEdit{member: "enabledPlugins", ours: ours, dropEmpty: true},
			document: `{"theme":"dark"}`,
			want:     `{"theme":"dark"}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var document, want map[string]any
			if err := json.Unmarshal([]byte(tc.document), &document); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			taken := tc.edit.take(document)
			if !reflect.DeepEqual(taken, tc.taken) || !reflect.DeepEqual(document, want) {
				t.Fatalf("take(%s) = %q, leaving %v\nwant %q, leaving %v", tc.document, taken, document, tc.taken, want)
			}
		})
	}
}
