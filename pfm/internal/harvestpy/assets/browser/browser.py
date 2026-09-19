"""Opt-in real-browser rung worker: Patchright + system Chrome.

Ported near-verbatim from the retired Python harvester's browser rung
(`git show fac3319:harvester/src/harvester/net.py` L593-672). The rung never
solves anything interactive (no Turnstile/CAPTCHA solver); it passes PASSIVE
bot walls by rendering in a real Chrome whose CDP automation handshake is
hidden by Patchright.

Protocol (JSON lines over stdin/stdout), one request serialized at a time:

  Go -> worker:   {"op":"fetch","url":"https://…","proxy":"http://127.0.0.1:PORT","headless":true,
                   "host_resolver_rules":"MAP host 1.2.3.4"|null,"timeout_ms":45000}
                  ("proxy" is REQUIRED — it is the Go-owned dial; see PROXY_REQUIRED)
                  {"op":"smoke"}
  worker -> Go:   zero or more guard asks before the final line:
                  {"ask":"fetchable","url":"https://…"}
  Go -> worker:   {"allow":true}
                  {"allow":false,"reason":"refusing private/internal host …"}
  worker -> Go:   exactly one final line:
                  {"ok":true,"html":"…","status":403,"headless":false}
                  {"ok":false,"error":"patchright not installed"}

SSRF: Go owns the fetchable decision (harvest.AssertFetchable) — the route
guards below ASK for every URL Chrome touches: navigation, every redirect,
every subresource, every XHR/fetch via context.route("**/*", …), and every
WebSocket connection via context.route_web_socket("**/*", …), since
context.route() alone never sees WebSocket traffic. Service workers are
blocked at context creation (service_workers="block") — a page's own SW
fetches would otherwise bypass the route interceptor entirely. Every path
aborts (or closes, for WebSocket) blocked targets BEFORE Chrome ever
connects.

The ask is only the POLICY half. The connection that follows used to be
Chrome's own: it re-resolved the host itself, a moment after Go validated an
address, so a TTL-0 rebind put the browser on 127.0.0.1 / 169.254.169.254
while the ask had seen a public IP — the check-vs-dial hole pinnedDialContext
closes for every Go client. The DIAL half therefore belongs to Go too: this
worker refuses to launch without the Go-side proxy (see PROXY_REQUIRED), and
launch_arguments() makes that proxy the only way out of the browser.
"""

import asyncio
import json
import os.path
import shutil
import sys
import urllib.parse


# Only binaries `channel="chrome"` can actually launch — patchright's chrome
# channel means GOOGLE Chrome; a chromium-only host must report MISSING here,
# not pass smoke and fail every launch.
CHROME_CANDIDATES = [
    "google-chrome",
    "google-chrome-stable",
    "/usr/bin/google-chrome",
    "/usr/bin/google-chrome-stable",
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
]


def chrome_binary():
    """Resolve a system Chrome without launching anything."""
    for candidate in CHROME_CANDIDATES:
        found = shutil.which(candidate) or (
            candidate if os.path.isfile(candidate) else None
        )
        if found:
            return found
    return None


# The refusal one missing proxy earns. Go validates every URL through the ask
# protocol, but Chrome dials what CHROME resolves; only a proxy Go owns makes
# the address Go validated the address Chrome connects to. Rendering a page
# without it would re-open the SSRF hole the guards exist to close, so the
# rung fails CLOSED and the ladder falls through to its other rungs.
PROXY_REQUIRED = (
    "browser rung requires the Go-side pinned proxy: without it Chrome resolves and dials "
    "every host itself, so a DNS rebind reaches an address the SSRF guard refused"
)

# Chrome's own resolver must answer NOTHING. Every connection then has to go
# through the proxy, which dials the address Go validated; DNS prefetch and
# preconnect cannot leak a lookup or open an unproxied socket either.
NO_LOCAL_DNS_RULE = "MAP * ~NOTFOUND"

# WebRTC negotiates UDP straight to an ICE candidate: it crosses neither the
# HTTP proxy nor the route guard, so a page could reach an internal address
# every other path refuses. The switch is Chrome's own mechanism; the init
# script below is the second layer, in case a build ignores it.
WEBRTC_POLICY_ARG = "--force-webrtc-ip-handling-policy=disable_non_proxied_udp"

WEBRTC_BLOCK_SCRIPT = """
for (const name of ['RTCPeerConnection', 'webkitRTCPeerConnection', 'RTCDataChannel']) {
  try {
    Object.defineProperty(window, name, {configurable: false, writable: false, value: undefined});
  } catch (error) {
    console.error('harvester browser rung could not disable ' + name + ': ' + error);
  }
}
"""


def proxy_settings(proxy_url):
    """Patchright's proxy option for one fetch, or None when there is nothing
    to proxy through — the caller decides what an absent proxy means.

    bypass="<-loopback>" REMOVES Chrome's implicit "never proxy localhost or
    link-local" rule. Those are precisely the addresses an SSRF is after, and
    a default build dials them directly, past the proxy Go owns."""
    if not proxy_url:
        return None
    return {"server": proxy_url, "bypass": "<-loopback>"}


def launch_arguments(proxy_url, host_resolver_rules=None):
    """Chrome's launch switches for one fetch. Pure, so the rung's SSRF
    posture is testable with no browser and no patchright."""
    if not proxy_url:
        raise ValueError(PROXY_REQUIRED)
    rules = [host_resolver_rules] if host_resolver_rules else []
    # The proxy's OWN host still has to resolve; NO_LOCAL_DNS_RULE would
    # otherwise strand the browser with no way to reach its only exit. An
    # EXCLUDE for an IP-literal proxy is a harmless no-op.
    proxy_host = urllib.parse.urlsplit(proxy_url).hostname
    if not proxy_host:
        # A bare "host:port" has no scheme for urlsplit to key on; Chrome
        # accepts that spelling, so read the host out of it directly.
        proxy_host = proxy_url.rsplit(":", 1)[0].strip("[]")
    if proxy_host:
        rules.append(f"EXCLUDE {proxy_host}")
    rules.append(NO_LOCAL_DNS_RULE)
    return [f"--host-resolver-rules={','.join(rules)}", WEBRTC_POLICY_ARG]


def browser_route_guard(ask_fetchable):
    """The per-request SSRF chokepoint for the browser rung, as a Playwright/Patchright
    route handler. Same contract as the retired net.py guard: EVERY url Chrome touches —
    redirects, subresources, XHR — is re-validated through the ask protocol, and blocked
    targets are aborted before Chrome ever connects. Extracted from fetch_browser so it
    is testable WITHOUT a browser under CI."""
    async def _ssrf_route_guard(route) -> None:
        target = str(route.request.url)
        try:
            allowed, reason = await ask_fetchable(target)
        except Exception as e:  # noqa: BLE001 — a broken ask channel must ABORT, never continue
            print(f"browser route guard RAISED for {redact(target)}: {e}", file=sys.stderr)
            await abort_request(route, redact(target))
            return
        if not allowed:
            print(f"browser route refused (ssrf) {redact(target)}: {reason}", file=sys.stderr)
            await abort_request(route, redact(target))
            return
        await route.continue_()

    return _ssrf_route_guard


def browser_websocket_guard(ask_fetchable):
    """The SSRF chokepoint for WebSocket connections, as a Patchright
    route_web_socket handler. context.route() does NOT intercept WebSocket
    traffic — a page's `new WebSocket(url)` reaches the network unless it is
    routed separately. Same fail-closed contract as browser_route_guard: the
    ask runs BEFORE the socket ever connects to the real server, and a denied
    or raising ask closes the routed (page-side) socket instead of forwarding
    it."""
    async def _ssrf_websocket_guard(ws) -> None:
        target = str(ws.url)
        try:
            allowed, reason = await ask_fetchable(target)
        except Exception as e:  # noqa: BLE001 — a broken ask channel must CLOSE, never connect
            print(f"browser websocket guard RAISED for {redact(target)}: {e}", file=sys.stderr)
            await ws.close(code=1011, reason="ssrf guard error")
            return
        if not allowed:
            print(f"browser websocket refused (ssrf) {redact(target)}: {reason}", file=sys.stderr)
            await ws.close(code=1008, reason=reason or "refused by ssrf guard")
            return
        # connect_to_server() is synchronous — it returns the server-side
        # WebSocketRoute and wires automatic message forwarding both ways.
        ws.connect_to_server()

    return _ssrf_websocket_guard


# service_workers="block" is a BrowserContext.new_context() option, not a
# route: a page's Service Worker can issue its own fetch()es that never pass
# through context.route() at all. Blocking SW registration outright is the
# only way the route guard sees every request the page causes.
CONTEXT_OPTIONS = {"service_workers": "block"}


async def install_route_guards(context, guarded_ask):
    """Register both SSRF chokepoints on *context*, at CONTEXT scope (every
    page the context opens, not just the first). Extracted from fetch_browser
    so a regression — page-scope registration, a narrower glob than "**/*",
    or a dropped registration entirely — fails a test instead of shipping
    silently."""
    await context.route("**/*", browser_route_guard(guarded_ask))
    await context.route_web_socket("**/*", browser_websocket_guard(guarded_ask))


# Route.abort validates its argument against the fixed CDP Network.ErrorReason
# enum. An invented code ("blocked", "guard-error") raises inside the route
# handler INSTEAD of aborting, which leaves the intercepted request's fate
# unresolved — on the refusal branch, the one path that must deterministically
# stop a request the SSRF guard rejected.
ABORT_REASON = "blockedbyclient"


async def abort_request(route, redacted_target):
    """Abort one intercepted request. Route.abort() itself failing (e.g. the
    request already finished) is reported rather than swallowed — but note
    that even then the request never proceeds: Chrome's paused (CDP
    Fetch-intercepted) request has no fallback path to the network, so a
    failed abort leaves it hanging until the caller's own timeout_ms, not
    silently let through. Fails closed either way."""
    try:
        await route.abort(ABORT_REASON)
    except Exception as e:  # noqa: BLE001 — a guard that cannot abort must SAY so
        print(f"browser route guard could not abort {redacted_target} (will hang until timeout, not proceed): {e}", file=sys.stderr)


def redact(url):
    """Strip query string and fragment — refused URLs are logged, and their
    query strings can carry session tokens or internal host details."""
    return url.split("?", 1)[0].split("#", 1)[0]


def serialized_ask(raw_ask):
    """Wrap an ask callable so only ONE ask exchange is ever in flight.

    Playwright dispatches concurrent route handlers in parallel (measured on
    patchright 1.62.1: two handlers both entered while one was still awaiting
    its reply). The ask protocol is a strictly ordered stdin/stdout exchange
    with no correlation id — two interleaved exchanges would let an ALLOW
    meant for one URL satisfy the handler for another. A lock makes the
    protocol safe under concurrency.
    """
    lock = asyncio.Lock()

    async def guarded_ask(target):
        async with lock:
            return await raw_ask(target)

    return guarded_ask


async def fetch_browser(url, ask_fetchable, proxy_url=None, timeout_ms=45_000, headless=True,
                        host_resolver_rules=None):
    """Render *url* in a real system Chrome via Patchright; return (html, status, headless, error).

    Opt-in rung — Go gates it behind fetch.browser because a browser launch is ~100ms+ and
    needs Chrome installed. It renders in exactly the mode Go asks for: headless unless Go
    is spending the one headed (visible-window) retry on a wall the headless render met.
    patchright is an OPTIONAL dependency; when absent this returns
    ("", None, False, "patchright not installed") and the ladder falls through, never raises.

    SSRF: the fetchable decision runs on the initial URL AND on EVERY request the page
    makes — a context.route() interceptor re-checks each navigation/redirect/subresource/
    XHR/fetch and aborts any that Go refuses (a public URL 302ing to 169.254.169.254 must
    die at the route layer; a one-shot pre-check alone would let Chrome walk straight past
    it), a context.route_web_socket() interceptor re-checks every WebSocket the same way
    before it connects, and service workers are blocked at context creation so a page
    cannot route around either interceptor via its own SW-originated fetches. The DIAL
    behind every one of those decisions belongs to Go as well: without *proxy_url* this
    returns PROXY_REQUIRED and launches nothing, because Chrome resolving a validated
    host a second time is the whole rebinding hole.
    """
    if not proxy_url:
        return "", None, headless, PROXY_REQUIRED
    try:
        from patchright.async_api import async_playwright  # type: ignore[import-not-found]
    except ImportError:
        return "", None, False, "patchright not installed"
    loop = asyncio.get_event_loop()

    async def ask(target):
        return await loop.run_in_executor(None, ask_fetchable, target)

    # One lock for BOTH the initial ask and every route-handler ask: a single
    # in-flight stdin/stdout exchange, no matter how handlers interleave.
    guarded_ask = serialized_ask(ask)

    allowed, reason = await guarded_ask(url)
    if not allowed:
        return "", None, False, reason or f"refused initial url {url}"

    proxy = proxy_settings(proxy_url)
    launch_args = launch_arguments(proxy_url, host_resolver_rules)

    async def _render(headless: bool):
        async with async_playwright() as p:  # type: ignore[attr-defined]
            browser = await p.chromium.launch(channel="chrome", headless=headless, proxy=proxy,
                                              args=launch_args)
            try:
                context = await browser.new_context(**CONTEXT_OPTIONS)
                await install_route_guards(context, guarded_ask)
                await context.add_init_script(WEBRTC_BLOCK_SCRIPT)

                page = await context.new_page()
                resp = await page.goto(url, timeout=timeout_ms, wait_until="domcontentloaded")
                try:
                    await page.wait_for_load_state("networkidle", timeout=15_000)
                except Exception as e:  # noqa: BLE001 — best-effort quiet-period wait
                    print(f"browser networkidle wait ended early for {redact(url)}: {e}", file=sys.stderr)
                html = await page.content()
                status = resp.status if resp else None
                print(f"browser rung {redact(url)} -> HTTP {status} ({len(html)} chars, headless={headless})",
                      file=sys.stderr)
                return html, status, headless
            finally:
                await browser.close()

    try:
        html, status, rendered_headless = await _render(headless=headless)
        return html, status, rendered_headless, None
    except Exception as e:  # noqa: BLE001 — the rung never raises past this boundary
        print(f"browser rung failed for {redact(url)} (headless={headless}): {e}", file=sys.stderr)
        return "", None, headless, str(e)


def smoke():
    """Report patchright importability and Chrome resolution WITHOUT launching a page."""
    try:
        from patchright.async_api import async_playwright  # noqa: F401

        patchright = True
        error = None
    except ImportError as e:
        patchright = False
        error = "patchright not installed"
    binary = chrome_binary()
    return {
        "ok": True,
        "patchright": patchright,
        "chrome_path": binary,
        "error": error,
    }


async def handle_fetch(request):
    html, status, headless, error = await fetch_browser(
        request.get("url", ""),
        lambda target: _blocking_ask(target),
        proxy_url=request.get("proxy"),
        timeout_ms=int(request.get("timeout_ms") or 45_000),
        headless=bool(request.get("headless", True)),
        host_resolver_rules=request.get("host_resolver_rules") or None,
    )
    if error is not None and not html:
        return {"ok": False, "error": error}
    return {"ok": True, "html": html, "status": status, "headless": headless}


def _blocking_ask(target):
    """Emit one guard ask and block on exactly one stdin reply line."""
    sys.stdout.write(json.dumps({"ask": "fetchable", "url": target}) + "\n")
    sys.stdout.flush()
    line = sys.stdin.readline()
    if not line:
        raise RuntimeError("go closed the ask channel mid-guard")
    reply = json.loads(line)
    return bool(reply.get("allow")), reply.get("reason")


def main():
    for raw in sys.stdin:
        raw = raw.strip()
        if not raw:
            continue
        try:
            request = json.loads(raw)
        except json.JSONDecodeError as e:
            print(json.dumps({"ok": False, "error": f"bad request JSON: {e}"}), flush=True)
            continue
        op = request.get("op")
        if op == "smoke":
            try:
                response = smoke()
            except Exception as e:  # noqa: BLE001 — a broken patchright install is an answer, not a crash
                response = {"ok": False, "error": f"{type(e).__name__}: {e}"}
            print(json.dumps(response), flush=True)
            continue
        if op != "fetch":
            print(json.dumps({"ok": False, "error": f"unknown op {op!r}"}), flush=True)
            continue
        try:
            response = asyncio.run(handle_fetch(request))
        except Exception as e:  # noqa: BLE001 — a crash discards only this response
            response = {"ok": False, "error": f"{type(e).__name__}: {e}"}
        sys.stdout.write(json.dumps(response) + "\n")
        sys.stdout.flush()


if __name__ == "__main__":
    main()
