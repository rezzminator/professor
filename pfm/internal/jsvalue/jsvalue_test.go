package jsvalue

import (
	"encoding/json"
	"math"
	"testing"
)

func TestMembersFollowJavaScriptPropertyOrder(t *testing.T) {
	members, ok := Members(json.RawMessage(`{"b":1,"10":2,"a":3,"2":4,"b":5,"02":6}`))
	if !ok {
		t.Fatal("Members refused an object")
	}
	var got []string
	for _, member := range members {
		got = append(got, member.Key+"="+string(member.Value))
	}
	want := []string{"2=4", "10=2", "b=5", "a=3", "02=6"}
	if len(got) != len(want) {
		t.Fatalf("members = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("members = %q, want %q", got, want)
		}
	}
	if _, ok := Members(json.RawMessage(`[1]`)); ok {
		t.Fatal("Members accepted an array")
	}
}

func TestNumberPrintsLikeJavaScript(t *testing.T) {
	// variables, so the sum is float64 arithmetic, not an exact constant
	tenth, fifth := 0.1, 0.2
	for value, want := range map[float64]string{
		0: "0", math.Copysign(0, -1): "0", 1: "1", -2.5: "-2.5", tenth + fifth: "0.30000000000000004",
		1e21: "1e+21", 1e-7: "1e-7", 123456789012: "123456789012", 1.5e-9: "1.5e-9",
		1e-6: "0.000001", 6.25e-7: "6.25e-7", -5e-7: "-5e-7",
		math.NaN(): "NaN", math.Inf(1): "Infinity",
	} {
		if got := Number(value); got != want {
			t.Fatalf("Number(%v) = %q, want %q", value, got, want)
		}
	}
	if got := JSONNumber(math.Inf(-1)); got != "null" {
		t.Fatalf("JSONNumber(-Inf) = %q, want null", got)
	}
}

func TestQuoteStringEscapesLikeJSONStringify(t *testing.T) {
	if got, want := QuoteString("a\"\\\b\f\n\r\t\x01<>& é"), `"a\"\\\b\f\n\r\t\u0001<>&`+" é\""; got != want {
		t.Fatalf("QuoteString = %s, want %s", got, want)
	}
}

func TestStringifyRawRewritesParsedJSON(t *testing.T) {
	got, err := StringifyRaw(json.RawMessage(` {"z": [1.0, 1e2, "A\/"], "1": null, "t": true} `))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"1":null,"z":[1,100,"A/"],"t":true}`; got != want {
		t.Fatalf("StringifyRaw = %s, want %s", got, want)
	}
}
