package pricing

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestBillingRoutesApplyBeforeOverridesWithoutMutatingPublished(t *testing.T) {
	published, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	before, err := Encode(published)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		route, region string
		factor        float64
	}{
		{"direct", "global", 1},
		{"bedrock", "global", 1},
		{"bedrock", "geo", 1.1},
		{"vertex", "global", 1},
		{"vertex", "regional", 1.1},
	} {
		t.Run(tc.route+"/"+tc.region, func(t *testing.T) {
			body := fmt.Sprintf(`{"version":2,"billing":{"route":%q,"region":%q},"rows":[
				{"key":"claude-sonnet-5","engine":"claude","in":7,"out":9,"hit":0.7}]}`, tc.route, tc.region)
			prices, err := ServeTable(
				published,
				Origin{From: FromFile, File: "/clone/prices.json"},
				configWithOverride(t, body),
			)
			if err != nil {
				t.Fatalf("billing route must load: %v", err)
			}
			for _, original := range published.Rows {
				row, ok := prices.Resolve(original.Key)
				if !ok {
					t.Fatalf("missing row %s", original.Key)
				}
				if original.Key == "claude-sonnet-5" {
					if *row.In != 7 || *row.Out != 9 || *row.Hit != 0.7 || row.Source != SourceOverride {
						t.Fatalf("override must win without a multiplier: %+v", row)
					}
					continue
				}
				factor := tc.factor
				if original.Engine != EngineClaude || original.Key == "claude-opus-4" ||
					original.Key == "claude-opus-4-1" ||
					original.Key == "claude-sonnet-4" ||
					original.Key == "claude-haiku-3-5" {
					factor = 1
				}
				checkBillingRates(t, original.Key, original.Rates, row.Rates, factor)
				if original.Long != nil {
					if row.Long == nil || row.Long.Above != original.Long.Above {
						t.Fatalf("long tier changed for %s", original.Key)
					}
					checkBillingRates(t, original.Key+" long", original.Long.Rates, row.Long.Rates, factor)
				}
			}
			encoded, err := json.Marshal(prices)
			if err != nil {
				t.Fatal(err)
			}
			var served struct {
				Billing struct {
					Route, Region string
					Configured    bool
				}
			}
			if err := json.Unmarshal(encoded, &served); err != nil {
				t.Fatal(err)
			}
			if served.Billing.Route != tc.route || served.Billing.Region != tc.region || !served.Billing.Configured {
				t.Fatalf("billing metadata missing: %s", encoded)
			}
		})
	}
	after, err := Encode(published)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("billing mutated the published table or a long-tier pointer")
	}
}

func TestCloudSonnetCacheReadIsIndependentOfOlderDirectRate(t *testing.T) {
	published, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for i := range published.Rows {
		if published.Rows[i].Key == "claude-sonnet-5-5" {
			published.Rows[i].Hit = ptr(0.20)
		}
	}
	for _, tc := range []struct {
		route, region string
		hit           float64
	}{
		{"direct", "global", 0.20},
		{"bedrock", "global", 0.10},
		{"vertex", "global", 0.10},
		{"bedrock", "geo", 0.11},
		{"vertex", "regional", 0.11},
	} {
		body := fmt.Sprintf(`{"version":2,"billing":{"route":%q,"region":%q},"rows":[]}`, tc.route, tc.region)
		prices, err := ServeTable(published, Origin{}, configWithOverride(t, body))
		if err != nil {
			t.Fatal(err)
		}
		row, ok := prices.Resolve("claude-sonnet-5-5")
		if !ok || row.Hit == nil || math.Abs(*row.Hit-tc.hit) > 1e-9 {
			t.Fatalf("%s/%s: Sonnet cache read = %v, want %g", tc.route, tc.region, row.Hit, tc.hit)
		}
	}
}

func TestBillingDefaultAndPremiumScope(t *testing.T) {
	published, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", configWithOverride(t, ""), configWithOverride(t, `{"version":2,"rows":[]}`)} {
		prices, err := ServeTable(published, Origin{From: FromEmbedded}, path)
		if err != nil || prices.Billing != defaultBilling() {
			t.Fatalf("default billing = %+v, error %v", prices.Billing, err)
		}
	}
	for key, want := range map[string]bool{
		"claude-opus-4-1": false, "claude-sonnet-4": false, "claude-haiku-3-5": false,
		"claude-sonnet-4-20250514": false, "claude-sonnet-4-5-20250929": true,
		"claude-opus-4-5": true, "claude-sonnet-4-6": true, "claude-haiku-5-5": true,
		"claude-fable-5-1": true, "claude-mythos-5": true,
		"claude-negotiated-1": false, "gpt-5-5": false, "claude-opus-test": false,
	} {
		if got := regionalPremium(key); got != want {
			t.Fatalf("regionalPremium(%q) = %v, want %v", key, got, want)
		}
	}
}

func checkBillingRates(t *testing.T, name string, original, served Rates, factor float64) {
	t.Helper()
	for i, pair := range [][2]*float64{{original.In, served.In}, {original.Out, served.Out}, {original.Hit, served.Hit}, {original.W5m, served.W5m}, {original.W1h, served.W1h}, {original.Cached, served.Cached}} {
		if pair[0] == nil {
			if pair[1] != nil {
				t.Fatalf("%s column %d: absent price became present", name, i)
			}
			continue
		}
		if pair[1] == nil || math.Abs(*pair[1]-*pair[0]*factor) > 1e-9 {
			t.Fatalf("%s column %d: got %v, want %g x %g", name, i, pair[1], *pair[0], factor)
		}
	}
}

func TestBillingRejectsInvalidSelectionNamingOverride(t *testing.T) {
	published, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, billing := range []string{
		`{}`, `null`, `{"route":"other","region":"global"}`, `{"route":"direct","region":"geo"}`,
		`{"route":"vertex","region":"geo"}`, `{"route":"bedrock","region":"regional"}`,
		`{"route":"bedrock"}`, `{"route":"vertex","region":"regional","multiplier":2}`,
	} {
		t.Run(billing, func(t *testing.T) {
			_, err := ServeTable(
				published,
				Origin{},
				configWithOverride(t, `{"version":2,"billing":`+billing+`,"rows":[]}`),
			)
			if err == nil || !strings.Contains(err.Error(), "pfm.prices.json") {
				t.Fatalf("invalid billing must name its file: %v", err)
			}
		})
	}
}
