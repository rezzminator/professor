package installer

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// The editor accepts JSONC, so installer edits must not round-trip the whole
// document through encoding/json and erase the operator's comments. These
// helpers parse enough structure to replace one object property surgically.
type jsoncProperty struct {
	name                 string
	keyStart, valueStart int
	valueEnd, commaStart int
	commaEnd             int
}

type jsoncObject struct {
	open, close int
	properties  []jsoncProperty
	byName      map[string]jsoncProperty
}

func decodeJSONCObject(raw []byte) (map[string]any, error) {
	sanitized, err := sanitizeJSONC(raw)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(sanitized, &document); err != nil {
		return nil, err
	}
	if _, err := parseJSONCObject(raw, 0); err != nil {
		return nil, err
	}
	return document, nil
}

func sanitizeJSONC(raw []byte) ([]byte, error) {
	clean := append([]byte(nil), raw...)
	inString, escaped := false, false
	for index := 0; index < len(clean); index++ {
		character := clean[index]
		if inString {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				inString = false
			}
			continue
		}
		if character == '"' {
			inString = true
			continue
		}
		if character != '/' || index+1 >= len(clean) {
			continue
		}
		switch clean[index+1] {
		case '/':
			clean[index], clean[index+1] = ' ', ' '
			index += 2
			for index < len(clean) && clean[index] != '\n' {
				clean[index] = ' '
				index++
			}
			index--
		case '*':
			clean[index], clean[index+1] = ' ', ' '
			index += 2
			closed := false
			for index < len(clean) {
				if index+1 < len(clean) && clean[index] == '*' && clean[index+1] == '/' {
					clean[index], clean[index+1] = ' ', ' '
					index++
					closed = true
					break
				}
				if clean[index] != '\n' && clean[index] != '\r' {
					clean[index] = ' '
				}
				index++
			}
			if !closed {
				return nil, errors.New("unterminated block comment")
			}
		}
	}
	if inString {
		return nil, errors.New("unterminated string")
	}
	// A comma is trailing — VS Code accepts it, encoding/json does not — when
	// nothing but whitespace and/or MORE commas separates it from the object
	// or array's closing bracket. A hand edit that leaves two properties
	// each ending in their own comma back to back (a block moved, or two
	// edits landing on the same spot) is exactly this, one comma deeper than
	// the single-comma case a lookahead of whitespace alone would catch.
	for index, character := range clean {
		if character != ',' {
			continue
		}
		next := index + 1
		for next < len(clean) && (isJSONWhitespace(clean[next]) || clean[next] == ',') {
			next++
		}
		if next < len(clean) && (clean[next] == '}' || clean[next] == ']') {
			clean[index] = ' '
		}
	}
	return clean, nil
}

func parseJSONCObject(raw []byte, start int) (jsoncObject, error) {
	open, err := skipJSONCTrivia(raw, start)
	if err != nil {
		return jsoncObject{}, err
	}
	if open >= len(raw) || raw[open] != '{' {
		return jsoncObject{}, fmt.Errorf("expected object at byte %d", open)
	}
	result := jsoncObject{open: open, byName: map[string]jsoncProperty{}}
	index := open + 1
	for {
		index, err = skipJSONCTrivia(raw, index)
		if err != nil {
			return jsoncObject{}, err
		}
		if index >= len(raw) {
			return jsoncObject{}, errors.New("unterminated object")
		}
		if raw[index] == '}' {
			result.close = index
			return result, nil
		}
		if raw[index] == ',' { // trailing commas and the separator after a prior member
			index++
			continue
		}
		if raw[index] != '"' {
			return jsoncObject{}, fmt.Errorf("expected object key at byte %d", index)
		}
		keyStart := index
		keyEnd, err := scanJSONString(raw, index)
		if err != nil {
			return jsoncObject{}, err
		}
		var name string
		if err := json.Unmarshal(raw[index:keyEnd], &name); err != nil {
			return jsoncObject{}, err
		}
		if _, duplicate := result.byName[name]; duplicate {
			return jsoncObject{}, fmt.Errorf("duplicate object key %q", name)
		}
		index, err = skipJSONCTrivia(raw, keyEnd)
		if err != nil || index >= len(raw) || raw[index] != ':' {
			return jsoncObject{}, fmt.Errorf("expected colon after %q", name)
		}
		valueStart, err := skipJSONCTrivia(raw, index+1)
		if err != nil {
			return jsoncObject{}, err
		}
		valueEnd, err := scanJSONCValue(raw, valueStart)
		if err != nil {
			return jsoncObject{}, fmt.Errorf("scan %q: %w", name, err)
		}
		after, err := skipJSONCTrivia(raw, valueEnd)
		if err != nil {
			return jsoncObject{}, err
		}
		property := jsoncProperty{
			name:       name,
			keyStart:   keyStart,
			valueStart: valueStart,
			valueEnd:   valueEnd,
			commaStart: -1,
			commaEnd:   -1,
		}
		if after < len(raw) && raw[after] == ',' {
			property.commaStart, property.commaEnd = after, after+1
			index = after + 1
		} else if after < len(raw) && raw[after] == '}' {
			index = after
		} else {
			return jsoncObject{}, fmt.Errorf("expected comma or object end after %q", name)
		}
		result.properties = append(result.properties, property)
		result.byName[name] = property
	}
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

func scanJSONCValue(raw []byte, start int) (int, error) {
	if start >= len(raw) {
		return 0, errors.New("missing value")
	}
	if raw[start] == '"' {
		return scanJSONString(raw, start)
	}
	if raw[start] == '{' || raw[start] == '[' {
		stack := []byte{raw[start]}
		inString, escaped := false, false
		for index := start + 1; index < len(raw); index++ {
			character := raw[index]
			if inString {
				if escaped {
					escaped = false
				} else if character == '\\' {
					escaped = true
				} else if character == '"' {
					inString = false
				}
				continue
			}
			if character == '"' {
				inString = true
				continue
			}
			if character == '/' && index+1 < len(raw) && (raw[index+1] == '/' || raw[index+1] == '*') {
				next, err := skipOneJSONCComment(raw, index)
				if err != nil {
					return 0, err
				}
				index = next - 1
				continue
			}
			switch character {
			case '{', '[':
				stack = append(stack, character)
			case '}', ']':
				open := stack[len(stack)-1]
				if (open == '{' && character != '}') || (open == '[' && character != ']') {
					return 0, fmt.Errorf("mismatched %c at byte %d", character, index)
				}
				stack = stack[:len(stack)-1]
				if len(stack) == 0 {
					return index + 1, nil
				}
			}
		}
		return 0, errors.New("unterminated composite value")
	}
	index := start
	for index < len(raw) && raw[index] != ',' && raw[index] != '}' && raw[index] != ']' && !isJSONWhitespace(raw[index]) {
		if raw[index] == '/' && index+1 < len(raw) && (raw[index+1] == '/' || raw[index+1] == '*') {
			break
		}
		index++
	}
	if index == start {
		return 0, errors.New("empty primitive value")
	}
	return index, nil
}

func scanJSONString(raw []byte, start int) (int, error) {
	escaped := false
	for index := start + 1; index < len(raw); index++ {
		if escaped {
			escaped = false
			continue
		}
		if raw[index] == '\\' {
			escaped = true
			continue
		}
		if raw[index] == '"' {
			return index + 1, nil
		}
	}
	return 0, errors.New("unterminated string")
}

func skipJSONCTrivia(raw []byte, start int) (int, error) {
	index := start
	for index < len(raw) {
		if isJSONWhitespace(raw[index]) {
			index++
			continue
		}
		if raw[index] == '/' && index+1 < len(raw) && (raw[index+1] == '/' || raw[index+1] == '*') {
			next, err := skipOneJSONCComment(raw, index)
			if err != nil {
				return 0, err
			}
			index = next
			continue
		}
		break
	}
	return index, nil
}

func skipOneJSONCComment(raw []byte, start int) (int, error) {
	if raw[start+1] == '/' {
		index := start + 2
		for index < len(raw) && raw[index] != '\n' {
			index++
		}
		return index, nil
	}
	for index := start + 2; index+1 < len(raw); index++ {
		if raw[index] == '*' && raw[index+1] == '/' {
			return index + 2, nil
		}
	}
	return 0, errors.New("unterminated block comment")
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
