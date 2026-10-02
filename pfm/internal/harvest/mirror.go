package harvest

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// idToPMCID converts through the NCBI ID converter. A nil resolver sends no
// operator identity; the ladder passes its own so NCBI sees the contact.
func idToPMCID(ctx context.Context, client *http.Client, id string, r *Resolver) (string, error) {
	if client == nil {
		client = safeHTTPClientTimeout(false, 15*time.Second)
	}
	raw := r.withContact(
		"https://pmc.ncbi.nlm.nih.gov/tools/idconv/api/v1/articles/?ids="+url.QueryEscape(
			id,
		)+"&format=json&tool=harvester-mcp",
		"email",
	)
	var data struct {
		Records []struct {
			PMCID string `json:"pmcid"`
		} `json:"records"`
	}
	// getJSONBody, not getBody: a JSON-decoding path refuses an over-ceiling
	// body by name rather than truncating it into a decode failure.
	if err := getJSONBody(ctx, client, raw, r.scholarlyUA(), nil, 1<<20, &data); err != nil {
		return "", err
	}
	if len(data.Records) == 0 || data.Records[0].PMCID == "" {
		return "", nil
	}
	return data.Records[0].PMCID, nil
}

func PMCArticleURL(pmcid string) string {
	return "https://pmc.ncbi.nlm.nih.gov/articles/" + pmcid + "/"
}

var pmcOAPDFLinkRe = regexp.MustCompile(`<link[^>]+format="pdf"[^>]+href="([^"]+)"`)

// PMCOAPDFURL resolves a PMCID through the PMC OA service (oa.fcgi) to a DIRECT
// downloadable PDF URL. Verified live 2026-08-22: NCBI's own API still hands out
// `ftp://ftp.ncbi.nlm.nih.gov/pub/pmc/...` hrefs whose plain paths now 404 — the
// files live under `deprecated/`, and the rewritten https path serves the real
// PDF. Returns "" when no pdf-format link exists.
func PMCOAPDFURL(ctx context.Context, client *http.Client, pmcid string) (string, error) {
	body, status, _, err := getBody(
		ctx,
		client,
		"https://www.ncbi.nlm.nih.gov/pmc/utils/oa/oa.fcgi?id="+url.QueryEscape(pmcid),
		defaultUA,
		10<<20,
	)
	if err != nil || status >= 400 {
		return "", fmt.Errorf("pmc oa.fcgi lookup failed for %s (status %d): %w", pmcid, status, err)
	}
	match := pmcOAPDFLinkRe.FindSubmatch(body)
	if match == nil {
		return "", nil
	}
	href := string(match[1])
	fixed := strings.Replace(href, "ftp://ftp.ncbi.nlm.nih.gov/pub/pmc/",
		"https://ftp.ncbi.nlm.nih.gov/pub/pmc/deprecated/", 1)
	if !strings.HasPrefix(fixed, "https://") {
		// A pdf link EXISTS but this rewrite could not reach it — that is a
		// failure to resolve, never "no pdf-format link exists".
		return "", fmt.Errorf("pmc oa.fcgi gave %s an unfetchable pdf href: %q", pmcid, href)
	}
	return fixed, nil
}

func WaybackRawURL(ctx context.Context, client *http.Client, source string) (string, error) {
	if client == nil {
		client = safeHTTPClientTimeout(false, 15*time.Second)
	}
	var data struct {
		Snapshots struct {
			Closest struct {
				Available bool   `json:"available"`
				Timestamp string `json:"timestamp"`
			} `json:"closest"`
		} `json:"archived_snapshots"`
	}
	// getJSONBody, not getBody: a JSON-decoding path refuses an over-ceiling
	// body by name rather than truncating it into a decode failure.
	if err := getJSONBody(
		ctx,
		client,
		"https://archive.org/wayback/available?url="+url.QueryEscape(source),
		defaultUA,
		nil,
		1<<20,
		&data,
	); err != nil {
		return "", err
	}
	if !data.Snapshots.Closest.Available || data.Snapshots.Closest.Timestamp == "" {
		return "", nil
	}
	return "https://web.archive.org/web/" + data.Snapshots.Closest.Timestamp + "id_/" + source, nil
}

// waybackStamp is the snapshot timestamp in a Wayback raw address
// (WaybackRawURL).
var waybackStamp = regexp.MustCompile(`/web/(\d{4})(\d{2})(\d{2})\d*id_/`)

// waybackReason is the partial reason of an artifact read from the Wayback
// Machine's snapshot at address snapshot: the archive's copy of its day,
// never the live page.
func waybackReason(snapshot string) string {
	day := "an undated day"
	if m := waybackStamp.FindStringSubmatch(snapshot); m != nil {
		day = m[1] + "-" + m[2] + "-" + m[3]
	}
	return "archived copy: the Wayback Machine's snapshot of " + day + ", not the live page"
}

// storeWaybackCopy stores the Wayback Machine's copy of source (result, read
// from snapshot) under source itself: its method the wayback rung, its
// partial naming the snapshot beside whatever the copy's own read left out.
func (h *Harvester) storeWaybackCopy(
	source, snapshot string,
	result Result,
	rungs []string,
	options FetchOptions,
) Result {
	content := storedContent(result)
	archived := withPartial(partialBody(content), joinReasons(waybackReason(snapshot), partialReason(content)))
	return h.storeResult(source, result.Kind, rungWayback, archived, result.Bytes, result.HTTPStatus,
		append([]string(nil), rungs...), options)
}
