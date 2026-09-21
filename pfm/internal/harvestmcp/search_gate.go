// Package harvestmcp: this file is the one gate every search-shaped surface
// reads through — the registered tool, its routing instructions in the
// server description, the cache-miss hint, and a failed call's rendered
// error. None of them re-derives "is search available" or "what happened"
// a second way.
package harvestmcp

import (
	"errors"

	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// searchEnabled is the one place a Runtime's search configuration turns into
// the enabled bool the search tool, its instructions, and its hints all key
// off of — never re-derived a second way per call site.
func runtimeSearchEnabled(runtime Runtime) bool {
	return harvest.SearchEnabled(harvest.SearchOptions{
		SearXNGURL: runtime.SearXNGURL, BraveAPIKey: runtime.BraveAPIKey, DisableSearch: runtime.DisableSearch,
	})
}

// serverInstructions is the server's top-level routing guide. searchOnInstructions
// is byte-identical to the server's original hardcoded text; searchOffInstructions
// drops both clauses that name the (unavailable) search tool and states why.
func serverInstructions(searchAvailable bool) string {
	if searchAvailable {
		return searchOnInstructions
	}
	return searchOffInstructions
}

const searchOnInstructions = `Public-document retrieval. Routing — "fetch / get / read this URL, DOI, ISBN, PMID, PMCID, or local file" is fetch; "find papers / works / a book by TITLE" is findWorks (bibliographic candidates with a fetch handle, no download); "search the web for X" is search (ranked URLs with snippets, not a paper finder); "fetch this image / figure" is fetchImage; "did we already fetch it / grep what we hold" is searchCache; "open / list / extract from this .zip, .tar, .7z, or .rar" is archive (a compressed-archive browser, NOT a webpage snapshotter — a web page goes to fetch). Order for a known document — searchCache, then fetch; for a title — findWorks, then fetch with its handle; for a topic — search, then fetch the URL. Every tool answers per item — an empty list is "nothing found", an error is "the lookup failed", never one shape for both.`

const searchOffInstructions = `Public-document retrieval. Routing — "fetch / get / read this URL, DOI, ISBN, PMID, PMCID, or local file" is fetch; "find papers / works / a book by TITLE" is findWorks (bibliographic candidates with a fetch handle, no download); "fetch this image / figure" is fetchImage; "did we already fetch it / grep what we hold" is searchCache; "open / list / extract from this .zip, .tar, .7z, or .rar" is archive (a compressed-archive browser, NOT a webpage snapshotter — a web page goes to fetch). Order for a known document — searchCache, then fetch; for a title — findWorks, then fetch with its handle. Every tool answers per item — an empty list is "nothing found", an error is "the lookup failed", never one shape for both. Web search is not configured on this server — set search.searxngURL or search.braveApiKey in harvester.config.json to enable the search tool.`

// renderSearchFailure is the public rendering of a failed search call.
// ErrSearchNotConfigured and ErrSearchDisabled render verbatim — both are
// configuration states, not outages, and retrying either changes nothing. A
// SearchBackendError names its backend and a safe, classified cause — never
// the backend's own error text, which could carry its URL and this surface
// may serve a remote client. Detailed backend errors are logged internally
// (see the search tool's own stderr line); callers receive only these
// actionable failure classes.
func renderSearchFailure(err error) string {
	switch {
	case errors.Is(err, harvest.ErrSearchNotConfigured), errors.Is(err, harvest.ErrSearchDisabled):
		return err.Error()
	}
	var backendErr *harvest.SearchBackendError
	if errors.As(err, &backendErr) {
		return "Web search failed: " + backendErr.SafeMessage()
	}
	return "Web search failed. " + harvest.PublicFailureMessage(harvest.Result{Error: err.Error()})
}
