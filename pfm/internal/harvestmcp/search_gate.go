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

// serverInstructions is the harvester's routing clauses, exactly the
// contracts' harvester part: the six clauses joined by "; ", ending with
// ".". Clause 3 (local documents) is omitted on the remote gateway; clause 6
// (web search) is present only when a search backend is configured.
func serverInstructions(searchAvailable, remote bool) string {
	routes := []string{
		`Read a web page → harvester_read with it in urls`,
		`a paper or book by DOI, arXiv id, PMID, PMCID, ISBN, landing URL or harvester_search_literature handle → harvester_read with it in publications`,
	}
	if !remote {
		routes = append(routes, `a local document → harvester_read with its path in files`)
	}
	routes = append(
		routes,
		`find papers or books by title → harvester_search_literature`,
		`save a file's bytes unparsed → harvester_download_file`,
	)
	if searchAvailable {
		routes = append(routes, `search the web for a topic → harvester_search_web`)
	}
	return strings.Join(routes, "; ") + "."
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
		"(the details are in its log). Retry later; harvester_read, harvester_search_literature and harvester_download_file do not depend on web search."
}
