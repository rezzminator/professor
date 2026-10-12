package transcript

import (
	"encoding/json"
	"testing"
)

func TestFlattenPromptText(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "string content",
			content: `"hello"`,
			want:    "hello",
		},
		{
			name:    "string whitespace is flattened",
			content: "\"line one\\n\\tline two\"",
			want:    "line one line two",
		},
		{
			name:    "top-level text blocks join",
			content: `[{"type":"text","text":"one"},{"type":"text","text":"two"}]`,
			want:    "one two",
		},
		{
			name:    "tool results never contribute",
			content: `[{"type":"text","text":"before"},{"type":"tool_result","text":"secret"},{"type":"text","text":"after"}]`,
			want:    "before after",
		},
		{
			name:    "nested text never contributes",
			content: `[{"type":"container","content":[{"type":"text","text":"nested"}]},{"type":"text","text":"top"}]`,
			want:    "top",
		},
		{
			name:    "top-level strings are not text blocks",
			content: `["ignored",{"type":"text","text":"kept"}]`,
			want:    "kept",
		},
		{
			name:    "array whitespace is flattened after joining",
			content: `[{"type":"text","text":"one\n"},{"type":"text","text":"\ttwo"}]`,
			want:    "one   two",
		},
		{
			name:    "empty array",
			content: `[]`,
		},
		{
			name:    "null",
			content: `null`,
		},
		{
			name:    "object is not content",
			content: `{"type":"text","text":"not an array"}`,
		},
		{
			name:    "invalid JSON",
			content: `[{"type":`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := FlattenPromptText(json.RawMessage(test.content))
			if got != test.want {
				t.Fatalf("FlattenPromptText() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDecodeContentKeepsEveryElementInPlace(t *testing.T) {
	blocks, isList := DecodeContent(json.RawMessage(`[{"type":"text","text":"a"}, 7, {"type":3,"id":"x"}, "s"]`))
	if !isList || len(blocks) != 4 {
		t.Fatalf("DecodeContent = %d blocks, isList %t; want 4, true", len(blocks), isList)
	}
	got := []struct {
		kind   string
		object bool
		raw    string
	}{
		{blocks[0].Kind(), blocks[0].IsObject(), string(blocks[0].Raw)},
		{blocks[1].Kind(), blocks[1].IsObject(), string(blocks[1].Raw)},
		{blocks[2].Kind(), blocks[2].IsObject(), string(blocks[2].ID)},
		{blocks[3].Kind(), blocks[3].IsObject(), string(blocks[3].Raw)},
	}
	want := []struct {
		kind   string
		object bool
		raw    string
	}{{"text", true, `{"type":"text","text":"a"}`}, {"", false, "7"}, {"", true, `"x"`}, {"", false, `"s"`}}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("block %d = %+v, want %+v", index, got[index], want[index])
		}
	}
	for _, content := range []string{`"text"`, `{"type":"text"}`, ``, `null`, `[`} {
		if blocks, isList := DecodeContent(json.RawMessage(content)); isList || blocks != nil {
			t.Errorf("DecodeContent(%s) = %v, %t; want no list", content, blocks, isList)
		}
	}
}

func TestFlatContentIsPythonsFlat(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"absent", ``, ""},
		{"null", `null`, ""},
		{"string", `"just text"`, "just text"},
		{"one object is a one-block list", `{"type":"text","text":"solo"}`, "solo"},
		{"number is its str", `1.50`, "1.5"},
		{"bool is its str", `true`, "True"},
		{"string elements and text blocks join by newline", `["a",{"type":"text","text":"b"}]`, "a\nb"},
		{"images", `[{"type":"image"},{"type":"input_image","image_url":"x"}]`, "[image]\n[image]"},
		{
			"tool reference",
			`[{"type":"tool_reference","tool_name":"Read"},{"type":"tool_reference"}]`,
			"[tool Read]\n[tool ]",
		},
		{"other block is its JSON in written order", `[{"z":1,"a":"é"}]`, `{"z": 1, "a": "é"}`},
		{"a non-string text is no text", `[{"type":"text","text":5}]`, `{"type": "text", "text": 5}`},
		{"numbers and lists carry nothing", `[1,[2],"c"]`, "c"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := FlatContent(json.RawMessage(test.content)); got != test.want {
				t.Fatalf("FlatContent(%s) = %q, want %q", test.content, got, test.want)
			}
		})
	}
}
