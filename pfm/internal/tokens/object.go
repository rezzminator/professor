package tokens

import (
	"strings"

	"github.com/rezzminator/professor/pfm/internal/jsvalue"
)

// object is a JavaScript object: members kept in property order (integer keys
// ascending, then insertion order), the order the report iterates and its JSON
// is written in. Values are float64, string, bool, nil (null), undefined
// (left out of JSON), *object, or a slice of those.
type object struct {
	keys   []string
	values map[string]any
}

// undefined is a member JSON.stringify leaves out.
type undefined struct{}

func newObject() *object { return &object{values: map[string]any{}} }

// set stores value under key; a key already present keeps its place.
func (o *object) set(key string, value any) *object {
	if _, found := o.values[key]; !found {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
	return o
}

func (o *object) get(key string) (any, bool) {
	value, found := o.values[key]
	return value, found
}

// number is the member as a number, 0 when absent: (o[k] || 0).
func (o *object) number(key string) float64 {
	value, _ := o.values[key].(float64)
	return value
}

// bump adds value to the member: o[k] = (o[k] || 0) + v.
func (o *object) bump(key string, value float64) { o.set(key, o.number(key)+value) }

// child is the member object, created empty when absent: o[k] ??= {}.
func (o *object) child(key string) *object {
	if value, ok := o.values[key].(*object); ok {
		return value
	}
	created := newObject()
	o.set(key, created)
	return created
}

// childWith is the member object, created with fields at 0 when absent:
// o[k] ??= {f1: 0, f2: 0, …}.
func (o *object) childWith(key string, fields ...string) *object {
	if value, ok := o.values[key].(*object); ok {
		return value
	}
	created := newObject()
	for _, field := range fields {
		created.set(field, 0.0)
	}
	o.set(key, created)
	return created
}

// insertion is the members in insertion order, a Map's iteration order.
func (o *object) insertion() []entry {
	out := make([]entry, len(o.keys))
	for i, key := range o.keys {
		out[i] = entry{key, o.values[key]}
	}
	return out
}

// bump2 adds value to field of the member object: (o[k] ??= {})[f] += v.
func (o *object) bump2(key, field string, value float64) { o.child(key).bump(field, value) }

func (o *object) size() int { return len(o.keys) }

// ordered is Object.keys(o).
func (o *object) ordered() []string {
	keys := append([]string(nil), o.keys...)
	jsvalue.SortKeys(keys, func(key string) string { return key })
	return keys
}

// entry is one [key, value] of Object.entries.
type entry struct {
	key   string
	value any
}

func (o *object) entries() []entry {
	keys := o.ordered()
	out := make([]entry, len(keys))
	for i, key := range keys {
		out[i] = entry{key, o.values[key]}
	}
	return out
}

// childOf is an entry's value as an object.
func (e entry) child() *object {
	value, _ := e.value.(*object)
	if value == nil {
		return newObject()
	}
	return value
}

// fromEntries is Object.fromEntries.
func fromEntries(entries []entry) *object {
	out := newObject()
	for _, item := range entries {
		out.set(item.key, item.value)
	}
	return out
}

// stringify is JSON.stringify(value, null, indent): indent 0 is compact.
func stringify(value any, indent int) string {
	var out strings.Builder
	writeValue(&out, value, strings.Repeat(" ", indent), "")
	return out.String()
}

func writeValue(out *strings.Builder, value any, indent, prefix string) {
	switch typed := value.(type) {
	case nil:
		out.WriteString(jsNull)
	case bool:
		if typed {
			out.WriteString("true")
		} else {
			out.WriteString("false")
		}
	case float64:
		out.WriteString(jsvalue.JSONNumber(typed))
	case int:
		out.WriteString(jsvalue.JSONNumber(float64(typed)))
	case string:
		out.WriteString(jsvalue.QuoteString(typed))
	case *object:
		var members []entry
		for _, item := range typed.entries() {
			if _, skip := item.value.(undefined); !skip {
				members = append(members, item)
			}
		}
		jsvalue.WriteContainer(out, '{', '}', len(members), indent, prefix, func(i int, inner string) {
			jsvalue.WriteKey(out, members[i].key, indent)
			writeValue(out, members[i].value, indent, inner)
		})
	case []any:
		writeList(out, len(typed), func(i int) any { return typed[i] }, indent, prefix)
	case []float64:
		writeList(out, len(typed), func(i int) any { return typed[i] }, indent, prefix)
	case []string:
		writeList(out, len(typed), func(i int) any { return typed[i] }, indent, prefix)
	case []*object:
		writeList(out, len(typed), func(i int) any { return typed[i] }, indent, prefix)
	case undefined:
		out.WriteString(jsNull) // an undefined array item is null, as in JSON.stringify
	default:
		panic("tokens: no JSON form for a value of this type")
	}
}

func writeList(out *strings.Builder, length int, item func(int) any, indent, prefix string) {
	jsvalue.WriteContainer(out, '[', ']', length, indent, prefix, func(i int, inner string) {
		writeValue(out, item(i), indent, inner)
	})
}
