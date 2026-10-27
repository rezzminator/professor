package pricing

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func documentFixture() Document {
	changed, persisted := true, false
	return Document{
		Version:    Version,
		FetchedAt:  "2026-01-01T00:00:00Z",
		CheckedAt:  "2026-01-02T03:04:05.5Z",
		Sources:    []Source{{Provider: "anthropic", URL: "https://example.invalid/anthropic"}},
		ServedFrom: FromFile,
		File:       "/clone/prices.json",
		Refresh: RefreshReport{
			Status:       "refreshed",
			Changed:      &changed,
			Persisted:    &persisted,
			PersistError: "read-only",
		},
		Override: &Override{Path: "/cfg/pfm.prices.json", Rows: 1},
		Billing:  &Billing{Route: "vertex", Region: "regional", Configured: true, Multiplier: 1.1},
		Rows: []Row{
			{
				Key:    "claude-opus-5-5",
				Engine: EngineClaude,
				Rates:  Rates{In: ptr(4), Out: ptr(20), Hit: ptr(0.2)},
				Source: SourcePublished,
			},
			{
				Key: "gpt-5.6-sol", Engine: EngineCodex, Rates: Rates{In: ptr(4), Out: ptr(20), Cached: ptr(0.4)},
				Long: &Long{Above: 272000, Rates: Rates{In: ptr(8)}}, Source: SourceOverride,
			},
		},
	}
}

func TestDocumentRoundTrips(t *testing.T) {
	var encoded bytes.Buffer
	if err := EncodeDocument(&encoded, documentFixture()); err != nil {
		t.Fatalf("EncodeDocument: %v", err)
	}
	text := encoded.String()
	if !strings.HasPrefix(text, "{\n  \"version\": 2,\n  \"fetched_at\": ") || !strings.HasSuffix(text, "}\n") {
		t.Fatalf("encoded document is not the indented, newline-terminated form:\n%s", text)
	}
	decoded, err := DecodeDocument(encoded.Bytes(), "round-trip")
	if err != nil {
		t.Fatalf("DecodeDocument: %v", err)
	}
	if !reflect.DeepEqual(decoded, documentFixture()) {
		t.Fatalf("round trip changed the document:\n got %+v\nwant %+v", decoded, documentFixture())
	}
	if table := decoded.Table(); len(table.Rows) != 2 || table.FetchedAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("Table() = %+v", table)
	}
}

func TestOlderDocumentLeavesBillingUnspecified(t *testing.T) {
	doc := documentFixture()
	doc.Billing = nil
	var content bytes.Buffer
	if err := EncodeDocument(&content, doc); err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeDocument(content.Bytes(), "older")
	if err != nil || decoded.Billing != nil {
		t.Fatalf("old document must not invent a billing route: %+v, %v", decoded.Billing, err)
	}
}

func TestDecodeDocumentRefusesWhatPfmNeverWrites(t *testing.T) {
	compact, err := json.Marshal(documentFixture())
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	valid := string(compact)
	for _, tc := range []struct{ name, from, to, want string }{
		{"an unknown key", `"served_from"`, `"servedFrom"`, "unknown field"},
		{"another version", `"version":2`, `"version":1`, "version must be 2"},
		{"a row without its source", `,"source":"published"`, ``, "neither published nor override"},
		{"a row source pfm never sets", `"source":"override"`, `"source":"guess"`, "neither published nor override"},
		{"a rate below zero", `"in":4,"out":20,"hit"`, `"in":-4,"out":20,"hit"`, "non-negative"},
		{"no fetch time", `"fetched_at":"2026-01-01T00:00:00Z"`, `"fetched_at":""`, "fetched_at"},
		{"an override with no path", `"path":"/cfg/pfm.prices.json"`, `"path":""`, "override needs"},
		{"an unknown billing route", `"route":"vertex"`, `"route":"other"`, "billing route/region"},
		{"a contradictory multiplier", `"multiplier":1.1`, `"multiplier":2`, "billing multiplier"},
		{"a non-default unconfigured route", `"configured":true`, `"configured":false`, "configured flag"},
		{"not JSON", `{`, `[`, "price document saved"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := strings.Replace(valid, tc.from, tc.to, 1)
			if content == valid {
				t.Fatalf("fixture has no %q to replace", tc.from)
			}
			if _, err := DecodeDocument(
				[]byte(content),
				"saved",
			); err == nil ||
				!strings.Contains(err.Error(), tc.want) {
				t.Fatalf("DecodeDocument error = %v, want one naming %q", err, tc.want)
			}
		})
	}
}
