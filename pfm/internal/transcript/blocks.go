package transcript

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/naming"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
)

// TextBlock is a content block's type when it carries prose.
const TextBlock = "text"

// ContentBlock is one block of a message's content array, as Claude and Codex
// write it — the one decoder of a block in this tree. Every member a reader
// tests is kept raw, so a member of an unexpected type reads as absent to one
// reader and as written to another; Raw is the block itself.
type ContentBlock struct {
	Type      json.RawMessage `json:"type"`
	ID        json.RawMessage `json:"id"`
	Name      json.RawMessage `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID json.RawMessage `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   json.RawMessage `json:"is_error"`
	Text      json.RawMessage `json:"text"`
	ToolName  json.RawMessage `json:"tool_name"`
	Raw       json.RawMessage `json:"-"`
}

// Kind is the block's type when it is a string, else "".
func (block *ContentBlock) Kind() string {
	kind, _ := pyvalue.String(block.Type)
	return kind
}

// IsObject reports a block written as a JSON object; an array element of any
// other form carries only Raw.
func (block *ContentBlock) IsObject() bool {
	raw := bytes.TrimSpace(block.Raw)
	return len(raw) != 0 && raw[0] == '{'
}

// DecodeContent decodes a content array block by block: an element that is
// no object is kept with only its Raw, so one odd element never hides the
// others. isList is false for every other form (a string, an object, absent).
func DecodeContent(raw json.RawMessage) (blocks []ContentBlock, isList bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '[' {
		return nil, false
	}
	var elements []json.RawMessage
	if json.Unmarshal(raw, &elements) != nil {
		return nil, false
	}
	blocks = make([]ContentBlock, len(elements))
	for index, element := range elements {
		element = bytes.TrimSpace(element)
		if len(element) != 0 && element[0] == '{' {
			_ = json.Unmarshal(element, &blocks[index])
		}
		blocks[index].Raw = element
	}
	return blocks, true
}

// FlatContent is a content value as one text: a string as itself, an object
// as a one-block list, each block by its text, an image as "[image]", a tool
// reference as "[tool NAME]", any other block as its JSON, joined by newlines;
// a value of any other form is its str(). Elements that are neither strings
// nor objects carry no text.
func FlatContent(content json.RawMessage) string {
	content = bytes.TrimSpace(content)
	switch {
	case pyvalue.IsNull(content):
		return ""
	case content[0] == '"':
		text, _ := pyvalue.String(content)
		return text
	case content[0] == '{':
		content = json.RawMessage("[" + string(content) + "]")
	case content[0] != '[':
		return pyvalue.Str(content)
	}
	blocks, _ := DecodeContent(content)
	parts := make([]string, 0, len(blocks))
	for index := range blocks {
		block := &blocks[index]
		if text, ok := pyvalue.String(block.Raw); ok {
			parts = append(parts, text)
			continue
		}
		if !block.IsObject() {
			continue
		}
		switch text, isText := pyvalue.String(block.Text); {
		case isText:
			parts = append(parts, text)
		case block.Kind() == "image" || block.Kind() == "input_image":
			parts = append(parts, "[image]")
		case block.Kind() == "tool_reference":
			name := ""
			if block.ToolName != nil {
				name = pyvalue.Str(block.ToolName)
			}
			parts = append(parts, "[tool "+name+"]")
		default:
			parts = append(parts, pyvalue.Dumps(block.Raw))
		}
	}
	return strings.Join(parts, "\n")
}

// FlattenPromptText extracts a user message's content. JSON strings become
// one line; arrays keep the text-bearing blocks only ("text", Claude's, and
// "input_text", the Codex response_item user block — the only record current
// Codex writes a submitted prompt into), joined by spaces. A block whose type
// or text is of another type carries no text.
func FlattenPromptText(content json.RawMessage) string {
	var text string
	if err := json.Unmarshal(content, &text); err == nil {
		return naming.SquashTabsAndNewlines(text)
	}
	blocks, ok := DecodeContent(content)
	if !ok {
		return ""
	}
	texts := make([]string, 0, len(blocks))
	for index := range blocks {
		block := &blocks[index]
		if kind := block.Kind(); kind != TextBlock && kind != "input_text" {
			continue
		}
		text, isText := pyvalue.String(block.Text)
		if !isText && !pyvalue.IsNull(block.Text) {
			continue
		}
		texts = append(texts, text)
	}
	return naming.SquashTabsAndNewlines(strings.Join(texts, " "))
}
