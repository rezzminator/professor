package harvestmcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
)

// browserHardDeadline is the Go-side ceiling on one browser fetch. The
// 45000ms timeout travels to Python and bounds page.goto only — launch, IPC
// and close() answer to nobody else. Every other transport in this package has
// a hard Go ceiling; the browser gets one too.
const browserHardDeadline = 3 * time.Minute

// FetchBrowser renders one URL in system Chrome through the opt-in Patchright
// worker — the ladder's last wall-bypass rung. Go owns the SSRF decision:
// every URL Chrome touches is validated here through
// harvest.AssertFetchableStrict — strict, because Chrome re-resolves without a
// pinning hop — and a refusal is re-wrapped as harvest.ErrBrowserPolicyDenied so callers can
// tell POLICY from OUTAGE. Provisioning is lazy and only ever happens after
// fetch.browser gated this method; a missing environment is an outage,
// never a silent skip. The worker stamps an incomplete render with
// harvest.BrowserMarkerToken and reports the address the render landed on.
func (converter pythonConverter) FetchBrowser(
	ctx context.Context,
	source string,
) (string, int, string, error) {
	var html, finalURL string
	var status int
	headers, headersOrigin := harvest.CallerHeadersFor(ctx)
	err := converter.inBrowser(ctx, source, func(fetchCtx context.Context, session browserSession) error {
		var fetchErr error
		html, status, finalURL, fetchErr = session.worker.FetchPinned(
			fetchCtx,
			source,
			session.proxyURL,
			session.hostResolverRules,
			harvest.ProvenanceReferer,
			harvest.BrowserMarkerToken(),
			headers,
			headersOrigin,
			harvest.SitePressesLoaders(source),
			45000,
			session.onAsk,
		)
		return fetchErr
	})
	if err != nil {
		return "", 0, "", err
	}
	return html, status, finalURL, nil
}

// DownloadBrowser downloads one URL's bytes in system Chrome — the file
// policy's last rung — under the same SSRF boundary as FetchBrowser. The
// worker streams the file into dest, capped at maxBytes; a named worker
// failure becomes a harvest.BrowserDownloadError carrying its reason.
func (converter pythonConverter) DownloadBrowser(
	ctx context.Context,
	source, dest string,
	maxBytes int64,
) (harvest.BrowserFile, error) {
	var file harvest.BrowserFile
	headers, headersOrigin := harvest.CallerHeadersFor(ctx)
	err := converter.inBrowser(ctx, source, func(fetchCtx context.Context, session browserSession) error {
		got, downloadErr := session.worker.Download(fetchCtx, harvestpy.BrowserDownloadRequest{
			BrowserFetchRequest: harvestpy.BrowserFetchRequest{
				URL:               source,
				Proxy:             session.proxyURL,
				HostResolverRules: session.hostResolverRules,
				TimeoutMS:         45000,
				Referer:           harvest.ProvenanceReferer,
				Headers:           headers,
				HeadersOrigin:     headersOrigin,
			},
			Path:     dest,
			MaxBytes: maxBytes,
		}, session.onAsk)
		var failure *harvestpy.BrowserDownloadFailure
		if errors.As(downloadErr, &failure) {
			return &harvest.BrowserDownloadError{
				Reason: failure.Reason, Status: failure.Status, Head: failure.Head, Detail: failure.Message,
			}
		}
		file = harvest.BrowserFile{
			ContentType: got.ContentType, Bytes: got.Bytes, FinalURL: got.FinalURL, Status: got.Status,
		}
		return downloadErr
	})
	if err != nil {
		return harvest.BrowserFile{}, err
	}
	return file, nil
}

// browserSession is one browser worker behind its pinned proxy and resolver
// rule, with the SSRF ask handler every URL Chrome touches goes through.
type browserSession struct {
	worker            *harvestpy.BrowserWorker
	proxyURL          string
	hostResolverRules string
	onAsk             func(string) error
}

// inBrowser runs one worker op for source inside the browser rung's SSRF
// boundary (see FetchBrowser): the strict fetchable check on every ask, the
// Go-owned proxy, Chrome's resolver pinned, the hard Go deadline. A failure
// after the guard refused source itself is re-wrapped as
// harvest.ErrBrowserPolicyDenied: POLICY, not an outage.
func (converter pythonConverter) inBrowser(
	ctx context.Context,
	source string,
	op func(context.Context, browserSession) error,
) error {
	runtime, err := converter.browserRuntime(ctx)
	if err != nil {
		return err
	}
	browser := harvestpy.NewBrowserWorker(runtime)
	defer func() { _ = browser.Close() }()

	policyDenied := false
	onAsk := func(requestURL string) error {
		if askErr := harvest.AssertFetchableStrict(requestURL); askErr != nil {
			// Only a refusal of the INITIAL address labels the whole fetch
			// as POLICY. A denied tracker/subresource followed by an
			// unrelated failure must stay an outage — retrying can help.
			if requestURL == source {
				policyDenied = true
			}
			return askErr
		}
		return nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, browserHardDeadline)
	defer cancel()
	proxyURL, stopProxy, err := converter.browserProxy(fetchCtx)
	if err != nil {
		return err
	}
	defer stopProxy()
	// Pin Chrome to the address DoH resolved and the guard validated. Without
	// this the browser rung resolves the host a second time through the system
	// resolver — so on a network that rewrites DNS answers every HTTP rung
	// would reach the real host while the browser rung alone landed on a block
	// page, and the wall would look like the source's own.
	opErr := op(fetchCtx, browserSession{
		worker:            browser,
		proxyURL:          proxyURL,
		hostResolverRules: harvest.BrowserHostResolverRule(fetchCtx, source),
		onAsk:             onAsk,
	})
	if opErr != nil && policyDenied {
		return fmt.Errorf("%w: %v", harvest.ErrBrowserPolicyDenied, opErr)
	}
	return opErr
}

// browserProxy answers with the proxy Chrome is launched behind, and the
// function that tears it down (L2-F7). browser.py REFUSES to launch without
// one: Chrome resolves every host a second time, so the only way the address
// Go validated is the address Chrome dials is for Go to own the dial.
//
// A configured fetch.proxyURL wins, exactly as it does for every other rung
// (harvest.Options.ProxyURL routes all of them through the operator's proxy):
// there the operator's own egress proxy owns the dial, and substituting a
// second proxy underneath it would silently route around the deployment's
// only permitted way out. With no configured proxy this starts the Go-side
// pinned one, whose every outbound dial goes through the same pinned dialer
// the harvester's HTTP clients use.
func (converter pythonConverter) browserProxy(ctx context.Context) (string, func(), error) {
	if configured := strings.TrimSpace(converter.proxyURL); configured != "" {
		return configured, func() {}, nil
	}
	proxyURL, stop, err := harvest.StartBrowserProxy(ctx, harvest.ResolvePublicHost)
	if err != nil {
		return "", nil, fmt.Errorf("start the browser rung's pinned proxy: %w", err)
	}
	return proxyURL, stop, nil
}

// browserRuntime resolves the browser worker's runtime, provisioning it first
// when it is missing or was provisioned by an older pfm (harvestpy.EnsureBrowser).
func (converter pythonConverter) browserRuntime(ctx context.Context) (harvestpy.Runtime, error) {
	return harvestpy.EnsureBrowser(ctx, harvestpy.ProvisionOptions{
		Root:   converter.browserRoot,
		Runner: converter.runner,
	})
}
