package pricing

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

const (
	billingDirect  = "direct"
	billingBedrock = "bedrock"
	billingVertex  = "vertex"
)

// Billing identifies the Claude billing assumption, never a backend inferred
// from the process running a report. Multiplier applies only to published
// Claude 4.5+ rows; explicit row overrides are final billed rates.
// The publisher's regional rule is documented at:
// https://platform.claude.com/docs/en/about-claude/pricing#cloud-platform-pricing
// Older documents without Billing have an unspecified billing assumption.
type Billing struct {
	Route      string  `json:"route"`
	Region     string  `json:"region"`
	Configured bool    `json:"configured"`
	Multiplier float64 `json:"multiplier"`
}

func defaultBilling() Billing {
	return Billing{Route: billingDirect, Region: "global", Multiplier: 1}
}

func billingMultiplier(route, region string) (float64, error) {
	switch {
	case (route == billingDirect || route == billingBedrock || route == billingVertex) && region == "global":
		return 1, nil
	case route == billingBedrock && region == "geo", route == billingVertex && region == "regional":
		return 1.1, nil
	default:
		return 0, fmt.Errorf(
			"billing route/region %q/%q: want direct/global, bedrock/global|geo or vertex/global|regional",
			route,
			region,
		)
	}
}

func decodeBilling(content json.RawMessage) (Billing, error) {
	var selection struct {
		Route  string `json:"route"`
		Region string `json:"region"`
	}
	if err := strictUnmarshal(content, &selection); err != nil {
		return Billing{}, fmt.Errorf("billing: %w", err)
	}
	multiplier, err := billingMultiplier(selection.Route, selection.Region)
	if err != nil {
		return Billing{}, err
	}
	return Billing{Route: selection.Route, Region: selection.Region, Configured: true, Multiplier: multiplier}, nil
}

// applyBilling adjusts served copies, not the published table that refresh
// persists. Cloud global base prices remain the published rates. In particular
// the checked-in Anthropic page lists Sonnet 5.5 cache reads at $0.10, not $0.20.
func applyBilling(prices *Prices) {
	if prices.Billing.Route == billingDirect {
		return
	}
	url := "https://aws.amazon.com/bedrock/pricing/"
	if prices.Billing.Route == billingVertex {
		url = "https://cloud.google.com/vertex-ai/generative-ai/pricing#claude-models"
	}
	prices.Sources = append(append([]Source(nil), prices.Sources...), Source{
		Provider: prices.Billing.Route, URL: url,
	})
	for i, row := range prices.Rows {
		// Issue #43 records both partner price lists at $0.10 global. Keep
		// that known backend rate even when an older direct table says $0.20.
		if row.Key == "claude-sonnet-5-5" && row.Engine == EngineClaude {
			hit := 0.10
			row.Hit = &hit
		}
		if row.Engine != EngineClaude || !regionalPremium(row.Key) {
			continue
		}
		row.Rates = multiplyRates(row.Rates, prices.Billing.Multiplier)
		if row.Long != nil {
			long := *row.Long
			long.Rates = multiplyRates(long.Rates, prices.Billing.Multiplier)
			row.Long = &long
		}
		prices.Rows[i] = row
	}
}

// regionalPremium follows the publisher's 4.5+ scope, not a blanket markup
// on retired models or unknown, negotiated Claude model keys.
func regionalPremium(key string) bool {
	parts := strings.Split(snapshotDate.ReplaceAllString(key, ""), "-")
	if len(parts) < 3 || parts[0] != EngineClaude {
		return false
	}
	switch parts[1] {
	case "opus", "sonnet", "haiku", "fable", "mythos":
	default:
		return false
	}
	major, err := strconv.Atoi(parts[2])
	if err != nil {
		return false // Not a publisher's versioned key; never guess its price.
	}
	if major != 4 || len(parts) < 4 {
		return major > 4
	}
	minor, err := strconv.Atoi(parts[3])
	return err == nil && minor >= 5
}

func multiplyRates(rates Rates, multiplier float64) Rates {
	for _, rate := range []**float64{&rates.In, &rates.Out, &rates.Hit, &rates.W5m, &rates.W1h} {
		if *rate != nil {
			value := **rate * multiplier
			*rate = &value
		}
	}
	return rates
}
