package tokens

import (
	"reflect"
	"testing"
)

func TestObjectKeepsJavaScriptPropertyOrder(t *testing.T) {
	o := newObject().set("b", 1.0).set("10", 2.0).set("a", 3.0).set("2", 4.0)
	o.set("b", 5.0) // a key already present keeps its place
	if got, want := o.ordered(), []string{"2", "10", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ordered = %v, want %v: integer keys ascend, then insertion order", got, want)
	}
	var inserted []string
	for _, item := range o.insertion() {
		inserted = append(inserted, item.key)
	}
	if want := []string{"b", "10", "a", "2"}; !reflect.DeepEqual(inserted, want) {
		t.Errorf("insertion = %v, want %v: a Map iterates in insertion order", inserted, want)
	}
	if value, _ := o.get("b"); value != 5.0 || o.size() != 4 {
		t.Errorf("get(b) = %v and size %d, want 5 and 4", value, o.size())
	}
}

func TestObjectAccumulates(t *testing.T) {
	o := newObject()
	o.bump("n", 2)
	o.bump("n", 3)
	o.bump2("by", "x", 1)
	o.bump2("by", "x", 1)
	o.set("s", "text")
	if o.number("n") != 5 || o.number("missing") != 0 || o.number("s") != 0 {
		t.Errorf("number: n=%v missing=%v s=%v, want 5, 0, 0", o.number("n"), o.number("missing"), o.number("s"))
	}
	if got := o.child("by").number("x"); got != 2 {
		t.Errorf("bump2 total = %v, want 2", got)
	}
	created := o.childWith("w", "calls", "usd")
	created.bump("calls", 1)
	if again := o.childWith("w", "other"); again != created || again.size() != 2 {
		t.Errorf("childWith must return the existing member untouched")
	}
	if got := stringify(created, 0); got != `{"calls":1,"usd":0}` {
		t.Errorf("childWith fields = %s", got)
	}
	if got := (entry{"k", "not an object"}).child().size(); got != 0 {
		t.Errorf("entry.child of a non-object = %d members, want an empty object", got)
	}
	round := fromEntries(o.entries())
	if stringify(round, 0) != stringify(o, 0) {
		t.Errorf("fromEntries(entries) = %s, want %s", stringify(round, 0), stringify(o, 0))
	}
}

func TestStringifyIsJSONStringify(t *testing.T) {
	inner := newObject().set("z", nil).set("skip", undefined{}).set("t", true)
	o := newObject().set("s", "a\"b\x01\u2028").set("n", 0.1).set("i", 3).set("f", false).
		set("list", []any{1.0, undefined{}, "x"}).set("nums", []float64{}).set("strs", []string{"q"}).
		set("objs", []*object{newObject()}).set("o", inner)
	compact := "{\"s\":\"a\\\"b\\u0001\u2028\",\"n\":0.1,\"i\":3,\"f\":false,\"list\":[1,null,\"x\"],\"nums\":[],\"strs\":[\"q\"],\"objs\":[{}],\"o\":{\"z\":null,\"t\":true}}"
	if got := stringify(o, 0); got != compact {
		t.Errorf("compact:\n got %s\nwant %s", got, compact)
	}
	pretty := "{\n  \"a\": [\n    1\n  ],\n  \"b\": {\n    \"c\": \"d\"\n  },\n  \"e\": {}\n}"
	nested := newObject().set("a", []any{1.0}).
		set("b", newObject().set("c", "d")).
		set("e", newObject().set("u", undefined{}))
	if got := stringify(nested, 2); got != pretty {
		t.Errorf("indent 2:\n got %q\nwant %q", got, pretty)
	}
}
