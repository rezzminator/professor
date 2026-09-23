// Package harvestmcp: this file is the one gate every search-shaped surface
// reads through — the registered tool, its routing instructions in the
// server description, the cache-miss hint, and a failed call's rendered
// error. None of them re-derives "is search available" or "what happened"
// a second way.
package harvestmcp

import (
	"errors"
	"strings"

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

// serverInstructions is the server's top-level routing guide. It names
// webSearch only when a search backend is configured (and says how to
// configure one otherwise), and parseLocalDocuments only on a local server.
func serverInstructions(searchAvailable, remote bool) string {
	routes := []string{
		`"read / get this web page or URL" is readPage (a paper's landing page is a page too)`,
		`"read this paper or book by DOI, arXiv id, PMID, PMCID, ISBN, landing URL, or findWorks handle" is readWork`,
		`"find papers / works / a book by TITLE" is findWorks (ranked candidates with a handle, no download)`,
		`"download / save this file — a PDF, zip, image, audio, dataset" is download (the bytes, unparsed; nothing is converted)`,
	}
	if !remote {
		routes = append(routes, `"read this local document" is parseLocalDocuments`)
	}
	order := "Order for a title — findWorks, then readWork with its handle"
	if searchAvailable {
		routes = append(routes, `"search the web for X" is webSearch (ranked URLs with snippets, not a paper finder)`)
		order += "; for a topic — webSearch, then readPage the URL"
	}
	text := "Public-document retrieval. Routing — " + strings.Join(routes, "; ") + ". " + order +
		`. Every tool answers per item — an empty list is "nothing found", an error is "the lookup failed", never one shape for both.`
	if !searchAvailable {
		text += " Web search is not configured on this server — set search.searxngURL or search.braveApiKey in harvester.config.json to enable web search."
	}
	return text
}

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
	// An unclassified backend error keeps its text in the log only: it can
	// carry a backend URL, and this surface may serve a remote client.
	return "Web search failed: the configured search backends failed for a reason the harvester could not classify " +
		"(the details are in its log). Retry later; readPage, findWorks and readWork do not depend on web search."
}
