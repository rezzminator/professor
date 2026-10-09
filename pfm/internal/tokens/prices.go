package tokens

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
)

// priceBook is the one price table a run reads, loaded once before any
// transcript: a saved table (--prices) or the table `pfm model-cost` serves.
type priceBook struct {
	table      pricing.Table
	saved      string // the --prices path; "" for the served table
	servedFrom *string
	refresh    *pricing.RefreshReport
	override   *pricing.Override
}

// loadSaved reads a saved `pfm model-cost --json --all` document, or a bare
// prices.json table.
func loadSaved(path string) (*priceBook, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("--prices %s: %w", path, err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(content, &probe); err != nil {
		return nil, fmt.Errorf("--prices %s: %w", path, err)
	}
	if _, isDocument := probe["served_from"]; !isDocument {
		table, err := pricing.DecodeTable(content, path)
		if err != nil {
			return nil, fmt.Errorf("--prices %s: %w", path, err)
		}
		return &priceBook{table: table, saved: path}, nil
	}
	doc, err := pricing.DecodeDocument(content, path)
	if err != nil {
		return nil, fmt.Errorf("--prices %s: %w", path, err)
	}
	if doc.Model != "" {
		return nil, fmt.Errorf(
			"--prices %s: a single-model lookup (%s); prices come from `pfm model-cost --json --all`",
			path,
			doc.Model,
		)
	}
	book := bookOf(doc)
	book.saved = path
	return book, nil
}

// loadServed refreshes and serves the table exactly as `pfm model-cost` does.
func loadServed(ctx context.Context, options modelcost.Options) (*priceBook, error) {
	prices, result, err := modelcost.Refresh(ctx, options)
	if err != nil {
		return nil, fmt.Errorf("the price table cannot load (pfm model-cost): %w", err)
	}
	return bookOf(modelcost.PriceDocument(prices, result, "", prices.Rows)), nil
}

func bookOf(doc pricing.Document) *priceBook {
	served, refresh := doc.ServedFrom, doc.Refresh
	return &priceBook{table: doc.Table(), servedFrom: &served, refresh: &refresh, override: doc.Override}
}

// catalog is the pricing provenance the report and its JSON carry.
func (book *priceBook) catalog() *object {
	sources := make([]*object, len(book.table.Sources))
	for i, source := range book.table.Sources {
		sources[i] = newObject().set("provider", source.Provider).set("url", source.URL)
	}
	out := newObject()
	if book.saved != "" {
		out.set("saved", book.saved)
	} else {
		out.set("saved", nil)
	}
	out.set("fetched_at", book.table.FetchedAt).set("sources", sources)
	if book.servedFrom != nil {
		out.set("served_from", *book.servedFrom)
	} else {
		out.set("served_from", nil)
	}
	if book.refresh == nil {
		return out.set("refresh", nil)
	}
	return out.set("refresh", refreshObject(*book.refresh))
}

// refreshObject is the refresh report in the member order pfm writes it,
// each optional member present only when pfm writes it.
func refreshObject(report pricing.RefreshReport) *object {
	out := newObject().set("status", report.Status)
	if report.Error != "" {
		out.set("error", report.Error)
	}
	if report.Changed != nil {
		out.set("changed", *report.Changed)
	}
	if report.Persisted != nil {
		out.set("persisted", *report.Persisted)
	}
	if report.PersistError != "" {
		out.set("persist_error", report.PersistError)
	}
	if report.StampError != "" {
		out.set("stamp_error", report.StampError)
	}
	return out
}

// rates are a model's rates at a call's context; found is false for a model
// no row prices.
func (book *priceBook) rates(model string, window float64) (pricing.Row, pricing.Rates, bool) {
	row, found := book.table.Resolve(model)
	if !found {
		return pricing.Row{}, pricing.Rates{}, false
	}
	return row, row.RatesAt(int64(window)), true
}

// readRate is the row's cache-read rate: hit on a Claude row, cached on a Codex one.
func readRate(row pricing.Row, rates pricing.Rates) *float64 {
	if row.Engine == pricing.EngineCodex {
		return rates.Cached
	}
	return rates.Hit
}
