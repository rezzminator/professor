package harvestmcp

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/harvestpy"
)

// browserHardDeadline is the Go-side ceiling on one browser fetch. The
// 45000ms timeout travels to Python and bounds page.goto only — launch, IPC,
// the headed→headless retry, and close() answer to nobody else. Every other
// transport in this package has a hard Go ceiling; the browser gets one too.
const browserHardDeadline = 3 * time.Minute

// FetchBrowser renders one URL in system Chrome through the opt-in Patchright
// worker — the ladder's last wall-bypass rung. Go owns the SSRF decision:
// every URL Chrome touches is validated here through
// harvest.AssertFetchableStrict — strict, because Chrome re-resolves without a
// pinning hop — and a refusal is re-wrapped as harvest.ErrBrowserPolicyDenied so callers can
// tell POLICY from OUTAGE. Provisioning is lazy and only ever happens after
// fetch.browser gated this method; a missing environment is an outage,
// never a silent skip.
func (converter pythonConverter) FetchBrowser(ctx context.Context, source string, headless bool) (string, int, error) {
	runtime, err := converter.browserRuntime(ctx)
	if err != nil {
		return "", 0, err
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
		return "", 0, err
	}
	defer stopProxy()
	// Pin Chrome to the address DoH resolved and the guard validated. Without
	// this the browser rung resolves the host a second time through the system
	// resolver — so on a network that rewrites DNS answers every HTTP rung
	// would reach the real host while the browser rung alone landed on a block
	// page, and the wall would look like the source's own.
	hostResolverRules := harvest.BrowserHostResolverRule(fetchCtx, source)
	html, status, fetchErr := browser.FetchPinned(
		fetchCtx,
		source,
		proxyURL,
		hostResolverRules,
		harvest.ProvenanceReferer,
		headless,
		harvest.SitePressesLoaders(source),
		45000,
		onAsk,
	)
	if fetchErr != nil && policyDenied {
		return "", 0, fmt.Errorf("%w: %v", harvest.ErrBrowserPolicyDenied, fetchErr)
	}
	return html, status, fetchErr
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
