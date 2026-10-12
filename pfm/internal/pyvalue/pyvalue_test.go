package pyvalue

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDumpsIsPythonJSONDumpsWithoutASCIIEscapes(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{
			`{"n":1.5,"big":1e20,"small":0.00001,"flag":true,"none":null,"list":[1,2.0,"x"],"nested":{"k":[]}}`,
			`{"n": 1.5, "big": 1e+20, "small": 1e-05, "flag": true, "none": null, "list": [1, 2.0, "x"], "nested": {"k": []}}`,
		},
		{`{"b":1,"a":2,"b":3}`, `{"b": 3, "a": 2}`},
		{`"naïve — \"q\" <tag> &   \t \u0001 \u007f"`, "\"naïve — \\\"q\\\" <tag> &   \\t \\u0001 \x7f\""},
		{`-0`, `0`},
		{`1e400`, `Infinity`},
		{`123456789012345678901234567890`, `123456789012345678901234567890`},
		{`1e16`, `1e+16`},
		{`1e15`, `1000000000000000.0`},
		{`0.0001`, `0.0001`},
		{``, `null`},
	} {
		if got := Dumps(json.RawMessage(test.raw)); got != test.want {
			t.Errorf("Dumps(%s) = %s, want %s", test.raw, got, test.want)
		}
	}
}

func TestStrAndReprFollowPython(t *testing.T) {
	for _, test := range []struct{ raw, str, repr string }{
		{`"it's"`, `it's`, `"it's"`},
		{`"say \"hi\" it's"`, `say "hi" it's`, `'say "hi" it\'s'`},
		{`"tab\tnl\n\u0085\u200b"`, "tab\tnl\n\u0085\u200b", `'tab\tnl\n\x85\u200b'`},
		{`null`, `None`, `None`},
		{``, `None`, `None`},
		{`true`, `True`, `True`},
		{`[1, "a", {"k": false}]`, `[1, 'a', {'k': False}]`, `[1, 'a', {'k': False}]`},
		{`2.50`, `2.5`, `2.5`},
		{`-1e400`, `-inf`, `-inf`},
	} {
		if got := Str(json.RawMessage(test.raw)); got != test.str {
			t.Errorf("Str(%s) = %q, want %q", test.raw, got, test.str)
		}
		if got := Repr(json.RawMessage(test.raw)); got != test.repr {
			t.Errorf("Repr(%s) = %q, want %q", test.raw, got, test.repr)
		}
	}
}

func TestBoolIsPythonTruthiness(t *testing.T) {
	for raw, want := range map[string]bool{
		``: false, `null`: false, `false`: false, `0`: false, `0.0`: false, `-0`: false, `""`: false, `[]`: false,
		`{}`: false, `true`: true, `1`: true, `" "`: true, `[0]`: true, `{"a":null}`: true, `1e-400`: false,
	} {
		if got := Bool(json.RawMessage(raw)); got != want {
			t.Errorf("Bool(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestStringMethodsUseUnicodeWhitespaceAndLineBoundaries(t *testing.T) {
	if got := SplitLines(
		"a\r\nb\rc\x0bd\x1ce f\n\n",
	); !reflect.DeepEqual(
		got,
		[]string{"a", "b", "c", "d", "e", "f", ""},
	) {
		t.Errorf("SplitLines = %q", got)
	}
	if got := SplitLines(""); got != nil {
		t.Errorf("SplitLines(\"\") = %q, want none", got)
	}
	if got := OneLine("  ls -la\n\x1fpwd  x  "); got != "ls -la pwd x" {
		t.Errorf("OneLine = %q", got)
	}
	if got := Strip("\x1c　 x \t"); got != "x" {
		t.Errorf("Strip = %q", got)
	}
	if got, want := LStrip(" x "), "x "; got != want {
		t.Errorf("LStrip = %q", got)
	}
	if got, want := RStrip(" x "), " x"; got != want {
		t.Errorf("RStrip = %q", got)
	}
}

func TestParseIntIsPythonInt(t *testing.T) {
	for _, test := range []struct {
		text string
		want int
	}{{" 12 ", 12}, {"+5", 5}, {"-3", -3}, {"1_000", 1000}} {
		if got, ok := ParseInt(test.text); !ok || got != test.want {
			t.Errorf("ParseInt(%q) = %d, %v", test.text, got, ok)
		}
	}
	for _, text := range []string{"nope", "", "1__0", "_1", "1_", "--1", "1.5"} {
		if _, ok := ParseInt(text); ok {
			t.Errorf("ParseInt(%q) accepted", text)
		}
	}
}

func TestStringReadsOnlyAJSONString(t *testing.T) {
	if text, ok := String(json.RawMessage(` "x" `)); !ok || text != "x" {
		t.Errorf("String = %q, %v", text, ok)
	}
	if _, ok := String(json.RawMessage(`5`)); ok {
		t.Error("String accepted a number")
	}
	if !IsNull(nil) || !IsNull(json.RawMessage(`null`)) || IsNull(json.RawMessage(`0`)) {
		t.Error("IsNull misreads absent, null or zero")
	}
	tenth, fifth := 0.1, 0.2
	if got := FloatRepr(tenth+fifth, "inf"); got != "0.30000000000000004" {
		t.Errorf("FloatRepr = %s", got)
	}
}
