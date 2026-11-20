package installer

import (
	"bytes"
	"encoding/json"
	"fmt"
)

// jsonMember is one top-level member of a JSON object: its key and the byte
// span from the key's opening quote to the end of its value.
type jsonMember struct {
	key        string
	start, end int
}

// removeTopLevelKeys splices every member named in keys out of the JSON object
// raw, leaving every other byte as it was, and reports whether it removed any.
func removeTopLevelKeys(raw []byte, keys ...string) ([]byte, bool, error) {
	removed := false
	for {
		members, err := topLevelMembers(raw)
		if err != nil {
			return nil, false, err
		}
		index := -1
		for position, member := range members {
			for _, key := range keys {
				if member.key == key {
					index = position
					break
				}
			}
			if index >= 0 {
				break
			}
		}
		if index < 0 {
			return raw, removed, nil
		}
		start, end := members[index].start, members[index].end
		switch {
		case index < len(members)-1:
			end = members[index+1].start
		case index > 0:
			start = members[index-1].end
		}
		next := make([]byte, 0, len(raw)-(end-start))
		next = append(append(next, raw[:start]...), raw[end:]...)
		if !json.Valid(next) {
			return nil, false, fmt.Errorf("removing %q left invalid JSON", members[index].key)
		}
		raw, removed = next, true
	}
}

// topLevelMembers lists the members of the JSON object raw in order.
func topLevelMembers(raw []byte) ([]jsonMember, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	open, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	if open != json.Delim('{') {
		return nil, fmt.Errorf("decode: top level is %v, not an object", open)
	}
	var members []jsonMember
	previous := int(decoder.InputOffset())
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("decode key: %w", err)
		}
		key, isString := token.(string)
		if !isString {
			return nil, fmt.Errorf("decode key: %v is not a string", token)
		}
		quote := bytes.IndexByte(raw[previous:decoder.InputOffset()], '"')
		if quote < 0 {
			return nil, fmt.Errorf("decode key %q: opening quote not found", key)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, fmt.Errorf("decode %q: %w", key, err)
		}
		end := int(decoder.InputOffset())
		members = append(members, jsonMember{key: key, start: previous + quote, end: end})
		previous = end
	}
	if _, err := decoder.Token(); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return members, nil
}

// setTopLevelKey gives the JSON object raw exactly one member key holding
// value: every existing member of that name is spliced out and one is added
// after the last member, indented like it. Every other byte stays as it was.
func setTopLevelKey(raw []byte, key string, value json.RawMessage) ([]byte, error) {
	raw, _, err := removeTopLevelKeys(raw, key)
	if err != nil {
		return nil, err
	}
	members, err := topLevelMembers(raw)
	if err != nil {
		return nil, err
	}
	name, err := json.Marshal(key)
	if err != nil {
		return nil, fmt.Errorf("encode key %q: %w", key, err)
	}
	member := append(append(name, ": "...), value...)
	var at int
	var insert []byte
	if len(members) == 0 {
		at = bytes.IndexByte(raw, '{') + 1
		insert = member
	} else {
		last := members[len(members)-1]
		indent := last.start
		for indent > 0 && bytes.IndexByte([]byte(" \t\r\n"), raw[indent-1]) >= 0 {
			indent--
		}
		at = last.end
		insert = append(append([]byte(","), raw[indent:last.start]...), member...)
	}
	next := make([]byte, 0, len(raw)+len(insert))
	next = append(append(append(next, raw[:at]...), insert...), raw[at:]...)
	if !json.Valid(next) {
		return nil, fmt.Errorf("setting %q left invalid JSON", key)
	}
	return next, nil
}
