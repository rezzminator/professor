package installer

import (
	"reflect"
	"testing"
)

// TestJSONCReadsAndEditsWhatVSCodeAccepts covers the JSONC a hand-edited
// settings file carries: two (or more) trailing commas in a row — two edits
// landing on the same spot, or a block moved and re-punctuated — comments,
// and commas or brackets inside strings, which must come back verbatim from
// a read and from a read after an edit, because uninstall restores them.
func TestJSONCReadsAndEditsWhatVSCodeAccepts(t *testing.T) {
	t.Parallel()
	for name, row := range map[string]struct {
		content string
		want    map[string]any
	}{
		"double trailing comma in a nested object": {
			`{"a": {"x": 1,,},"b":2}`,
			map[string]any{"a": map[string]any{"x": 1.0}, "b": 2.0},
		},
		"triple trailing comma in a nested object": {
			`{"a": {"x": 1,,,},"b":2}`,
			map[string]any{"a": map[string]any{"x": 1.0}, "b": 2.0},
		},
		"double trailing comma in an array": {
			`{"a": [1, 2,,],"b":2}`,
			map[string]any{"a": []any{1.0, 2.0}, "b": 2.0},
		},
		"double trailing comma spread across lines": {
			"{\"a\": {\"x\": 1,\n  ,\n  },\"b\":2}",
			map[string]any{"a": map[string]any{"x": 1.0}, "b": 2.0},
		},
		"comma before a closing bracket inside strings": {
			`{"x": "a, }", "y": ["b, ]"],"b":2}`,
			map[string]any{"x": "a, }", "y": []any{"b, ]"}, "b": 2.0},
		},
		"quote in a comment and doubled commas in a string": {
			"{\"x\": \"a,,]\", // it's \"quoted\n\"b\":2,,}",
			map[string]any{"x": "a,,]", "b": 2.0},
		},
		"line comment ending the file without a newline": {
			`{"b":2} // last`,
			map[string]any{"b": 2.0},
		},
	} {
		t.Run(name, func(t *testing.T) {
			document, err := decodeJSONCObject([]byte(row.content))
			if err != nil {
				t.Fatalf("decodeJSONCObject(%q): %v", row.content, err)
			}
			if !reflect.DeepEqual(document, row.want) {
				t.Fatalf("read %q\nwant %#v\ngot  %#v", row.content, row.want, document)
			}
			edited, err := setJSONCProperty([]byte(row.content), 0, "b", []byte("3"))
			if err != nil {
				t.Fatalf("setJSONCProperty(%q): %v", row.content, err)
			}
			document, err = decodeJSONCObject(edited)
			if err != nil {
				t.Fatalf("decodeJSONCObject(%q): %v", edited, err)
			}
			row.want["b"] = 3.0
			if !reflect.DeepEqual(document, row.want) {
				t.Fatalf("read after edit %q\nwant %#v\ngot  %#v", edited, row.want, document)
			}
		})
	}
}
