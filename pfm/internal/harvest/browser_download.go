package harvest

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// rungBrowserDownload is the file policy's last rung: a real browser that
// downloads the file (its download event, or the navigation's own response
// body), headless first; rungBrowserDownloadHeaded is the visible-window retry
// spent only on a challenge the headless browser did not pass.
const (
	rungBrowserDownload       = "browser-download"
	rungBrowserDownloadHeaded = "browser-download-headed"
)

// BrowserDownloader is implemented by adapters that can download one URL's
// bytes in a real browser (PolicyFile's last rung). The adapter writes the
// file to dest, a path in the binary cache's directory Go named, holding at
// most maxBytes; Go then moves it into the cache through atomicfile. A failure
// is a *BrowserDownloadError naming its reason, or any other error for a rung
// that could not run (an outage).
type BrowserDownloader interface {
	DownloadBrowser(ctx context.Context, source, dest string, maxBytes int64, headless bool) (BrowserFile, error)
}

// BrowserFile is what a browser download wrote to dest.
type BrowserFile struct {
	ContentType string // "" when the browser did not report one
	Bytes       int64
	FinalURL    string // the address the file came from, after every redirect
	Status      int    // the navigation's HTTP status; 0 when unknown
}

// The named reasons a browser download ends without a file.
const (
	// BrowserDownloadNoDownload: the navigation showed a page and no download
	// started. Head and Status tell Go whether that page is a challenge.
	BrowserDownloadNoDownload = "no-download"
	// BrowserDownloadTooLarge: the file ran past maxBytes.
	BrowserDownloadTooLarge = "too-large"
	// BrowserDownloadTimeout: nothing arrived within the worker's timeout.
	BrowserDownloadTimeout = "timeout"
)

// BrowserDownloadError is a browser download that ran and ended without the
// file, for a named Reason.
type BrowserDownloadError struct {
	Reason string
	Status int
	Head   string // the first characters of the page shown instead of a download
	Detail string
}

func (e *BrowserDownloadError) Error() string {
	message := fmt.Sprintf("browser download ended without a file (%s", e.Reason)
	if e.Status != 0 {
		message += fmt.Sprintf(", HTTP %d", e.Status)
	}
	if e.Detail != "" {
		message += ": " + e.Detail
	}
	return message + ")"
}

// browserFileRung is PolicyFile's last rung: headless first, the headed retry
// only on a challenge the headless browser did not pass (renderHeadlessFirst's
// policy, for a download). It runs only for the file policy — the inline-image
// policy never starts a browser — with the browser rung on and an adapter
// wired. kept reports a file stored in out.Result; err is the rung's named
// failure, nil when the rung did not run.
func (h *Harvester) browserFileRung(
	ctx context.Context,
	req retrieveRequest,
	limit int64,
	out *Retrieved,
) (bool, error) {
	if req.policy != PolicyFile || !h.settings.browser || isPrivateURL(req.target) {
		return false, nil
	}
	downloader, ok := h.options.Converter.(BrowserDownloader)
	if !ok {
		obs.Logger(ctx).Warn("harvest: no BrowserDownloader adapter is wired; the browser download rung did not run",
			"target", logSource(req.target))
		return false, nil
	}
	kept, err := h.browserFileAttempt(ctx, downloader, req, limit, rungBrowserDownload, true, out)
	if kept || !errors.Is(err, errBrowserChallenge) {
		return kept, err
	}
	headedKept, headedErr := h.browserFileAttempt(ctx, downloader, req, limit, rungBrowserDownloadHeaded, false, out)
	var named *BrowserDownloadError
	if headedErr != nil && !errors.As(headedErr, &named) && !errors.Is(headedErr, errBrowserChallenge) &&
		!errors.Is(headedErr, errDownloadTooLarge) {
		// A headed launch that could not run (a display-less host) leaves the
		// headless verdict standing rather than masking it.
		obs.Logger(ctx).Warn("harvest: the headed browser download could not run after a headless challenge",
			"target", logSource(req.target), obs.FieldErr, headedErr.Error())
		return false, err
	}
	return headedKept, headedErr
}

// errBrowserChallenge is a browser that met a challenge and did not pass it.
var errBrowserChallenge = errors.New("met a challenge the browser did not pass")

// browserFileAttempt is one browser download: the worker writes into a scratch
// file beside the cache, and the file moves into the binary cache through
// atomicfile, streamed and capped. The scratch never survives the attempt.
func (h *Harvester) browserFileAttempt(
	ctx context.Context,
	downloader BrowserDownloader,
	req retrieveRequest,
	limit int64,
	rung string,
	headless bool,
	out *Retrieved,
) (bool, error) {
	out.Rungs = append(out.Rungs, rung)
	// The removal below logs its failure, so WriteScratch's silent cleanup is unused.
	dest, _, err := atomicfile.WriteScratch(h.options.CacheDir, ".browser-download-*", nil)
	if err != nil {
		return false, fmt.Errorf("%s: %w", rung, err)
	}
	defer func() {
		if removeErr := os.Remove(dest); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			obs.Logger(ctx).
				Warn("harvest: removing a browser download scratch file", "path", dest, obs.FieldErr, removeErr.Error())
		}
	}()
	file, err := downloader.DownloadBrowser(ctx, req.target, dest, limit, headless)
	if err != nil {
		return false, browserDownloadFailure(rung, limit, err)
	}
	return h.keepBrowserFile(ctx, req, limit, rung, dest, file, out)
}

// keepBrowserFile moves a finished browser download from dest into the binary
// cache, classifying it by its first bytes as every file rung does.
func (h *Harvester) keepBrowserFile(
	ctx context.Context,
	req retrieveRequest,
	limit int64,
	rung, dest string,
	file BrowserFile,
	out *Retrieved,
) (bool, error) {
	source, err := os.Open(dest)
	if err != nil {
		return false, fmt.Errorf("%s: open the downloaded file: %w", rung, err)
	}
	defer func() {
		if closeErr := source.Close(); closeErr != nil {
			obs.Logger(ctx).Warn("harvest: closing a browser download", "path", dest, obs.FieldErr, closeErr.Error())
		}
	}()
	body := bufio.NewReaderSize(source, 512)
	head, err := body.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return false, fmt.Errorf("%s: read the downloaded file: %w", rung, err)
	}
	kind := classifyKind(req.target, file.ContentType, head)
	if req.accept != nil && !req.accept(kind) {
		return false, fmt.Errorf("%s: the browser downloaded a %s, which this caller does not keep", rung, kind)
	}
	if (kind == kindHTML || kind == kindTXT) && isChallenge(head, file.Status) {
		return false, fmt.Errorf("%s: %w (HTTP %d)", rung, errBrowserChallenge, file.Status)
	}
	path := h.binaryPath(req.target, kind)
	written, err := atomicfile.WriteFrom(path, body, 0o600, limit)
	if errors.Is(err, atomicfile.ErrTooLarge) {
		return false, fmt.Errorf("%s: %w: the body ran past the %d-byte cap (harvest.maxDownloadBytes)",
			rung, errDownloadTooLarge, limit)
	}
	if err != nil {
		return false, fmt.Errorf("%s: cache file: %w", rung, err)
	}
	cache := cacheStatusMiss
	if req.options.Refresh {
		cache = cacheStatusRefresh
	}
	out.Status = file.Status
	out.FinalURL = file.FinalURL
	out.ContentType = file.ContentType
	out.Result = Result{
		Source: req.target, Kind: kind, Path: path, CacheStatus: cache, Bytes: written,
		Method: rung, Rungs: append([]string(nil), out.Rungs...),
	}
	return true, nil
}

// browserDownloadFailure names a browser download's failure: a page shown
// instead of a download is a challenge when it reads as one; an overrun is the
// download cap's own terminal failure; anything else is an outage.
func browserDownloadFailure(rung string, limit int64, err error) error {
	var named *BrowserDownloadError
	if !errors.As(err, &named) {
		if errors.Is(err, ErrBrowserPolicyDenied) {
			return fmt.Errorf("%s: %w", rung, err)
		}
		return fmt.Errorf("%s could not run: %w", rung, err)
	}
	switch named.Reason {
	case BrowserDownloadTooLarge:
		return fmt.Errorf("%s: %w: the download ran past the %d-byte cap (harvest.maxDownloadBytes)",
			rung, errDownloadTooLarge, limit)
	case BrowserDownloadTimeout:
		return fmt.Errorf("%s: the download timed out (%s)", rung, named.Detail)
	case BrowserDownloadNoDownload:
		if isChallenge([]byte(named.Head), named.Status) {
			return fmt.Errorf("%s: %w (HTTP %d)", rung, errBrowserChallenge, named.Status)
		}
		return fmt.Errorf("%s: the browser opened a page (HTTP %d) and no download started", rung, named.Status)
	}
	return fmt.Errorf("%s: %w", rung, err)
}
