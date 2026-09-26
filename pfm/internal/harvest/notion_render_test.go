package harvest

import (
	"encoding/json"
	"net/url"
	"testing"
)

// TestNotionRichTextKeepsItsMarks: a captured block's rich text renders as
// inline Markdown — a link, italics, and bold kept or, for a toggle's
// summary (bold already), left out — its runs joined as the site shows them.
func TestNotionRichTextKeepsItsMarks(t *testing.T) {
	var records struct {
		RecordMap notionRecordMap `json:"recordMap"`
	}
	if err := json.Unmarshal([]byte(socialFixture(t, "notion/records.json")), &records); err != nil {
		t.Fatalf("records fixture: %v", err)
	}
	var chunk notionAnswer
	if err := json.Unmarshal([]byte(socialFixture(t, "notion/chunk-0.json")), &chunk); err != nil {
		t.Fatalf("chunk fixture: %v", err)
	}
	state := &notionPage{blocks: records.RecordMap.Block}
	for id, record := range chunk.RecordMap.Block {
		state.blocks[id] = record
	}
	base, err := url.Parse(notionURL)
	if err != nil {
		t.Fatal(err)
	}
	renderer := notionRenderer{state: state, base: base, seen: map[string]bool{}, rendered: map[string]bool{}}
	callout := state.block("aa69255b-583a-46c1-a14c-260f74320d36")
	toggle := state.block("b0b67343-abfa-427b-8a65-8e05aedd11a3")
	if callout == nil || toggle == nil {
		t.Fatal("the fixture lacks the captured callout or toggle")
	}
	for _, check := range []struct{ name, got, want string }{
		{
			"callout", renderer.rich(callout.Properties["title"], false),
			"b223 callout[b223 callout part 2](https://example.com/link-29)b223 callout part 3*b223 callout part 4*",
		},
		{"toggle summary", renderer.rich(toggle.Properties["title"], true), "b008 toggle"},
		{"bold text", renderer.rich(toggle.Properties["title"], false), "**b008 toggle**"},
	} {
		if check.got != check.want {
			t.Errorf("%s: got %q, want %q", check.name, check.got, check.want)
		}
	}
}
