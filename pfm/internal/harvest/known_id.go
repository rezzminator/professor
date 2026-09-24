package harvest

import (
	"context"
	"fmt"
	"log"
	"strings"
)

func (h *Harvester) fetchKnownID(ctx context.Context, source string, kind IdentifierKind, options FetchOptions) Result {
	canonical := NormalizeIdentifier(source)
	if canonical == "" {
		canonical = strings.TrimSpace(source)
	}
	if result, handled := h.readThroughLanding(ctx, source, kind, options); handled {
		return result
	}
	if !options.Refresh && !throughLanding(ctx) {
		if body, cachedKind, meta, path, ok := h.cache.loadAny(
			canonical,
			[]string{kindPDF, kindDOCX, kindXLSX, kindPPTX, kindCSV, kindJSON, kindTXT, kindHTML},
		); ok {
			return h.resultFromCache(source, cachedKind, body, meta, path)
		}
	}
	r := h.resolver()
	var candidates []Candidate
	var err error
	switch kind {
	case IdentifierDOI:
		candidates, err = r.ResolveDOI(ctx, source)
	case IdentifierISBN:
		candidates, err = r.ResolveBook(ctx, source)
	case IdentifierPMCID:
		candidates, err = r.ResolvePMCID(ctx, source)
	case IdentifierPMID:
		candidates, err = r.ResolvePMID(ctx, source)
	}
	trace := []string{}
	resolverFailureKind := ""
	if err != nil && (kind == IdentifierDOI || kind == IdentifierISBN) {
		// doiMetadataError (F14) now also carries a book-lookup outage
		// (ResolveBook); doiResolverErrorKind reads its generic kind field
		// regardless of subject, so the same classifier applies here too.
		resolverFailureKind = doiResolverErrorKind(err)
	}
	var doiMirrorFailure *Result
	tryDOIMirror := func() (Result, bool) {
		if kind != IdentifierDOI && kind != IdentifierPMID || h.settings.doiMirrorURL == "" {
			return Result{}, false
		}
		identifier := canonical
		if kind == IdentifierPMID {
			identifier = strings.TrimSpace(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(source)), "pmid:"))
		}
		result := h.fetchDOIMirror(ctx, identifier, options)
		if result.Error != "" {
			resultCopy := result
			doiMirrorFailure = &resultCopy
			trace = append(trace, result.Rungs...)
			return result, false
		}
		trace = append(trace, result.Rungs...)
		return h.storeResultAlias(source, canonical, result, trace, options), true
	}
	tryMirrors := func() (Result, bool) {
		if kind != IdentifierDOI {
			return Result{}, false
		}
		result, attempted := h.fetchDOIMirrors(ctx, canonical, options)
		if !attempted {
			return Result{}, false
		}
		trace = append(trace, result.Rungs...)
		if result.Error != "" {
			resultCopy := result
			doiMirrorFailure = &resultCopy // shared terminal diagnostic slot for all mirror providers
			return result, false
		}
		return h.storeResultAlias(source, canonical, result, trace, options), true
	}
	tryScholar := func() (Result, bool) {
		if kind != IdentifierDOI || strings.TrimSpace(h.settings.googleScholarURL) == "" {
			return Result{}, false
		}
		result := h.fetchScholarDOI(ctx, canonical, options)
		trace = append(trace, result.Rungs...)
		if result.Error != "" {
			resultCopy := result
			doiMirrorFailure = &resultCopy
			return result, false
		}
		return h.storeResultAlias(source, canonical, result, trace, options), true
	}
	if err != nil {
		if result, ok := tryDOIMirror(); ok {
			return result
		}
		if result, ok := tryMirrors(); ok {
			return result
		}
		if result, ok := tryScholar(); ok {
			return result
		}
		failure := Result{Source: source, Error: err.Error(), ErrorKind: resolverFailureKind, Rungs: trace}
		if doiMirrorFailure != nil {
			failure = mergeResolverFailure(failure, *doiMirrorFailure)
		}
		return failure
	}
	if len(candidates) == 0 && kind != IdentifierDOI {
		if result, ok := tryDOIMirror(); ok {
			return result
		} else if doiMirrorFailure != nil {
			return Result{
				Source:     source,
				Error:      withRungs(doiMirrorFailure.Error, doiMirrorFailure.Rungs),
				ErrorKind:  doiMirrorFailure.ErrorKind,
				Challenge:  doiMirrorFailure.Challenge,
				HTTPStatus: doiMirrorFailure.HTTPStatus,
				Rungs:      doiMirrorFailure.Rungs,
			}
		}
		return Result{Source: source, Error: "no legal open-access copy found"}
	}
	trace = make([]string, 0, len(candidates))
	// An open-access page read as HTML that carries no full text (a record
	// page: abstract and metadata) does not end the loop: its own PDF link is
	// read, else the next candidate is tried, else it is the answer at last,
	// its partial naming why (keptThin).
	var thin *thinCopy
	for _, c := range candidates {
		trace = append(trace, "oa:"+c.Source)
		result := h.fetchURLWithPolicy(ctx, c.URL, options, false)
		if result.Error != "" {
			continue
		}
		if c.Kind == kindHTML {
			if full, measure := landingFullText(result.Content); !full {
				if linked, ok := h.readLinkedFullText(ctx, c.URL, result.Content, options); ok {
					trace = append(trace, "oa:"+c.Source+"-pdf-link")
					return h.storeResultAlias(source, canonical, linked, append([]string(nil), trace...), options)
				}
				if thin == nil {
					thin = &thinCopy{
						result:  result,
						url:     c.URL,
						measure: measure,
						trace:   append([]string(nil), trace...),
					}
				}
				continue
			}
		}
		noteServed(ctx, c.URL)
		return h.storeResultAlias(source, canonical, result, append([]string(nil), trace...), options)
	}
	keptThin := func() (Result, bool) {
		if thin == nil {
			return Result{}, false
		}
		return h.storeThinCopy(ctx, source, canonical, *thin, options), true
	}
	// Europe PMC's PDF endpoint is the preferred mirror, but scanned/HTML-only
	// records still expose a legal full-text article page. Keep that fallback
	// inside the identifier chain rather than treating the PMCID as a generic
	// publisher URL.
	if kind == IdentifierPMCID || kind == IdentifierPMID {
		for _, c := range candidates {
			pmcid := extractPMCID(c.URL)
			if pmcid == "" {
				continue
			}
			// PMC OA service (oa.fcgi) direct PDF — the ftp hrefs NCBI hands out
			// are dead; PMCOAPDFURL rewrites them to the live deprecated/ https path.
			trace = append(trace, "oa:pmc-fcgi")
			oaPDF, oaErr := PMCOAPDFURL(ctx, h.oa, pmcid)
			if oaErr != nil {
				// The lookup FAILED — that is an outage, not proof the PMC
				// OA service holds no PDF for this accession.
				log.Printf("harvest: pmc oa.fcgi lookup failed for %s: %v", pmcid, oaErr)
			}
			if oaErr == nil && oaPDF != "" {
				result := h.fetchURLWithPolicy(ctx, oaPDF, options, false)
				if result.Error == "" {
					result.Method = "mirror:pmc-oa-pdf"
					return h.storeResultAlias(source, canonical, result, append([]string(nil), trace...), options)
				}
				trace = append(trace, "oa:pmc-oa-pdf")
			}
			for _, mirrorURL := range []string{PMCArticleURL(pmcid), "https://www.ebi.ac.uk/europepmc/webservices/rest/" + pmcid + "/fullTextXML"} {
				result := h.fetchURL(ctx, mirrorURL, options)
				if result.Error == "" {
					trace := append([]string{"mirror:" + pmcid}, result.Rungs...)
					return h.storeResultAlias(source, canonical, result, trace, options)
				}
			}
		}
	}
	if kind == IdentifierDOI || kind == IdentifierPMID {
		if result, ok := tryDOIMirror(); ok {
			return result
		}
	}
	if kind == IdentifierDOI {
		if result, ok := tryMirrors(); ok {
			return result
		}
		if result, ok := tryScholar(); ok {
			return result
		}
	}
	if kind == IdentifierDOI {
		doiURL := "https://doi.org/" + canonical
		trace = append(trace, "wayback")
		if snapshot, waybackErr := WaybackRawURL(ctx, h.oa, doiURL); waybackErr == nil && snapshot != "" {
			result := h.fetchURLWithPolicy(ctx, snapshot, options, false)
			if result.Error == "" {
				result.Method = "mirror:wayback"
				return h.storeResultAlias(source, canonical, result, append([]string(nil), trace...), options)
			}
		}
		if result, ok := keptThin(); ok {
			return result
		}
		// Name only what was ACTUALLY queried: Unpaywall is gated on an operator
		// email, so a keyless run must not claim to have checked it.
		checked := "OpenAlex, Semantic Scholar, Europe PMC, OpenAIRE, Zenodo, eLife, PLOS, NBER, Crossref, CORE, DOAJ, arXiv/ar5iv/OSF, and the Wayback Machine"
		skipped := " Unpaywall was SKIPPED — it requires an operator email (scholarly.contactEmail in harvester.config.json)."
		if h.resolver().contact() != "" {
			checked = "Unpaywall, " + checked
			skipped = ""
		}
		if doiMirrorFailure != nil {
			message := fmt.Sprintf(
				"Found DOI %s, but retrieval exhausted the configured open-access and fallback sources (checked %s).%s %s",
				canonical,
				checked,
				skipped,
				doiMirrorFailure.Error,
			)
			return Result{
				Source:    source,
				Error:     withRungs(message, trace),
				ErrorKind: doiMirrorFailureKind(doiMirrorFailure),
				Challenge: doiMirrorFailureChallenge(
					doiMirrorFailure,
				),
				HTTPStatus: doiMirrorFailureStatus(doiMirrorFailure),
				Rungs:      trace,
			}
		}
		message := fmt.Sprintf(
			"Found DOI %s, but no free, legal full text exists in the configured open-access sources (checked %s).%s The paper is likely paywalled — %s",
			canonical,
			checked,
			skipped,
			SearchHint(
				h.settings.searchAvailable,
				"use `search_web` to find an author preprint or the publisher's page directly.",
				"find an author preprint or the publisher's page directly, or try search_literature again with different terms.",
			),
		)
		return Result{
			Source:    source,
			Error:     withRungs(message, trace),
			ErrorKind: doiMirrorFailureKind(doiMirrorFailure),
			Challenge: doiMirrorFailureChallenge(
				doiMirrorFailure,
			),
			HTTPStatus: doiMirrorFailureStatus(doiMirrorFailure),
			Rungs:      trace,
		}
	}
	if result, ok := keptThin(); ok {
		return result
	}
	message := "all legal open-access candidates failed"
	if doiMirrorFailure != nil {
		message += " " + doiMirrorFailure.Error
	}
	return Result{
		Source:     source,
		Error:      withRungs(message, trace),
		Rungs:      trace,
		ErrorKind:  doiMirrorFailureKind(doiMirrorFailure),
		Challenge:  doiMirrorFailureChallenge(doiMirrorFailure),
		HTTPStatus: doiMirrorFailureStatus(doiMirrorFailure),
	}
}

func doiMirrorFailureKind(result *Result) string {
	if result == nil {
		return ""
	}
	return result.ErrorKind
}

func doiMirrorFailureChallenge(result *Result) bool {
	return result != nil && result.Challenge
}

func doiMirrorFailureStatus(result *Result) int {
	if result == nil {
		return 0
	}
	return result.HTTPStatus
}

func (h *Harvester) fetchOA(ctx context.Context, doi string, rungs []string, options FetchOptions) Result {
	cands, err := h.resolver().ResolveDOI(ctx, doi)
	if err != nil {
		metadataFailureKind := doiResolverErrorKind(err)
		trace := append([]string(nil), rungs...)
		var last Result
		if h.settings.doiMirrorURL != "" {
			result := h.fetchDOIMirror(ctx, DOIFrom(doi), options)
			trace = append(trace, result.Rungs...)
			if result.Error == "" {
				result.Rungs = trace
				return result
			}
			last = result
		}
		if mirrored, attempted := h.fetchDOIMirrors(ctx, DOIFrom(doi), options); attempted {
			trace = append(trace, mirrored.Rungs...)
			if mirrored.Error == "" {
				mirrored.Rungs = trace
				return mirrored
			}
			last = mirrored
		}
		if h.settings.googleScholarURL != "" {
			scholar := h.fetchScholarDOI(ctx, DOIFrom(doi), options)
			trace = append(trace, scholar.Rungs...)
			if scholar.Error == "" {
				scholar.Rungs = trace
				return scholar
			}
			last = scholar
		}
		return mergeResolverFailure(
			Result{Source: doi, Error: err.Error(), ErrorKind: metadataFailureKind, Rungs: trace},
			last,
		)
	}
	trace := append([]string(nil), rungs...)
	for _, c := range cands {
		trace = append(trace, "oa:"+c.Source)
		result := h.fetchURLWithPolicy(ctx, c.URL, options, false)
		if result.Error == "" {
			result.Rungs = append([]string(nil), trace...)
			return result
		}
	}
	if h.settings.doiMirrorURL != "" {
		result := h.fetchDOIMirror(ctx, DOIFrom(doi), options)
		if result.Error == "" {
			result.Rungs = append(trace, result.Rungs...)
			return result
		}
		trace = append(trace, result.Rungs...)
		lastProvider := result
		if mirrored, attempted := h.fetchDOIMirrors(ctx, DOIFrom(doi), options); attempted {
			trace = append(trace, mirrored.Rungs...)
			if mirrored.Error == "" {
				mirrored.Rungs = append([]string(nil), trace...)
				return mirrored
			}
			lastProvider = mirrored
		}
		if h.settings.googleScholarURL != "" {
			scholar := h.fetchScholarDOI(ctx, DOIFrom(doi), options)
			if scholar.Error == "" {
				scholar.Rungs = append(trace, scholar.Rungs...)
				return scholar
			}
			trace = append(trace, scholar.Rungs...)
			lastProvider = scholar
		}
		return Result{
			Source:     doi,
			Error:      withRungs("OA chain exhausted: "+lastProvider.Error, trace),
			ErrorKind:  lastProvider.ErrorKind,
			Challenge:  lastProvider.Challenge,
			HTTPStatus: lastProvider.HTTPStatus,
			Rungs:      trace,
		}
	}
	if mirrored, attempted := h.fetchDOIMirrors(ctx, DOIFrom(doi), options); attempted {
		if mirrored.Error == "" {
			mirrored.Rungs = append(trace, mirrored.Rungs...)
			return mirrored
		}
		trace = append(trace, mirrored.Rungs...)
		if h.settings.googleScholarURL == "" {
			return Result{
				Source:     doi,
				Error:      withRungs("OA chain exhausted: "+mirrored.Error, trace),
				ErrorKind:  mirrored.ErrorKind,
				Challenge:  mirrored.Challenge,
				HTTPStatus: mirrored.HTTPStatus,
				Rungs:      trace,
			}
		}
	}
	if h.settings.googleScholarURL != "" {
		scholar := h.fetchScholarDOI(ctx, DOIFrom(doi), options)
		if scholar.Error == "" {
			scholar.Rungs = append(trace, scholar.Rungs...)
			return scholar
		}
		trace = append(trace, scholar.Rungs...)
		return Result{
			Source:     doi,
			Error:      withRungs("OA chain exhausted: "+scholar.Error, trace),
			ErrorKind:  scholar.ErrorKind,
			Challenge:  scholar.Challenge,
			HTTPStatus: scholar.HTTPStatus,
			Rungs:      trace,
		}
	}
	return Result{Source: doi, Error: withRungs("OA chain exhausted", trace), Rungs: trace}
}
