package harvest

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

// A cloud share link names a viewer page, not the file: readPage and download
// fetch the service's direct form instead (shareDirectLink), and refuse by name
// the services whose shared files open only for a signed-in session or inside a
// scripted viewer (shareLinkRefusal). A rewritten fetch answered with a sign-in
// or interstitial page is a named failure, never stored as the document.

// shareLink is one rewritten share link: service is the rung slug (the rung is
// "<service>-download"), name the reader-facing service name, target the direct
// URL retrieval fetches in place of the share link.
type shareLink struct {
	service string
	name    string
	target  string
}

const shareGoogleDrive = "google-drive"

var (
	googleDriveFileIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,256}$`)
	boxSharedNamePattern     = regexp.MustCompile(`^[A-Za-z0-9]{8,128}$`)
	// driveConfirmForm is Drive's virus-scan warning ("Google Drive can't scan
	// this file for viruses"): a GET form whose hidden inputs (id, export,
	// confirm, uuid) are the download that skips the warning.
	driveConfirmForm = regexp.MustCompile(
		`(?is)<form[^>]*\bid="download-form"[^>]*\baction="([^"]+)"[^>]*>(.*?)</form>`,
	)
	hiddenInput = regexp.MustCompile(`(?i)<input[^>]*\btype="hidden"[^>]*\bname="([^"]+)"[^>]*\bvalue="([^"]*)"`)
)

// shareDirectLink rewrites a supported share link to its direct download.
// Google Docs, Sheets and Slides export to docx, xlsx and pptx rather than pdf:
// the OOXML exports keep headings, lists, tables, every sheet of a workbook and
// the slides' text as structure the converter reads, where a pdf export
// flattens them to page layout (and csv carries one sheet only).
func shareDirectLink(raw string) (shareLink, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return shareLink{}, false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case host == "drive.google.com":
		if target, ok := googleDriveDownloadURL(raw); ok {
			return shareLink{service: shareGoogleDrive, name: "Google Drive", target: target}, true
		}
	case host == "docs.google.com" && len(parts) >= 3 && parts[1] == "d" && googleDriveFileIDPattern.MatchString(parts[2]):
		target := url.URL{Scheme: schemeHTTPS, Host: host, Path: "/" + parts[0] + "/d/" + parts[2] + "/export"}
		switch parts[0] {
		case "document":
			target.RawQuery = "format=docx"
			return shareLink{service: "google-docs", name: "Google Docs", target: target.String()}, true
		case "spreadsheets":
			target.RawQuery = "format=xlsx"
			return shareLink{service: "google-sheets", name: "Google Sheets", target: target.String()}, true
		case "presentation":
			target.Path += "/pptx"
			return shareLink{service: "google-slides", name: "Google Slides", target: target.String()}, true
		}
	case host == "dropbox.com" || host == "www.dropbox.com":
		// /s/<id>/<name>, /scl/fi/<id>/<name>?rlkey=… (the rlkey is the link's
		// key and is kept); dl=1 is the file itself instead of the preview page.
		if len(parts) >= 2 && (parts[0] == "s" || (parts[0] == "scl" && parts[1] == "fi")) {
			target := *u
			target.Scheme, target.Host, target.Fragment = schemeHTTPS, "www.dropbox.com", ""
			query := target.Query()
			query.Set("dl", "1")
			target.RawQuery = query.Encode()
			return shareLink{service: "dropbox", name: "Dropbox", target: target.String()}, true
		}
	case host == "box.com" || strings.HasSuffix(host, ".box.com"):
		// <host>/s/<shared name> → <host>/shared/static/<shared name>, Box's
		// direct download of a public shared file.
		if len(parts) == 2 && parts[0] == "s" && boxSharedNamePattern.MatchString(parts[1]) {
			target := url.URL{Scheme: schemeHTTPS, Host: host, Path: "/shared/static/" + parts[1]}
			return shareLink{service: "box", name: "Box", target: target.String()}, true
		}
	}
	return shareLink{}, false
}

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

// refusedShareService names the service of a share link that opens only for a
// signed-in session or inside a scripted viewer: SharePoint and OneDrive for
// Business are tenant sign-ins; a personal OneDrive link resolves only in the
// scripted viewer (signed out, onedrive.live.com answers with Microsoft's
// marketing page); iCloud Drive and MEGA files are served by their web apps
// (MEGA decrypts in the browser); a WeTransfer file needs its app's
// transfer-and-security-hash exchange.
func refusedShareService(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	first := strings.ToLower(strings.SplitN(strings.Trim(u.Path, "/"), "/", 2)[0])
	switch {
	case strings.HasSuffix(host, "-my.sharepoint.com"):
		return "OneDrive for Business"
	case host == "sharepoint.com" || strings.HasSuffix(host, ".sharepoint.com"):
		return "SharePoint"
	case host == "1drv.ms" || host == "onedrive.live.com":
		return "OneDrive"
	case host == "share.icloud.com":
		return "iCloud"
	case host == "icloud.com" || host == "www.icloud.com":
		switch first {
		case "iclouddrive", "attachment", "pages", "numbers", "keynote":
			return "iCloud"
		}
	case host == "mega.nz" || strings.HasSuffix(host, ".mega.nz") || host == "mega.co.nz" || host == "www.mega.co.nz":
		return "MEGA"
	case host == "we.tl" || host == "wetransfer.com" || strings.HasSuffix(host, ".wetransfer.com"):
		return "WeTransfer"
	}
	return ""
}

// shareLinkRefusal is the named failure of a refused service's link, before
// any fetch.
func shareLinkRefusal(source string) (Result, bool) {
	service := refusedShareService(source)
	if service == "" {
		return Result{}, false
	}
	return Result{
		Source: source,
		Error: fmt.Sprintf(
			"This is a %s link: "+shareSignInText,
			service,
		),
		ErrorKind: errorKindLogin,
	}, true
}

// shareInterstitialKind is the error kind of a rewritten fetch answered with a
// page instead of the file: a sign-in or interstitial page below 400, the
// status's own kind otherwise.
func shareInterstitialKind(status int, kind string) string {
	if status < 400 {
		return errorKindLogin
	}
	return kind
}

// shareFetchFailure names a rewritten share link whose direct form did not
// deliver the file.
func shareFetchFailure(source string, link shareLink, status int, kind string, challenge bool, rungs []string) Result {
	what := fmt.Sprintf("Could not download the complete %s file", link.name)
	switch {
	case status >= 400:
		what += fmt.Sprintf(" (HTTP %d)", status)
	case kind == errorKindLogin:
		what += " — its direct link answered with a sign-in or interstitial page, not the file"
	}
	return Result{
		Source:     source,
		HTTPStatus: status,
		Error: what + " — " + shareSignInText + " Make the file available to anyone with the link. " +
			"Harvester will not substitute the service's preview or sign-in page for the file.",
		ErrorKind: kind,
		Challenge: challenge,
		Rungs:     rungs,
	}
}

// driveConfirmURL reads Drive's virus-scan warning page into the download it
// confirms; false when page is not that warning.
func driveConfirmURL(page []byte) (string, bool) {
	form := driveConfirmForm.FindSubmatch(page)
	if form == nil {
		return "", false
	}
	action, err := url.Parse(html.UnescapeString(string(form[1])))
	if err != nil || action.Scheme != schemeHTTPS ||
		!strings.EqualFold(action.Hostname(), "drive.usercontent.google.com") {
		return "", false
	}
	query := url.Values{}
	for _, input := range hiddenInput.FindAllSubmatch(form[2], -1) {
		query.Set(html.UnescapeString(string(input[1])), html.UnescapeString(string(input[2])))
	}
	if !googleDriveFileIDPattern.MatchString(query.Get("id")) || query.Get("confirm") == "" {
		return "", false
	}
	action.RawQuery = query.Encode()
	return action.String(), true
}

// fetchShareRung is readPage's fetchRung; for a share link (shared) a body that
// is Drive's virus-scan warning is followed once to the download it confirms.
func (h *Harvester) fetchShareRung(
	ctx context.Context,
	shared bool,
	client *http.Client,
	target, ua string,
	headers map[string]string,
	gaps *carriedGaps,
) ([]byte, int, string, error) {
	body, status, contentType, err := h.fetchRung(ctx, client, target, ua, headers, gaps)
	if !shared || err != nil || status >= 400 {
		return body, status, contentType, err
	}
	if confirmed, ok := driveConfirmURL(body); ok && confirmed != target {
		return h.fetchRung(ctx, client, confirmed, ua, headers, gaps)
	}
	return body, status, contentType, err
}

// driveConfirmTarget is download's follow of the virus-scan warning: after a
// Drive download refused a page for the file, the page is read once more and
// its confirmed download returned; false when link is not Drive or the page
// is not the warning. A probe that fails is logged and the download's own
// named failure stands.
func (h *Harvester) driveConfirmTarget(ctx context.Context, link shareLink) (string, bool) {
	if link.service != shareGoogleDrive {
		return "", false
	}
	resp, err := gatewayDo(ctx, gatewayRequest{url: link.target, client: h.binaryDirectOrClient(), ua: h.userAgent})
	if err != nil {
		obs.Logger(ctx).
			Warn("harvest: reading a Drive download's warning page", "url", safeURL(link.target), "error", err)
		return "", false
	}
	decoded, closeBody, err := decodedResponseBody(resp)
	defer func() {
		if closeErr := closeBody(); closeErr != nil {
			obs.Logger(ctx).
				Warn("harvest: closing a Drive warning page body", "url", safeURL(link.target), "error", closeErr)
		}
	}()
	if err != nil {
		obs.Logger(ctx).
			Warn("harvest: decoding a Drive download's warning page", "url", safeURL(link.target), "error", err)
		return "", false
	}
	page, err := io.ReadAll(io.LimitReader(decoded, 1<<20))
	if err != nil {
		obs.Logger(ctx).
			Warn("harvest: reading a Drive download's warning page", "url", safeURL(link.target), "error", err)
		return "", false
	}
	return driveConfirmURL(page)
}

// shareSignInText ends every share-link failure; the texts carry no URL (the
// result's source names it), so the public surface may repeat them verbatim.
const shareSignInText = "a share link this harvester cannot open without signing in — download it yourself and use parseLocalDocuments."
