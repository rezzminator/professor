package harvest

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FetchWithOptions routes local files, URLs, and scholarly identifiers.
func (h *Harvester) FetchWithOptions(ctx context.Context, source string, options FetchOptions) Result {
	key := canonicalNegativeKey("fetch", strings.TrimSpace(source))
	if !options.Refresh {
		if cached, ok := h.neg.get(key); ok {
			if options.SizeOnly {
				cached.Content = ""
			}
			return cached
		}
		h.flightMu.Lock()
		if flight, ok := h.flights[key]; ok {
			h.flightMu.Unlock()
			select {
			case <-flight.done:
				result := flight.result
				if options.SizeOnly {
					result.Content = ""
				}
				return result
			case <-ctx.Done():
				return Result{Source: source, Error: ctx.Err().Error()}
			}
		}
		flight := &fetchFlight{done: make(chan struct{})}
		h.flights[key] = flight
		h.flightMu.Unlock()
		result := h.fetchUnshared(ctx, source, options)
		h.flightMu.Lock()
		flight.result = result
		close(flight.done)
		delete(h.flights, key)
		h.flightMu.Unlock()
		if result.Error != "" {
			h.neg.put(key, result)
		}
		h.recordStat(source, result) // scoreboard: every terminal outcome lands in stats.jsonl
		if options.SizeOnly && result.Error == "" {
			result.Content = ""
		}
		return result
	}
	result := h.fetchUnshared(ctx, source, options)
	h.recordStat(source, result)
	return result
}

func (h *Harvester) fetchUnshared(ctx context.Context, source string, options FetchOptions) Result {
	source = strings.TrimSpace(source)
	if source == "" {
		return Result{Source: source, Error: "source is empty"}
	}
	var result Result
	if isDefiniteLocalSource(source) {
		result = h.fetchLocal(ctx, source, options)
	} else if id := ClassifyIdentifier(source); id != IdentifierNone {
		result = h.fetchKnownID(ctx, source, id, options)
	} else if isLocalSource(source) {
		result = h.fetchLocal(ctx, source, options)
	} else {
		// A bare title is intentionally ambiguous. The Python fetch tool never
		// guesses a work from title text; callers must use FindWorks and then fetch
		// the selected handle. Existing paths and unambiguous identifiers were
		// handled above.
		if strings.HasPrefix(strings.ToLower(source), "title:") {
			value := strings.Trim(strings.TrimSpace(source[len("title:"):]), "\"'")
			result = titleGuessResult(source, value)
		} else if parsed, parseErr := url.Parse(source); parseErr == nil && parsed.Scheme != "" && parsed.Host != "" {
			result = h.fetchURL(ctx, source, options)
		} else {
			result = titleGuessResult(source, source)
		}
	}
	if options.SizeOnly && result.Error == "" {
		result.Content = ""
	}
	return result
}

// isDefiniteLocalSource gives an existing or explicitly local path precedence
// over identifier heuristics. ISBN normalization intentionally ignores
// punctuation, so a filename such as 9780306406157.txt would otherwise be
// mistaken for the book identifier instead of read from disk.
func isDefiniteLocalSource(source string) bool {
	if strings.HasPrefix(strings.ToLower(source), "file://") ||
		filepath.IsAbs(source) || strings.HasPrefix(source, ".") {
		return true
	}
	_, err := os.Stat(source)
	return err == nil
}

func canonicalNegativeKey(media, source string) string {
	if media == "" {
		media = "fetch"
	}
	if ClassifyIdentifier(source) == IdentifierDOI {
		doi := DOIFrom(source)
		return media + ":doi:" + strings.ToLower(doi)
	}
	return media + ":" + source
}

func isLocalSource(source string) bool {
	if strings.HasPrefix(strings.ToLower(source), "file://") {
		return true
	}
	u, err := url.Parse(source)
	if err != nil {
		return true
	}
	if u.Scheme != "" || u.Host != "" {
		return false
	}
	if _, statErr := os.Stat(source); statErr == nil {
		return true
	}
	// Recognized file extensions remain local-path syntax even when the file is
	// missing; callers then receive a useful missing-path diagnostic instead of
	// an ambiguous scholarly-title hint.
	if DetectKind(source) != kindHTML {
		return true
	}
	return filepath.IsAbs(source) || strings.HasPrefix(source, ".") || strings.ContainsAny(source, `/\\`)
}

func (h *Harvester) fetchURL(ctx context.Context, source string, options FetchOptions) Result {
	return h.fetchURLWithPolicy(ctx, source, options, true)
}

func (h *Harvester) fetchURLWithPolicy(
	ctx context.Context,
	source string,
	options FetchOptions,
	allowOAPivot bool,
) Result {
	if err := validateFetchURL(source, false); err != nil {
		if strings.Contains(err.Error(), "unsupported URL scheme") {
			return Result{
				Source: source,
				Error: fmt.Sprintf(
					"unsupported URL scheme in %q — fetch handles http(s):// and file:// URLs, local paths, DOIs, and ISBNs.",
					source,
				),
			}
		}
		return Result{Source: source, Error: err.Error()}
	}
	if isPubMedSearchURL(source) {
		return Result{
			Source: source,
			Error: fmt.Sprintf(
				"%s is a PubMed search/results URL, not an article — use the `findWorks` tool%s to get candidate works, each with a fetch handle.",
				source,
				SearchHint(h.settings.searchAvailable,
					" (or `search`)",
					"",
				),
			),
		}
	}
	if providerResult, handled := h.fetchProviderRecord(ctx, source, options); handled {
		return providerResult
	}
	// Cache is looked up by the eventual kind for stable type partitioning. A URL
	// extension gives us an early key; response sniffing may move it to another
	// partition after the fetch.
	guess := kindFromName(source)
	fetchTarget, googleDriveFile := googleDriveDownloadURL(source)
	if !googleDriveFile {
		fetchTarget = source
	}
	if !options.Refresh {
		if googleDriveFile {
			// Before complete-file dispatch existed, Drive share URLs were cached as
			// Jina/direct HTML previews (usually only four pages). Only a cache entry
			// produced by the download rung can satisfy a Drive file request now.
			if body, kind, meta, path, ok := h.cache.loadAny(
				source,
				[]string{kindPDF, kindDOCX, kindXLSX, kindPPTX, kindCSV, kindJSON, kindTXT, kindEPUB, kindHTML},
			); ok &&
				strings.HasPrefix(meta["method"], "google-drive-download") {
				return h.resultFromCache(source, kind, body, meta, path)
			}
		} else {
			if body, meta, path, ok := h.cache.load(source, guess); ok {
				return h.resultFromCache(source, guess, body, meta, path)
			}
			// Extensionless scholarly/PDF URLs are classified from response headers
			// on the first request. Search every type partition on subsequent calls
			// so the sniffed kind remains cacheable without a sidecar index.
			if body, kind, meta, path, ok := h.cache.loadAny(
				source,
				[]string{kindPDF, kindDOCX, kindXLSX, kindPPTX, kindCSV, kindJSON, kindTXT, kindEPUB, kindHTML},
			); ok {
				return h.resultFromCache(source, kind, body, meta, path)
			}
		}
	}
	rungs := []string{}
	lastStatus := 0
	var lastErr error
	var lastPage []byte
	lastErrorKind := ""
	lastChallenge := false
	lastContentChars := 0
	var providerFailure *Result
	// loaders is this fetch's loader following (loaders.go), shared by every
	// conversion of the page so its cap holds across rungs.
	loaders := newLoaderBudget(ctx)
	emptyPDFConvert := false
	var emptyPDFBody []byte
	wrongPDF := false
	landingFollowed := false
	landingHops := 0
	if hops, ok := ctx.Value(bibliographicLandingHopKey{}).(int); ok {
		landingHops = hops
	}
	// appShellText is the detected app shell's text (see appshell.go). Once set,
	// every later rung's content that is still the shell is rejected, and the
	// usual "longer than the earlier rung" test is dropped: a real render of the
	// route may be SHORTER than the shell's explainer.
	appShellText, _ := ctx.Value(appShellKey{}).(string)
	appShellInherited := appShellText != ""
	browserShellRender := false
	staticConverterOutage := false
	// partialPage stores an HTML page an HTTP rung converted but KNOWS is
	// incomplete (its partial marker) where a render can close a gap. Only a real
	// browser loads more of the same page, so the ladder goes straight to the
	// browser rung and stores this page when the browser cannot do better. Nil
	// unless the browser rung is on.
	var partialPage func() Result
	keptExtractor := ""  // the extractor that claimed partialPage's page
	var gaps carriedGaps // a site-API gap and the next page; every rung that stores names them (withGaps)
	directClient, chromeClient := h.client, h.chrome
	switch guess {
	case kindPDF, kindDOCX, kindXLSX, kindPPTX, kindCSV, kindZIP, kindTAR, kind7Z, kindRAR:
		directClient, chromeClient = h.binaryDirectOrClient(), h.binaryChromeOrChrome()
	}
	directMediaFetch := guess == kindPDF || guess == kindDOCX || guess == kindXLSX || guess == kindPPTX ||
		guess == kindCSV || guess == kindZIP || guess == kindTAR || guess == kind7Z || guess == kindRAR || guess == kindImage
	directRung, chromeRung := rungDirect, rungChromeImpersonation
	if googleDriveFile {
		directClient, chromeClient = h.binaryDirectOrClient(), h.binaryChromeOrChrome()
		directRung, chromeRung = "google-drive-download", "google-drive-download-chrome"
	}
	// Chrome impersonation is tls-client at the wire level — no JS, no real browser
	// surface — which is why the opt-in browser rung below is the last wall-bypass step.
	for _, rung := range []struct {
		name   string
		client *http.Client
		target string
		ua     string
	}{
		{directRung, directClient, fetchTarget, h.userAgent},
		{chromeRung, chromeClient, fetchTarget, chromeUA},
	} {
		rungs = append(rungs, rung.name)
		headers := map[string]string{headerReferer: ProvenanceReferer}
		if directMediaFetch {
			headers = nil
		}
		body, status, contentType, err := getBodyWithHeaders(
			ctx, rung.client, rung.target, rung.ua, headers, h.options.MaxBytes,
		)
		if err != nil {
			lastErr = err
			lastErrorKind = errorKind(err)
			continue
		}
		lastStatus = status
		if status >= 400 && lastErrorKind == "" {
			lastErrorKind = schemeHTTP
		}
		if isChallenge(body, status) {
			lastChallenge = true
		}
		kind := classifyFetchedKind(source, contentType, body)
		if guess == kindPDF {
			if strings.HasPrefix(string(body), "%PDF-") {
				kind = kindPDF
			} else {
				wrongPDF = true
				if (kind == kindHTML || kind == kindTXT) && len(body) > 0 {
					lastPage = append(lastPage[:0], body...)
				}
				continue
			}
		}
		if (kind == kindHTML || kind == kindTXT) && len(body) > 0 {
			lastPage = append(lastPage[:0], body...)
		}
		if kind == kindImage || isImageKind(kind) {
			return Result{
				Source:     source,
				Kind:       kindImage,
				Error:      fmt.Sprintf("%s is an image — use the `fetchImage` tool, not `fetch`.", source),
				HTTPStatus: status,
				ErrorKind:  errorKindWrongKind,
			}
		}
		if kind == kindZIP || kind == kindTAR || kind == kind7Z || kind == kindRAR {
			// An EPUB is zip-SHAPED but is a book; OA book sources (OAPEN/DOAB/
			// Gutenberg/Zenodo) serve EPUB constantly. Detect it by its uncompressed
			// `mimetype` member and convert it instead of throwing the found book away.
			if kind == kindZIP && LooksLikeEpub(body) {
				kind = kindEPUB
			} else {
				return Result{
					Source:     source,
					Kind:       kindArchive,
					Error:      fmt.Sprintf("%s is a %s archive — use the `archive` tool, not `fetch`.", source, kind),
					HTTPStatus: status,
					ErrorKind:  errorKindWrongKind,
				}
			}
		}
		converted, page, err := h.convertFetchedDocument(ctx, kind, source, body, loaders)
		gaps = page.carry(gaps)
		if err != nil {
			staticConverterOutage = true // named a tool outage by convertOutageNote below (F12)
			continue
		}
		if kind == kindPDF && strings.TrimSpace(converted) == "" {
			emptyPDFConvert = true
			if len(emptyPDFBody) == 0 {
				emptyPDFBody = append([]byte(nil), body...)
			}
		}
		lastContentChars = contentChars(partialBody(converted))
		binary4xxOK := status >= 400 && kind == kindPDF && strings.HasPrefix(string(body), "%PDF-")
		// A wall or the origin's error page is no content — unless an extractor that readsSiteAPI
		// rendered the page from an API record it proved (page.siteAPI), the wall unread.
		if (len(body) == 0 && !page.siteAPI) || (isChallenge(body, status) && !page.siteAPI) ||
			(status >= 400 && !binary4xxOK && !page.siteAPI) {
			continue
		}
		if !usableContent(converted, kind) {
			continue
		}
		if kind == kindHTML && isBibliographicLanding(converted) {
			if !landingFollowed && landingHops < 1 {
				landingFollowed = true
				if linked := bibliographicDocumentURL(body, source); linked != "" && linked != source {
					followCtx := context.WithValue(ctx, bibliographicLandingHopKey{}, landingHops+1)
					if result := h.fetchURLWithPolicy(followCtx, linked, options, false); result.Error == "" {
						return result
					}
				}
			}
			continue
		}
		// The HTML ladder escalates thin extraction results. A short page is
		// commonly a JS shell or bot wall even when the HTTP status is 200;
		// plain text and converted binary documents are not subject to this
		// threshold because their bytes are already the requested artifact,
		// nor is a page a per-site extractor recognised (a short thread is
		// still that site's content, never a shell).
		if kind == kindHTML && page.extractor == "" && contentChars(partialBody(converted)) < 500 {
			continue
		}
		// A page a per-site extractor recognised is that site's content by
		// construction — never an app shell, so it never pays for the probe.
		if kind == kindHTML && !googleDriveFile && page.extractor == "" {
			if appShellText != "" {
				if sameAsShell(appShellText, converted) {
					continue
				}
			} else if looksLikeClientApp(body) && h.probeAppShell(ctx, rung.client, rung.ua, fetchTarget, body) {
				appShellText = converted
				continue
			}
		}
		status = page.deliveredStatus(status) // the site API delivered the page, not the wall
		method := rung.name
		if kind == kindTXT {
			method = "plain-text"
		}
		if kind == kindHTML && partialReason(converted) != "" && h.settings.browser && !isPrivateURL(source) &&
			!googleDriveFile && guess != kindPDF && page.renderMayComplete {
			partialPage = func() Result {
				stored := page.withGaps(h.localizedImages(ctx, kind, converted, source), gaps, loaders)
				return h.storeResult(source, kind, method, stored, int64(len(body)), status, rungs, options)
			}
			keptExtractor = page.extractor
			break
		}
		converted = page.withGaps(h.localizedImages(ctx, kind, converted, source), gaps, loaders)
		return h.storeResult(source, kind, method, converted, int64(len(body)), status, rungs, options)
	}
	if googleDriveFile {
		message := fmt.Sprintf(
			"Could not download the complete Google Drive file from %s — make the file available to anyone with the link, or download it locally and pass its path. Harvester will not substitute Drive's truncated preview page for the file.",
			source,
		)
		if lastStatus >= 400 {
			message = fmt.Sprintf(
				"Could not download the complete Google Drive file from %s (HTTP %d) — make the file available to anyone with the link, or download it locally and pass its path. Harvester will not substitute Drive's truncated preview page for the file.",
				source,
				lastStatus,
			)
		}
		return Result{
			Source:     source,
			HTTPStatus: lastStatus,
			Error:      message,
			ErrorKind:  lastErrorKind,
			Challenge:  lastChallenge,
			Rungs:      rungs,
		}
	}
	// No reader or archive copy stands in for a missing origin, nor for a hash route's view (hashRouteShell).
	originGone := originMissing(lastStatus, lastChallenge, source) || hashRouteShell(ctx, source, lastPage)
	if !isPrivateURL(source) && guess != kindPDF && partialPage == nil && !originGone {
		rungs = append(rungs, "jina")
		target := strings.TrimRight(h.options.JinaURL, "/") + "/" + source
		body, status, _, err := getBody(ctx, h.jina, target, h.userAgent, h.options.MaxBytes)
		if err != nil {
			lastErr = err
			lastErrorKind = errorKind(err)
			// getBody returns status=0 on every transport-error path; letting
			// that clobber a genuine earlier HTTP status would make the
			// receipt report HTTPStatus 0 for a walled 403.
		} else {
			lastStatus = status
		}
		if err == nil && status < 400 && !isChallenge(body, status) && jinaTargetError(body) == 0 {
			// Jina Reader already returns clean Markdown. Feeding it back into an
			// HTML converter loses headings and code blocks, so preserve it as the
			// original HTML-source kind for cache/type semantics.
			converted, convErr := quoraReaderPage(source, pageText(stripJinaEnvelope(string(body)))), error(nil)
			if convErr == nil && usableContent(converted, kindHTML) && !isBibliographicLanding(converted) &&
				!sameAsShell(appShellText, converted) {
				stored := h.readerPageChecked(ctx, source, converted).withGaps(converted, gaps, loaders)
				return h.storeResult(source, kindHTML, "jina", stored, int64(len(body)), status, rungs, options)
			}
		}
	}
	// defuddle.md — a second keyless reader beside Jina (different infra,
	// different blocks), tried before the legal mirror pivot.
	if !isPrivateURL(source) && guess != kindPDF && partialPage == nil && !originGone {
		rungs = append(rungs, "defuddle")
		target := "https://defuddle.md/" + source
		body, status, _, err := getBody(ctx, h.client, target, h.userAgent, h.options.MaxBytes)
		lastErrorKind, lastStatus, lastErr = noteRungOutcome(err, status, lastErr, lastErrorKind, lastStatus)
		if err == nil && status < 400 && !isChallenge(body, status) {
			converted := pageText(stripDefuddleEnvelope(string(body)))
			longer := contentChars(converted) > lastContentChars || appShellText != ""
			if usableContent(converted, kindHTML) && longer && !isBibliographicLanding(converted) &&
				!sameAsShell(appShellText, converted) {
				return h.storeResult(
					source,
					kindHTML,
					"defuddle-reader",
					h.readerPageChecked(ctx, source, converted).withGaps(converted, gaps, loaders),
					int64(len(body)),
					status,
					rungs,
					options,
				)
			}
		}
	}
	// Real-browser rung (opt-in, fetch.browser): Patchright + system
	// Chrome. Passes passive bot walls — including the Cloudflare MANAGED
	// challenge tier — that no HTTP-client trick can, because it holds a real
	// browser surface. It never solves anything interactive. Sits after
	// defuddle and before the legal mirror pivot, exactly like the retired
	// Python dispatch ladder.
	browserRan := false
	browserEmptyRender := false
	browserUnavailable := ""
	browserPolicyRefused := false
	converterOutage := false
	if h.settings.browser && !isPrivateURL(source) && guess != kindPDF {
		rungs = append(rungs, "browser")
		if browserFetcher, ok := h.options.Converter.(BrowserFetcher); !ok {
			browserUnavailable = "no BrowserFetcher adapter is wired into this Harvester"
		} else {
			// Headless first, headed only on a wall — the policy lives in
			// renderHeadlessFirst (gateway.go) so this ladder and the gateway
			// can never drift apart on when a visible window opens.
			outcome := renderHeadlessFirst(ctx, browserFetcher, source)
			html, status, err := outcome.html, outcome.status, outcome.err
			switch {
			case errors.Is(err, ErrBrowserPolicyDenied):
				// The SSRF guard refused — a PERMANENT policy answer about
				// this address, never an outage and never IP reputation.
				browserPolicyRefused = true
				log.Printf("harvest: browser rung refused %s by policy: %v", logSource(source), err)
			case err != nil:
				browserUnavailable = err.Error()
				log.Printf("harvest: browser rung could not run for %s: %v", logSource(source), err)
			case html == "" || blankRenderPage(html):
				// The render COMPLETED and found nothing — a real attempt with
				// an empty result, never an outage. Chrome serialises at least
				// a minimal document for ANY navigated page, so emptiness is
				// judged on visible TEXT, not on the raw string.
				browserRan = true
				browserEmptyRender = true
			default:
				browserRan = true
				if isChallenge([]byte(html), status) {
					// A challenge page that is merely LONGER than what the
					// earlier rungs extracted must never be accepted as content.
					// The flag travels even when rung one saw no challenge: the
					// browser surface is what identified the wall.
					lastChallenge = true
					log.Printf("harvest: browser rung hit a challenge wall for %s (HTTP %d)", logSource(source), status)
				} else {
					converted, page, convErr := h.convertFetchedDocument(ctx, kindHTML, source, []byte(html), loaders)
					switch {
					case convErr != nil:
						// The render SUCCEEDED; the conversion step failing is
						// a tool outage on this server — it must never read as
						// "the wall won".
						converterOutage = true
						log.Printf("harvest: browser rung conversion failed for %s: %v", logSource(source), convErr)
					case sameAsShell(appShellText, converted):
						// The bundle did not produce route content in a real
						// browser either — the render is still the shell.
						browserShellRender = true
						log.Printf("harvest: browser rung rendered only the app shell for %s", logSource(source))
					case usableContent(converted, kindHTML) && !isBibliographicLanding(converted) &&
						!originMissing(status, false, source) && browserRenderWins(ctx, browserCandidate{
						source:        source,
						finalURL:      outcome.finalURL,
						converted:     converted,
						extractor:     page.extractor,
						earlierChars:  lastContentChars,
						appShell:      appShellText != "",
						kept:          partialPage != nil,
						keptExtractor: keptExtractor,
					}):
						return h.storeResult(
							source,
							kindHTML,
							"browser-chrome",
							page.withGaps(converted, gaps, loaders),
							int64(len(html)),
							status,
							rungs,
							options,
						)
					}
				}
			}
		}
	}
	if partialPage != nil {
		// The browser rung loaded no more than the HTTP rung did (walled,
		// failed, or no longer): the partial page, still flagged, is the answer.
		return partialPage()
	}
	// A publisher wall often leaves citation_pdf_url/citation_doi metadata in
	// the HTML error body. Reuse that body before giving up; this is the same
	// legal mirror pivot used for DOI inputs, and avoids a redundant page fetch.
	if len(lastPage) > 0 {
		metaDOI, metaPDF, _ := ExtractMetaLinks(string(lastPage))
		if metaPDF != "" && metaPDF != source {
			if result := h.fetchURLWithPolicy(ctx, metaPDF, options, allowOAPivot); result.Error == "" {
				trace := append(append([]string(nil), rungs...), "oa:citation_pdf_url")
				return h.storeResultAlias(source, source, result, trace, options)
			}
		}
		if allowOAPivot && metaDOI != "" && !strings.EqualFold(metaDOI, DOIFrom(source)) {
			result := h.fetchOA(ctx, metaDOI, rungs, options)
			if result.Error == "" {
				if isMirrorProviderMethod(result.Method) {
					return h.storeResultAlias(source, source, result, result.Rungs, options)
				}
				return result
			}
			if len(result.Rungs) > len(rungs) {
				rungs = result.Rungs
			}
			if hasProviderDiagnostic(result.Error) {
				resultCopy := result
				providerFailure = &resultCopy
				lastStatus = result.HTTPStatus
				lastErrorKind = result.ErrorKind
				lastChallenge = result.Challenge
			}
		}
	}
	// Scholarly URLs get one legal OA pivot after the web ladder. This is
	// intentionally opt-in by recognizable DOI; arbitrary URLs must not trigger
	// broad discovery traffic.
	if doi := DOIFrom(source); allowOAPivot && doi != "" {
		result := h.fetchOA(ctx, doi, rungs, options)
		if result.Error == "" {
			if isMirrorProviderMethod(result.Method) {
				return h.storeResultAlias(source, source, result, result.Rungs, options)
			}
			return result
		}
		if len(result.Rungs) > len(rungs) {
			rungs = result.Rungs
		}
		if hasProviderDiagnostic(result.Error) {
			resultCopy := result
			providerFailure = &resultCopy
			lastStatus = result.HTTPStatus
			lastErrorKind = result.ErrorKind
			lastChallenge = result.Challenge
		}
	}
	// Any public source may have a legal Wayback snapshot, not only a DOI
	// landing page. Avoid recursing when the snapshot itself fails.
	if !strings.Contains(strings.ToLower(source), "web.archive.org") && !isPrivateURL(source) && !originGone {
		rungs = append(rungs, "wayback")
		if snapshot, wbErr := WaybackRawURL(ctx, h.oa, source); wbErr == nil && snapshot != "" {
			snapshotCtx := ctx
			if appShellText != "" {
				// A snapshot of a client-rendered route is the same shell; the
				// recursion rejects it before it is stored.
				snapshotCtx = context.WithValue(ctx, appShellKey{}, appShellText)
			}
			if result := h.fetchURLWithPolicy(snapshotCtx, snapshot, options, false); result.Error == "" {
				result.Source = source
				result.Rungs = append([]string(nil), rungs...)
				return result
			}
		}
	}
	// LAST resort for a remote PDF whose text layer converted EMPTY (a scan):
	// ONE forced-OCR pass. Every faster rescue just failed; OCR is slow, so it
	// fires exactly when nothing else worked. Needs an OCR-capable converter —
	// a plain Converter skips this rung and the ladder's error stands.
	ocrRan, ocrBackendFailed := false, false
	if emptyPDFConvert && len(emptyPDFBody) > 0 {
		if ocrConverter, ok := h.options.Converter.(OCRConverter); ok {
			rungs = append(rungs, "ocr")
			ocrRan = true
			ocrConverted, ocrErr := ocrConverter.ConvertOCR(ctx, kindPDF, source, emptyPDFBody)
			ocrConverted = pageText(ocrConverted)
			switch {
			case ocrErr != nil:
				ocrBackendFailed = true
				log.Printf("harvest: OCR escalation backend failed for %s: %v", logSource(source), ocrErr)
			case usableContent(ocrConverted, kindPDF):
				return h.storeResult(
					source,
					kindPDF,
					"pdf:ocr",
					ocrConverted,
					int64(len(emptyPDFBody)),
					lastStatus,
					rungs,
					options,
				)
			}
		}
	}
	message := FailureMessage(source, lastStatus, lastErrorKind, lastChallenge, h.settings.searchAvailable)
	if wrongPDF {
		message = fmt.Sprintf(
			"%s has a .pdf address but did not return a PDF (non-PDF content — likely an HTML paywall/login wall or a bot-block). %s",
			source,
			SearchHint(h.settings.searchAvailable,
				"Use `search` to find an open-access copy.",
				"Find an open-access copy with findWorks or another URL.",
			),
		)
	}
	if emptyPDFConvert {
		// A BROKEN OCR backend and an OCR pass that legitimately found no text
		// are different answers; collapsing them would let an outage read as
		// "this PDF has nothing in it".
		switch {
		case ocrBackendFailed:
			message = fmt.Sprintf(
				"Downloaded the PDF from %s but it converted to EMPTY text, and the OCR escalation could not RUN (converter backend error — see the server log). That is a tool outage, not proof the PDF is textless: %s",
				source,
				SearchHint(h.settings.searchAvailable,
					"retry, or use `search` to find an alternative copy.",
					"retry, or find an alternative copy with findWorks or another URL.",
				),
			)
		case ocrRan:
			message = fmt.Sprintf(
				"Downloaded the PDF from %s but it converted to EMPTY text. It is likely scanned/image-only, corrupt, or password-protected — an OCR pass was already attempted on this copy and produced nothing. %s",
				source,
				SearchHint(h.settings.searchAvailable,
					"Use `search` to find an alternative copy.",
					"Find an alternative copy with findWorks or another URL.",
				),
			)
		default:
			message = fmt.Sprintf(
				"Downloaded the PDF from %s but it converted to EMPTY text. It is likely scanned/image-only, corrupt, or password-protected — if it's a scanned/image-only PDF, set convert.pdfOcr=true in harvester.config.json to OCR it. %s",
				source,
				SearchHint(h.settings.searchAvailable,
					"Use `search` to find an alternative copy.",
					"Find an alternative copy with findWorks or another URL.",
				),
			)
		}
	}
	// A dead-ended challenge must say what the real-browser rung did — the
	// states are different answers and never collapse: DISABLED (never
	// attempted), REFUSED BY POLICY (the SSRF guard said no — permanent,
	// not an outage), COULD NOT RUN (enabled but the environment/launch
	// failed — an outage, not proof of IP reputation), RAN BUT CONVERSION
	// FAILED (the wall was beaten and the tool dropped it — an outage),
	// RAN AND RETURNED AN EMPTY PAGE, RAN AND RENDERED ONLY THE APP SHELL,
	// RAN AND STILL BLOCKED. The addendum fires on ANY challenge or app-shell
	// terminal, including one only the browser surface identified, on an
	// empty render, and whenever the rung ran at all so a completed attempt
	// is never silent.
	appShellFailure := appShellText != "" && !appShellInherited
	if appShellFailure {
		// The static page was READ and proved route-independent: an app
		// shell is a named failure, never a generic wall and never content.
		lastErrorKind = "app_shell"
		message = fmt.Sprintf(
			"%s is a JavaScript app shell: a sibling path that cannot exist returned the same page, so its static HTML is identical for every route and this route's content only exists after the app's JavaScript runs. No rendering rung returned the route's content.",
			source,
		)
	}
	if appShellFailure || (guess != kindPDF && (lastChallenge || browserRan || browserPolicyRefused)) {
		switch {
		case !h.settings.browser:
			message += " No real-browser bypass was attempted: this server's Patchright + system-Chrome rung is DISABLED (opt-in) — set fetch.browser=true in harvester.config.json to enable it."
		case browserPolicyRefused:
			message += " The real-browser rung did not run because this server's SSRF guard refused the address (private or internal network). That is policy working as designed, not an outage."
		case converterOutage:
			message += " The real-browser rung DID run and got real content past the wall, but the conversion step then failed on this server — a tool outage, not proof of IP reputation: " + SearchHint(
				h.settings.searchAvailable,
				"retry, or use `search` to find an alternative copy.",
				"retry, or find an alternative copy with findWorks or another URL.",
			)
		case browserUnavailable != "":
			message += fmt.Sprintf(
				" The real-browser rung (fetch.browser) could NOT RUN (%s) — that is a tool outage on this server, not proof of IP reputation.",
				browserUnavailable,
			)
		case browserEmptyRender:
			message += " The real-browser rung DID run and returned an EMPTY page — a completed attempt with nothing usable, not an outage."
		case browserShellRender:
			message += " The real-browser rung DID run and rendered only the shell — the app's JavaScript produced no route content in a real browser either."
		case browserRan:
			message += " The real-browser rung (Patchright + system Chrome) DID run against this wall and still could not pass it."
		}
	}
	if lastContentChars == 0 && len(lastPage) > 0 {
		lastContentChars = contentChars(string(lastPage))
	}
	if lastErr != nil && strings.Contains(strings.ToLower(lastErr.Error()), "private") {
		message = lastErr.Error()
	}
	if providerFailure != nil {
		message += " " + providerFailure.Error
	}
	message, lastErrorKind = convertOutageNote(
		message,
		lastErrorKind,
		staticConverterOutage,
		emptyPDFConvert,
		wrongPDF,
		appShellFailure,
	)
	message = withRungs(loaders.loginWallNote(source, message), rungs)
	return Result{
		Source:       source,
		HTTPStatus:   lastStatus,
		Error:        message,
		ErrorKind:    lastErrorKind,
		Challenge:    lastChallenge,
		Chars:        lastContentChars,
		ContentChars: lastContentChars,
		Rungs:        rungs,
	}
}

var googleDriveFileIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,256}$`)

// googleDriveDownloadURL turns an uploaded Drive file's share/view link into
// the public complete-file endpoint. The share page is not the artifact: its
// reader-facing HTML commonly exposes only the first four PDF pages.
func googleDriveDownloadURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(strings.TrimSuffix(u.Hostname(), "."), "drive.google.com") {
		return "", false
	}
	var id string
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) >= 3 && parts[0] == "file" && parts[1] == "d" {
		id = parts[2]
	} else if len(parts) == 1 && (parts[0] == "open" || parts[0] == "uc") {
		id = u.Query().Get("id")
	}
	if !googleDriveFileIDPattern.MatchString(id) {
		return "", false
	}
	target := &url.URL{Scheme: schemeHTTPS, Host: "drive.usercontent.google.com", Path: "/download"}
	query := target.Query()
	query.Set("id", id)
	query.Set("export", "download")
	query.Set("confirm", "t")
	target.RawQuery = query.Encode()
	return target.String(), true
}

func isPubMedSearchURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Hostname(), "pubmed.ncbi.nlm.nih.gov") {
		return false
	}
	return strings.Contains(strings.ToLower(u.Path), "/search") ||
		strings.Contains(strings.ToLower(u.RawQuery), "term=")
}

func isPrivateURL(source string) bool {
	// Jina's forwarding decision is lexical in the oracle. A public hostname
	// whose local resolver is unavailable must still be offered to Jina; the
	// direct/chrome transports perform the stronger DNS-pinned fetch check.
	return IsPrivateHost(source)
}
