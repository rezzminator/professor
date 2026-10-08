package modelcost

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/pricing"
)

var (
	footnotePattern  = regexp.MustCompile(`(?i)<sup>[^<]*</sup>`)
	amountPattern    = regexp.MustCompile(`(?i)^\$\s*(\d+(?:,\d{3})*(?:\.\d+)?)(?:\s*/\s*(?:MTok|1M tokens))?$`)
	promptTier       = regexp.MustCompile(`(?i)for prompts (up to|over) ([\d,]+) tokens`)
	anyPromptTier    = regexp.MustCompile(`(?i)for prompts`)
	serviceMode      = regexp.MustCompile(`(?m)^\s*(Standard|Batch|Fast|Ultrafast)\s*$`)
	perMillion       = regexp.MustCompile(`(?i)prices per 1m tokens`)
	longBoundary     = regexp.MustCompile(`(?i)long context:\s*>\s*([\d,.]+[kKmM]?)\s*input tokens`)
	thresholdPattern = regexp.MustCompile(`(\d+(?:,\d{3})*(?:\.\d+)?)\s*([kKmM]?)`)
)

// Derive turns both providers' catalogs into the price table: only the
// published standard text-token tables feed it (Claude's "Model pricing",
// OpenAI's Standard tables); batch, fast, regional, audio and tool tariffs
// never price a row. A model whose rows disagree, or that lacks an input or
// output price, is left out; a provider that yields no row is an error.
func Derive(catalogs []Catalog, fetchedAt string) (pricing.Table, error) {
	table := pricing.Table{Version: pricing.Version, FetchedAt: fetchedAt, Rows: []pricing.Row{}}
	for _, provider := range []string{providerAnthropic, providerOpenAI} {
		var catalog *Catalog
		for i := range catalogs {
			if catalogs[i].Provider != provider {
				continue
			}
			if catalog != nil {
				return pricing.Table{}, fmt.Errorf("derive prices: two %s catalogs", provider)
			}
			catalog = &catalogs[i]
		}
		if catalog == nil {
			return pricing.Table{}, fmt.Errorf("derive prices: no %s catalog", provider)
		}
		table.Sources = append(table.Sources, pricing.Source{Provider: provider, URL: catalog.Source.URL})
		var rows []pricing.Row
		if provider == providerAnthropic {
			rows = deriveClaude(catalog)
		} else {
			rows = deriveOpenAI(catalog)
		}
		if len(rows) == 0 {
			return pricing.Table{}, fmt.Errorf(
				"derive prices: the %s page %s yields no standard text-token rows; publisher layout changed",
				provider,
				catalog.Source.URL,
			)
		}
		table.Rows = append(table.Rows, rows...)
	}
	if _, err := pricing.Encode(table); err != nil {
		return pricing.Table{}, fmt.Errorf("derive prices: %w", err)
	}
	return table, nil
}

func plain(text string) string {
	return strings.TrimSpace(strings.NewReplacer("*", "", "`", "").Replace(tagPattern.ReplaceAllString(text, "")))
}

// amount is a "$N", "$N / MTok" or "$N / 1M tokens" cell; anything else — a
// dash, a range, another unit — is no price. A footnote is never a price.
func amount(text string) *float64 {
	text = strings.TrimSpace(footnotePattern.ReplaceAllString(text, ""))
	match := amountPattern.FindStringSubmatch(text)
	if match == nil {
		return nil
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", ""), 64)
	if err != nil {
		return nil
	}
	return &value
}

// threshold reads "100,000" or "272K" as a token count.
func threshold(text string) (int64, bool) {
	match := thresholdPattern.FindStringSubmatch(text)
	if match == nil {
		return 0, false
	}
	value, err := strconv.ParseFloat(strings.ReplaceAll(match[1], ",", ""), 64)
	if err != nil {
		return 0, false
	}
	switch strings.ToLower(match[2]) {
	case "k":
		value *= 1e3
	case "m":
		value *= 1e6
	}
	return int64(value), value > 0
}

// columns is a table's plain, lowercased headers.
type columns []string

func headersOf(table Table) columns {
	headers := make(columns, len(table.Headers))
	for i, header := range table.Headers {
		headers[i] = strings.ToLower(plain(header))
	}
	return headers
}

func (c columns) index(names ...string) int {
	return slices.IndexFunc(c, func(header string) bool { return slices.Contains(names, header) })
}

func cellAmount(row []Cell, column int) *float64 {
	if column < 0 || column >= len(row) {
		return nil
	}
	return amount(row[column].Text)
}

// claudeVariant is one Claude row: its rates and, for a prompt-length row,
// its condition and threshold.
type claudeVariant struct {
	rates     pricing.Rates
	condition string // "", "up to" or "over"
	threshold int64
}

func deriveClaude(catalog *Catalog) []pricing.Row {
	var rows []pricing.Row
	for _, model := range catalog.Models {
		var variants []claudeVariant
		for _, table := range model.Tables {
			if strings.ToLower(plain(table.Heading)) != "model pricing" {
				continue
			}
			headers := headersOf(table)
			in, out := headers.index("base input tokens", "input tokens"), headers.index("output tokens")
			if in < 0 || out < 0 {
				continue
			}
			hit, w5m, w1h := headers.index("cache hits and refreshes", "cache reads"),
				headers.index("5m cache writes"), headers.index("1h cache writes")
			label := modelColumn(table.Headers)
			for _, row := range table.Rows {
				variant := claudeVariant{rates: pricing.Rates{
					In: cellAmount(row, in), Out: cellAmount(row, out), Hit: cellAmount(row, hit),
					W5m: cellAmount(row, w5m), W1h: cellAmount(row, w1h),
				}}
				text := ""
				if label >= 0 {
					text = plain(row[label].Text)
				}
				if condition := promptTier.FindStringSubmatch(text); condition != nil {
					limit, ok := threshold(condition[2])
					if !ok {
						continue
					}
					variant.condition, variant.threshold = strings.ToLower(condition[1]), limit
				} else if anyPromptTier.MatchString(text) {
					continue
				}
				variants = append(variants, variant)
			}
		}
		if row, ok := claudeRow(model.ID, variants); ok {
			rows = append(rows, row)
		}
	}
	return rows
}

// claudeRow folds a model's variants into one row: every base row (plain or
// "up to") must agree, every "over" row must agree, and a plain row never
// mixes with a prompt-length tier.
func claudeRow(key string, variants []claudeVariant) (pricing.Row, bool) {
	var base, over []claudeVariant
	tiered := false
	for _, variant := range variants {
		if variant.condition == "over" {
			over = append(over, variant)
		} else {
			base = append(base, variant)
		}
		tiered = tiered || variant.condition != ""
	}
	if len(base) == 0 || !agree(base) || !agree(over) {
		return pricing.Row{}, false
	}
	row := pricing.Row{Key: key, Engine: pricing.EngineClaude, Rates: base[0].rates}
	if row.In == nil || row.Out == nil {
		return pricing.Row{}, false
	}
	if !tiered {
		return row, true
	}
	limit := int64(0)
	for _, variant := range variants {
		switch {
		case variant.condition == "":
			return pricing.Row{}, false
		case limit == 0:
			limit = variant.threshold
		case variant.threshold != limit:
			return pricing.Row{}, false
		}
	}
	row.Long = &pricing.Long{Above: limit}
	if len(over) > 0 {
		row.Long.Rates = over[0].rates
	}
	return row, true
}

func agree(variants []claudeVariant) bool {
	for _, variant := range variants {
		if !reflect.DeepEqual(variant, variants[0]) {
			return false
		}
	}
	return true
}

// openAIVariant is one OpenAI Standard row: its short-context rates and its
// long-context tier, when the table publishes one.
type openAIVariant struct {
	rates pricing.Rates
	long  *pricing.Long
}

func deriveOpenAI(catalog *Catalog) []pricing.Row {
	if !perMillion.MatchString(catalog.PageContext) {
		return nil
	}
	boundary, hasBoundary := int64(0), false
	if match := longBoundary.FindStringSubmatch(catalog.PageContext); match != nil {
		boundary, hasBoundary = threshold(match[1])
	}
	var rows []pricing.Row
	for _, model := range catalog.Models {
		var variants []openAIVariant
		for _, table := range model.Tables {
			headers := headersOf(table)
			if !standardTable(table, headers) {
				continue
			}
			in, out := headers.index("short context input", "input"), headers.index("short context output", "output")
			if in < 0 || out < 0 {
				continue
			}
			cached := headers.index("short context cached input", "cached input")
			longIn := headers.index("long context input")
			label := modelColumn(table.Headers)
			for _, row := range table.Rows {
				if label >= 0 && anyPromptTier.MatchString(plain(row[label].Text)) {
					continue
				}
				variant := openAIVariant{rates: pricing.Rates{
					In: cellAmount(row, in), Out: cellAmount(row, out), Cached: cellAmount(row, cached),
				}}
				// A long-context column of dashes publishes no long tier for the
				// model: its base rates hold at every context.
				long := pricing.Rates{
					In:     cellAmount(row, longIn),
					Out:    cellAmount(row, headers.index("long context output")),
					Cached: cellAmount(row, headers.index("long context cached input")),
				}
				if long.In != nil || long.Out != nil || long.Cached != nil {
					if !hasBoundary {
						continue
					}
					variant.long = &pricing.Long{Above: boundary, Rates: long}
				}
				variants = append(variants, variant)
			}
		}
		if row, ok := openAIRow(model.ID, variants); ok {
			rows = append(rows, row)
		}
	}
	return rows
}

// standardTable is OpenAI's Standard text-token table: the "Standard pricing
// data" table, a grouped table of short-context columns, or a specialized
// category table whose service mode is Standard.
func standardTable(table Table, headers columns) bool {
	heading := strings.ToLower(plain(table.Heading))
	if heading == "standard pricing data" {
		return true
	}
	if heading != "grouped pricing table data" {
		return false
	}
	if headers.index("short context input") >= 0 {
		return true
	}
	modes := serviceMode.FindAllStringSubmatch(table.Context, -1)
	return headers.index("category") >= 0 && headers.index("input") >= 0 && headers.index("output") >= 0 &&
		len(modes) > 0 && modes[len(modes)-1][1] == "Standard"
}

// openAIRow folds a model's variants: their short-context rates must agree,
// and so must every long tier they publish.
func openAIRow(key string, variants []openAIVariant) (pricing.Row, bool) {
	if len(variants) == 0 {
		return pricing.Row{}, false
	}
	row := pricing.Row{Key: key, Engine: pricing.EngineCodex, Rates: variants[0].rates}
	for _, variant := range variants {
		if !reflect.DeepEqual(variant.rates, row.Rates) {
			return pricing.Row{}, false
		}
		if variant.long == nil {
			continue
		}
		if row.Long != nil && !reflect.DeepEqual(variant.long, row.Long) {
			return pricing.Row{}, false
		}
		row.Long = variant.long
	}
	if row.In == nil || row.Out == nil {
		return pricing.Row{}, false
	}
	return row, true
}
