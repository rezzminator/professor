package installer

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/tailscale/hujson"
)

// The editor accepts JSONC, so installer edits must not round-trip the whole
// document through encoding/json and erase the operator's comments. hujson
// supplies the byte offsets; the edits splice only the bytes of one property.
type jsoncProperty struct {
	name                 string
	keyStart, valueStart int
	valueEnd, commaStart int
	commaEnd             int
}

type jsoncObject struct {
	close      int
	properties []jsoncProperty
	byName     map[string]jsoncProperty
}

func decodeJSONCObject(raw []byte) (map[string]any, error) {
	root, err := parseJSONC(raw)
	if err != nil {
		return nil, err
	}
	if _, err := jsoncObjectAt(raw, &root, 0); err != nil {
		return nil, err
	}
	root.Standardize()
	var document map[string]any
	if err := json.Unmarshal(root.Pack(), &document); err != nil {
		return nil, err
	}
	return document, nil
}

// parseJSONC parses a copy of raw, so offsets index raw and Standardize never
// blanks the caller's bytes. The copy ends in a newline, since hujson refuses a
// line comment that ends the file, and every comma directly after another comma
// is blanked: VS Code reads `1,,}`, the leftover of two hand edits landing on
// the same spot, and hujson does not.
func parseJSONC(raw []byte) (hujson.Value, error) {
	clean := append(append([]byte(nil), raw...), '\n')
	afterComma := false
	for index := 0; index < len(clean); index++ {
		switch character := clean[index]; {
		case character == '"':
			afterComma = false
			for index++; index < len(clean) && clean[index] != '"'; index++ {
				if clean[index] == '\\' {
					index++
				}
			}
		case bytes.HasPrefix(clean[index:], []byte("//")):
			index += bytes.IndexByte(clean[index:], '\n')
		case bytes.HasPrefix(clean[index:], []byte("/*")):
			end := bytes.Index(clean[index+2:], []byte("*/"))
			if end < 0 {
				return hujson.Parse(clean) // reports the unterminated comment
			}
			index += end + 3
		case character == ',':
			if afterComma {
				clean[index] = ' '
			}
			afterComma = true
		case !isJSONWhitespace(character):
			afterComma = false
		}
	}
	return hujson.Parse(clean)
}

func parseJSONCObject(raw []byte, start int) (jsoncObject, error) {
	root, err := parseJSONC(raw)
	if err != nil {
		return jsoncObject{}, err
	}
	return jsoncObjectAt(raw, &root, start)
}

// jsoncObjectAt reads the object that is the first value at or after start:
// the root for 0, a member for its valueStart.
func jsoncObjectAt(raw []byte, root *hujson.Value, start int) (jsoncObject, error) {
	for value := range root.All() {
		if value.StartOffset < start {
			continue
		}
		object, ok := value.Value.(*hujson.Object)
		if !ok {
			return jsoncObject{}, fmt.Errorf("expected object at byte %d", value.StartOffset)
		}
		result := jsoncObject{close: value.EndOffset - 1, byName: map[string]jsoncProperty{}}
		for index := range object.Members {
			member := &object.Members[index]
			name := member.Name.Value.(hujson.Literal).String()
			if _, duplicate := result.byName[name]; duplicate {
				return jsoncObject{}, fmt.Errorf("duplicate object key %q", name)
			}
			property := jsoncProperty{
				name:       name,
				keyStart:   member.Name.StartOffset,
				valueStart: member.Value.StartOffset,
				valueEnd:   member.Value.EndOffset,
				commaStart: -1,
				commaEnd:   -1,
			}
			if comma := member.Value.EndOffset + len(member.Value.AfterExtra); comma < len(raw) && raw[comma] == ',' {
				property.commaStart, property.commaEnd = comma, comma+1
				// The span takes every comma parseJSONC blanked after this
				// one, so a removal never leaves a stray comma behind.
				for next := property.commaEnd; next < len(raw); next++ {
					if raw[next] == ',' {
						property.commaEnd = next + 1
					} else if !isJSONWhitespace(raw[next]) {
						break
					}
				}
			}
			result.properties = append(result.properties, property)
			result.byName[name] = property
		}
		return result, nil
	}
	return jsoncObject{}, fmt.Errorf("expected object at byte %d", start)
}

func setJSONCProperty(raw []byte, objectStart int, name string, value []byte) ([]byte, error) {
	if !json.Valid(value) {
		return nil, fmt.Errorf("replacement for %q is not JSON", name)
	}
	object, err := parseJSONCObject(raw, objectStart)
	if err != nil {
		return nil, err
	}
	if property, found := object.byName[name]; found {
		return spliceBytes(raw, property.valueStart, property.valueEnd, value), nil
	}
	closingIndent := lineIndent(raw, object.close)
	childIndent := closingIndent + "  "
	formatted := indentJSON(value, childIndent)
	prefix := ""
	if len(object.properties) != 0 && object.properties[len(object.properties)-1].commaStart < 0 {
		prefix = ","
	}
	// No trailing comma after the inserted property: it is the new last
	// member, sitting directly against the closing bracket, so the result
	// is valid strict JSON there, not merely JSONC. A LATER insertion into
	// the same object still lands correctly — it is the "last property has
	// no comma" case the prefix check above already exists to handle.
	insertion := []byte(
		prefix + "\n" + childIndent + string(mustJSON(name)) + ": " + string(formatted) + "\n" + closingIndent,
	)
	return spliceBytes(raw, object.close, object.close, insertion), nil
}

func removeJSONCProperty(raw []byte, objectStart int, name string) ([]byte, error) {
	object, err := parseJSONCObject(raw, objectStart)
	if err != nil {
		return nil, err
	}
	property, found := object.byName[name]
	if !found {
		return raw, nil
	}
	if property.commaStart >= 0 {
		return spliceBytes(raw, property.keyStart, property.commaEnd, nil), nil
	}
	// A formatter may have removed the trailing comma PFM writes. Remove only
	// the preceding separator byte; comments and whitespace around it survive.
	for index := len(object.properties) - 1; index >= 0; index-- {
		prior := object.properties[index]
		if prior.keyStart >= property.keyStart {
			continue
		}
		withoutProperty := spliceBytes(raw, property.keyStart, property.valueEnd, nil)
		if prior.commaStart >= 0 {
			return spliceBytes(withoutProperty, prior.commaStart, prior.commaEnd, nil), nil
		}
		break
	}
	return spliceBytes(raw, property.keyStart, property.valueEnd, nil), nil
}

func isJSONWhitespace(character byte) bool {
	return character == ' ' || character == '\t' || character == '\r' || character == '\n'
}

func spliceBytes(raw []byte, start, end int, replacement []byte) []byte {
	result := make([]byte, 0, len(raw)-(end-start)+len(replacement))
	result = append(result, raw[:start]...)
	result = append(result, replacement...)
	result = append(result, raw[end:]...)
	return result
}

func lineIndent(raw []byte, position int) string {
	start := bytes.LastIndexByte(raw[:position], '\n') + 1
	end := start
	for end < position && (raw[end] == ' ' || raw[end] == '\t') {
		end++
	}
	return string(raw[start:end])
}

func indentJSON(raw []byte, indent string) []byte {
	return bytes.ReplaceAll(raw, []byte("\n"), []byte("\n"+indent))
}

func mustJSON(value string) []byte {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return encoded
}
