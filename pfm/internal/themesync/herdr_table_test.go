package themesync

import "testing"

// TestHerdrTableSpanHoldsTheWholeTableOrRefuses pins the span a block replaces:
// the table and its subtables, trailing comments left outside, and no span
// for a table set at the root or split around another table.
func TestHerdrTableSpanHoldsTheWholeTableOrRefuses(t *testing.T) {
	cases := []struct {
		name, content, span, err string
		found                    bool
	}{
		{name: "absent", content: "[theme]\nname = 'x'\n"},
		{
			name:    "table and subtables",
			found:   true,
			content: "[theme]\nname = 'x'\n\n[keys]\nnext_workspace = 'prefix+m'\n\n[[keys.command]]\nkey = 'prefix+a'\n# trailing\n\n[ui]\n",
			span:    "[keys]\nnext_workspace = 'prefix+m'\n\n[[keys.command]]\nkey = 'prefix+a'\n",
		},
		{
			name:    "root dotted key",
			content: "keys.next_workspace = 'prefix+m'\n[theme]\n",
			err:     `themesync: herdr config sets keys at the root (line 1: "keys.next_workspace = 'prefix+m'"); move it under a [keys] table`,
		},
		{
			name:    "split",
			content: "[keys]\na = 1\n[ui]\n[[keys.command]]\nkey = 'x'\n",
			err:     `themesync: herdr config splits its keys tables around line 3 ("[ui]"); keep [keys] and [keys.*] together`,
		},
	}
	for _, testCase := range cases {
		start, end, found, err := HerdrTableSpan(testCase.content, "keys")
		gotErr := ""
		if err != nil {
			gotErr = err.Error()
		}
		span := ""
		if found {
			span = testCase.content[start:end]
		}
		if found != testCase.found || span != testCase.span || gotErr != testCase.err {
			t.Errorf("%s: found=%v span=%q err=%q\nwant found=%v span=%q err=%q",
				testCase.name, found, span, gotErr, testCase.found, testCase.span, testCase.err)
		}
	}
}
