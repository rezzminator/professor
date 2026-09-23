package harvest

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

func (r *Resolver) ResolveBook(ctx context.Context, query string) ([]Candidate, error) {
	query = strings.TrimSpace(strings.Trim(query, "\"'"))
	if query == "" {
		return nil, fmt.Errorf("empty book query")
	}
	ctx = resolverContext(ctx, r)
	client := r.client()
	var out []Candidate
	// failures collects every provider's own error (F14): five of the six
	// book providers used to drop theirs silently, so a total outage read as
	// "no copy exists" — the exact same shape as a genuine empty shelf.
	// hathitrust's own log line (below) keeps the operator-visible voice for
	// its lookup; the C23 activity-log ratchet (arch-check.sh) refuses a
	// SECOND bare log call here, so the other providers' failures
	// travel with the RETURNED error only — still enough for the caller to
	// tell "the lookup failed" from "nothing found" (F14's actual ask), and
	// only an ALL-failed query becomes a returned error — a partial result
	// keeps returning candidates, matching ResolveDOI's own precedent for a
	// mixed success/failure fan-out.
	var failures []doiMetadataFailure
	noteFailure := func(provider string, err error) {
		if err == nil {
			return
		}
		failures = append(failures, doiMetadataFailure{provider: provider, err: err})
	}
	// OAPEN exposes direct ORIGINAL bitstreams for open academic books.
	var search string
	if isbn := NormalizeISBN(query); isbn != "" {
		search = "isbn:" + isbn
	} else {
		search = "title:" + query
	}
	var oapen []struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	}
	// getJSON, not getBody: a JSON-decoding path refuses an over-ceiling body
	// by name rather than truncating it into a decode failure.
	if err := getJSON(
		ctx,
		client,
		"https://library.oapen.org/rest/search?query="+url.QueryEscape(search)+"&limit=5",
		&oapen,
	); err != nil {
		noteFailure("oapen", err)
	} else {
		for _, item := range oapen {
			if item.UUID == "" {
				continue
			}
			detailURL := "https://library.oapen.org/rest/items/" + url.PathEscape(item.UUID) + "?expand=bitstreams"
			var detail struct {
				Bitstreams []struct {
					Bundle string `json:"bundleName"`
					Mime   string `json:"mimeType"`
					Link   string `json:"retrieveLink"`
				} `json:"bitstreams"`
			}
			if err := getJSON(ctx, client, detailURL, &detail); err != nil {
				continue
			}
			for _, bs := range detail.Bitstreams {
				if bs.Bundle == "ORIGINAL" && bs.Mime == mediaTypePDF && bs.Link != "" {
					link := bs.Link
					if !strings.HasPrefix(link, schemeHTTP) {
						link = "https://library.oapen.org" + link
					}
					out = append(
						out,
						Candidate{URL: link, Source: "oapen", Priority: 8, Kind: kindPDF, Title: item.Name},
					)
					break
				}
			}
		}
	}
	// Gutendex is useful for public-domain title searches and has no key.
	if NormalizeISBN(query) == "" {
		var data struct {
			Results []struct {
				Title     string            `json:"title"`
				Copyright bool              `json:"copyright"`
				Formats   map[string]string `json:"formats"`
			} `json:"results"`
		}
		if err := getJSON(ctx, client, "https://gutendex.com/books?search="+url.QueryEscape(query), &data); err != nil {
			noteFailure(sourceGutenberg, err)
		} else {
			for _, book := range data.Results {
				if book.Copyright {
					continue
				}
				link, kind := preferredTextFormat(book.Formats)
				if link != "" {
					out = append(
						out,
						Candidate{
							URL:      link,
							Source:   sourceGutenberg,
							Priority: candidatePriority(sourceGutenberg, "", "", kind),
							Kind:     kind,
							Title:    book.Title,
						},
					)
				}
			}
		}
	}
	// OpenLibrary index -> Internet Archive full text, with the oracle's
	// public-domain gate. Never serve controlled-lending copies.
	isbn := NormalizeISBN(query)
	ocaid := ""
	if isbn != "" {
		var data struct {
			OCAID string `json:"ocaid"`
		}
		if err := getJSON(ctx, client, "https://openlibrary.org/isbn/"+isbn+".json", &data); err != nil {
			noteFailure("openlibrary", err)
		} else {
			ocaid = data.OCAID
		}
	} else {
		var searchData struct {
			Docs []struct {
				IA     []string `json:"ia"`
				Access string   `json:"ebook_access"`
			} `json:"docs"`
		}
		searchURL := "https://openlibrary.org/search.json?q=" + url.QueryEscape(
			query,
		) + "&fields=ia,ebook_access,title&limit=5"
		if err := getJSON(ctx, client, searchURL, &searchData); err != nil {
			noteFailure("openlibrary", err)
		} else {
			for _, doc := range searchData.Docs {
				if doc.Access == accessPublic && len(doc.IA) > 0 {
					ocaid = doc.IA[0]
					break
				}
			}
		}
	}
	if ocaid != "" {
		var meta struct {
			Metadata struct {
				Restricted string `json:"access-restricted-item"`
			} `json:"metadata"`
			Files []struct {
				Name string `json:"name"`
			} `json:"files"`
		}
		if err := getJSON(
			ctx,
			client,
			"https://archive.org/metadata/"+url.PathEscape(ocaid),
			&meta,
		); err != nil {
			noteFailure(sourceInternetArchive, err)
		} else if meta.Metadata.Restricted != "true" {
			for _, file := range meta.Files {
				if strings.HasSuffix(strings.ToLower(file.Name), ".pdf") &&
					!strings.HasSuffix(strings.ToLower(file.Name), "_encrypted.pdf") {
					out = append(
						out,
						Candidate{
							URL:      "https://archive.org/download/" + ocaid + "/" + url.PathEscape(file.Name),
							Source:   sourceInternetArchive,
							Priority: 18,
							Kind:     kindPDF,
						},
					)
				}
				if strings.HasSuffix(strings.ToLower(file.Name), "_djvu.txt") {
					out = append(
						out,
						Candidate{
							URL:      "https://archive.org/download/" + ocaid + "/" + url.PathEscape(file.Name),
							Source:   sourceInternetArchive,
							Priority: 18,
							Kind:     kindTXT,
						},
					)
				}
			}
		}
	}
	// DOAB's API exposes an open download URL for academic books. A DOI URL
	// is expanded through the same legal article resolver instead of fetched as
	// an unverified publisher candidate.
	var doab []struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	}
	if isbn != "" {
		// DOAB's ISBN endpoint is a JSON POST; its search endpoint silently
		// returns no ISBN matches for several catalogues.
		noteFailure("doab", postJSON(
			ctx,
			client,
			"https://directory.doabooks.org/rest/items/find-by-metadata-field",
			map[string]string{"key": "oapen.relation.isbn", "value": isbn},
			&doab,
		))
	} else {
		noteFailure(
			"doab",
			getJSON(ctx, client, "https://directory.doabooks.org/rest/search?query="+url.QueryEscape(query), &doab),
		)
	}
	for _, item := range doab[:min(len(doab), 5)] {
		if item.UUID == "" {
			continue
		}
		var detail struct {
			Name       string         `json:"name"`
			Metadata   []doabMetadata `json:"metadata"`
			Bitstreams []struct {
				Metadata []doabMetadata `json:"metadata"`
			} `json:"bitstreams"`
		}
		if getJSON(
			ctx,
			client,
			"https://directory.doabooks.org/rest/items/"+url.PathEscape(item.UUID)+"?expand=metadata,bitstreams",
			&detail,
		) != nil {
			continue
		}
		pools := make([]doabMetadata, 0, len(detail.Metadata))
		pools = append(pools, detail.Metadata...)
		for _, bitstream := range detail.Bitstreams {
			pools = append(pools, bitstream.Metadata...)
		}
		for _, m := range pools {
			if m.Key != "oapen.identifier.downloadUrl" || m.Value == "" {
				continue
			}
			if doi := DOIFrom(m.Value); doi != "" {
				if cands, resolveErr := r.ResolveDOI(ctx, doi); resolveErr == nil && len(cands) > 0 {
					out = append(out, cands...)
					continue
				}
			}
			out = append(out, Candidate{URL: m.Value, Source: "doab", Priority: 22, Kind: kindPDF, Title: detail.Name})
		}
	}
	// HathiTrust full-view volumes (public domain) — rights-gated like IA.
	hathiOut, hathiErr := r.hathitrust(ctx, client, query)
	out = append(out, hathiOut...)
	noteFailure("hathitrust", hathiErr)
	if key := strings.TrimSpace(r.GoogleBooksAPIKey); key != "" {
		var data struct {
			Items []struct {
				Access struct {
					Public bool `json:"publicDomain"`
					PDF    struct {
						Link string `json:"downloadLink"`
					} `json:"pdf"`
				} `json:"accessInfo"`
			} `json:"items"`
		}
		bookQuery := query
		if isbn := NormalizeISBN(query); isbn != "" {
			bookQuery = "isbn:" + isbn
		}
		// The key rides this URL's query (safeURL/sanitizeTransportError,
		// safeurl.go, strip it from every error/log site downstream — F15).
		bookURL := "https://www.googleapis.com/books/v1/volumes?q=" + url.QueryEscape(
			bookQuery,
		) + "&country=US&key=" + url.QueryEscape(
			key,
		)
		if err := getJSON(ctx, client, bookURL, &data); err != nil {
			noteFailure("googlebooks", err)
		} else {
			for _, item := range data.Items {
				if item.Access.Public && item.Access.PDF.Link != "" {
					out = append(
						out,
						Candidate{URL: item.Access.PDF.Link, Source: "googlebooks", Priority: 50, Kind: kindPDF},
					)
				}
			}
		}
	}
	if len(out) == 0 && len(failures) > 0 {
		kind := errorKindConnect
		for _, failure := range failures {
			if candidateKind := doiMetadataFailureKind(failure.err); candidateKind != "" {
				kind = candidateKind
				break
			}
		}
		return nil, &doiMetadataError{subject: "book", failures: failures, kind: kind}
	}
	return sortCandidates(out), nil
}

// findOpenLibrary and findGutendex are two findWorks sources, not one: Gutendex
// often answers in 25-45 s, and sharing one source with it cost Open Library's
// one-second answer at every findWorks deadline.
func (r *Resolver) findOpenLibrary(ctx context.Context, client *http.Client, query string, limit int) []Candidate {
	var openLibrary struct {
		Docs []struct {
			Title   string   `json:"title"`
			Authors []string `json:"author_name"`
			Year    int      `json:"first_publish_year"`
			ISBN    []string `json:"isbn"`
			Access  string   `json:"ebook_access"`
		} `json:"docs"`
	}
	raw := "https://openlibrary.org/search.json?q=" + url.QueryEscape(
		query,
	) + "&fields=title,author_name,first_publish_year,isbn,ebook_access&limit=" + fmt.Sprint(
		limit,
	)
	out := []Candidate{}
	if err := getJSON(ctx, client, raw, &openLibrary); err == nil {
		for _, book := range openLibrary.Docs {
			if len(book.ISBN) == 0 {
				continue
			}
			out = append(
				out,
				Candidate{
					URL:     "isbn:" + book.ISBN[0],
					Source:  "openlibrary",
					Kind:    kindBook,
					Title:   book.Title,
					Authors: formatAuthors(book.Authors),
					Year:    book.Year,
					Free:    book.Access,
					Match:   roundMatch(titleMatch(query, book.Title)),
				},
			)
		}
	}
	return out
}

func (r *Resolver) findGutendex(ctx context.Context, client *http.Client, query string, _ int) []Candidate {
	out := []Candidate{}
	var gutendex struct {
		Results []struct {
			Title     string `json:"title"`
			Copyright bool   `json:"copyright"`
			Authors   []struct {
				Name string `json:"name"`
			} `json:"authors"`
			Formats map[string]string `json:"formats"`
		} `json:"results"`
	}
	if err := getJSON(ctx, client, "https://gutendex.com/books?search="+url.QueryEscape(query), &gutendex); err == nil {
		for _, book := range gutendex.Results[:min(len(gutendex.Results), 3)] {
			if book.Copyright {
				continue
			}
			handle, _ := preferredTextFormat(book.Formats)
			if handle == "" {
				continue
			}
			authors := make([]string, 0, len(book.Authors))
			for _, author := range book.Authors {
				authors = append(authors, author.Name)
			}
			out = append(
				out,
				Candidate{
					URL:     handle,
					Source:  sourceGutenberg,
					Kind:    kindBook,
					Title:   book.Title,
					Authors: formatAuthors(authors),
					Free:    "pd",
					Match:   roundMatch(titleMatch(query, book.Title)),
				},
			)
		}
	}
	return out
}

func preferredTextFormat(formats map[string]string) (string, string) {
	for _, prefix := range []string{mediaTypeHTML, mediaTypePlain} {
		keys := make([]string, 0)
		for mime := range formats {
			if strings.HasPrefix(mime, prefix) {
				keys = append(keys, mime)
			}
		}
		sort.Strings(keys)
		for _, mime := range keys {
			if formats[mime] != "" {
				kind := kindHTML
				if prefix == mediaTypePlain {
					kind = kindTXT
				}
				return formats[mime], kind
			}
		}
	}
	return "", ""
}

// hathitrust resolves an ISBN to FULL VIEW (public-domain) volumes only;
// search-only/lending items are skipped (legality gate, mirrors
// internetarchive's). Endpoint verified live 2026-08-22 — note the working form
// is `/brief/isbn/{id}.json`, NOT `/brief/json/{isbn}` (that 400s).
// hathitrust returns (nil, nil) only for a non-ISBN query (nothing to look
// up); a failed lookup returns (nil, err) — the error travels with the
// result (F14) rather than being swallowed into the same nil the caller gets
// for "no full-view volume exists".
func (r *Resolver) hathitrust(ctx context.Context, client *http.Client, query string) ([]Candidate, error) {
	isbn := NormalizeISBN(query)
	if isbn == "" {
		return nil, nil
	}
	var data struct {
		Items []struct {
			Rights string `json:"usRightsString"`
			URL    string `json:"itemURL"`
		} `json:"items"`
	}
	endpoint := "https://catalog.hathitrust.org/api/volumes/brief/isbn/" + url.PathEscape(isbn) + ".json"
	// getJSON, not getBody: a JSON-decoding path refuses an over-ceiling body
	// by name rather than truncating it into a decode failure.
	if err := getJSON(ctx, client, endpoint, &data); err != nil {
		// A failed LOOKUP (transport, HTTP status, or decode) is an outage, not
		// evidence of absence — say so loudly.
		log.Printf(
			"harvest: hathitrust %s lookup FAILED (err %v) — treated as no-copy, not as 'no full-view volume exists'",
			isbn,
			err,
		)
		return nil, err
	}
	out := []Candidate{}
	for _, item := range data.Items {
		if item.Rights != "Full view" || item.URL == "" {
			continue
		}
		out = append(
			out,
			Candidate{
				URL:      item.URL,
				Source:   "hathitrust",
				Priority: candidatePriority("hathitrust", "", "", kindHTML),
				Kind:     kindHTML,
				Free:     "pd",
			},
		)
	}
	return out, nil
}
