"""Opt-in real-browser rung worker: Patchright + system Chrome.

Ported near-verbatim from the retired Python harvester's browser rung
(`git show fac3319:harvester/src/harvester/net.py` L593-672). The rung never
solves anything interactive (no Turnstile/CAPTCHA solver); it passes PASSIVE
bot walls by rendering in a real Chrome whose CDP automation handshake is
hidden by Patchright.

Every render dismisses a consent banner before it scrolls — the reject or
necessary-only choice, never accept, in every frame; an overlay with no such
choice is removed and its scroll lock undone — and a page whose scrolling
stays locked is stamped incomplete ("blocked"), never judged stable. The
snapshot handed to Go carries no consent banner (see CONSENT_SETTLE_MS).

Protocol (JSON lines over stdin/stdout), one request serialized at a time:

  Go -> worker:   {"op":"fetch","url":"https://…","proxy":"http://127.0.0.1:PORT","headless":true,
                   "host_resolver_rules":"MAP host 1.2.3.4"|null,"timeout_ms":45000,
                   "referer":"https://www.google.com/"|null,"press_loaders":true,"marker_token":"…"}
                  ("proxy" is REQUIRED — it is the Go-owned dial; see PROXY_REQUIRED;
                   "press_loaders" absent = read-only scrolling, no button pressed;
                   "marker_token" is carried by the lazy-load marker — see mark_incomplete)
                  {"op":"smoke"}
  worker -> Go:   zero or more guard asks before the final line:
                  {"ask":"fetchable","url":"https://…"}
  Go -> worker:   {"allow":true}
                  {"allow":false,"reason":"refusing private/internal host …"}
  worker -> Go:   exactly one final line:
                  {"ok":true,"html":"…","status":403,"headless":false,"final_url":"https://…"}
                  ("final_url" is the address of the document "html" holds, after every redirect)
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
from html import escape as html_escape
import os.path
import re
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

# The platform token a stock desktop Chrome puts in its (reduced) User-Agent.
# Chrome freezes the Linux token to x86_64 on every architecture.
UA_PLATFORM_TOKENS = (
    ("linux", "X11; Linux x86_64"),
    ("darwin", "Macintosh; Intel Mac OS X 10_15_7"),
    ("win", "Windows NT 10.0; Win64; x64"),
)


def stock_user_agent(browser_version, platform=None):
    """The User-Agent a stock desktop Chrome of *browser_version* sends.

    Headless Chrome announces itself as `HeadlessChrome/<v>` — a token forum
    anti-bot walls refuse outright, so the headless render would meet the wall
    the headed one does not. The rung sends the reduced UA a visible Chrome of
    the SAME major version sends, so the UA never contradicts the engine that
    renders. An unreadable version raises: guessing one would ship a UA that
    disagrees with the browser's own client hints."""
    major = str(browser_version or "").split(".", 1)[0]
    if not major.isdigit():
        raise ValueError(f"unreadable Chrome version {browser_version!r}")
    platform = platform or sys.platform
    token = next((value for prefix, value in UA_PLATFORM_TOKENS if platform.startswith(prefix)),
                 UA_PLATFORM_TOKENS[0][1])
    return f"Mozilla/5.0 ({token}) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/{major}.0.0.0 Safari/537.36"


def context_options(user_agent=None):
    """new_context() options for one fetch: the SSRF posture of CONTEXT_OPTIONS,
    plus the stock User-Agent when one is given (the headless render). None
    keeps Chrome's own UA — the headed render's, which is already stock."""
    if user_agent is None:
        return dict(CONTEXT_OPTIONS)
    return {**CONTEXT_OPTIONS, "user_agent": user_agent}


# The engine's own client-hint metadata, read in the headless render's engine
# so the UA override's hints are that engine's, never synthesised. about:blank
# is not a secure context, so it exposes no navigator.userAgentData (measured on
# Chrome 153); ENGINE_INFO_URL is a secure page the engine serves itself, so
# opening it makes no network request.
ENGINE_INFO_URL = "chrome://version"
UA_DATA_JS = """async (hints) => {
  const data = navigator.userAgentData;
  if (!data) return null;
  const high = await data.getHighEntropyValues(hints);
  return {brands: data.brands, mobile: data.mobile, platform: data.platform, ...high};
}"""
UA_HIGH_ENTROPY_HINTS = ["architecture", "bitness", "formFactors", "fullVersionList", "model", "platformVersion",
                        "uaFullVersion", "wow64"]
HEADLESS_BRAND = "HeadlessChrome"
STOCK_BRAND = "Google Chrome"


def ua_metadata(data):
    """CDP userAgentMetadata from navigator.userAgentData as the engine
    reported it, every HeadlessChrome brand renamed Google Chrome (brands and
    full version list) and everything else kept. No brand list raises."""
    if not isinstance(data, dict) or not data.get("brands"):
        raise ValueError(f"the engine's navigator.userAgentData is unreadable (no brand list): {data!r}")

    def renamed(entries):
        return [{**entry, "brand": STOCK_BRAND if entry.get("brand") == HEADLESS_BRAND else entry.get("brand")}
                for entry in entries]

    metadata = dict(data)
    metadata["brands"] = renamed(data["brands"])
    if data.get("fullVersionList"):
        metadata["fullVersionList"] = renamed(data["fullVersionList"])
    return metadata


async def engine_ua_metadata(browser, guarded_ask):
    """Read the running engine's client-hint metadata from ENGINE_INFO_URL —
    a page the engine serves itself, never the web — in its own guarded
    context closed before the render context opens."""
    context = await browser.new_context(**context_options(None))
    try:
        await install_route_guards(context, guarded_ask)
        page = await context.new_page()
        await page.goto(ENGINE_INFO_URL)
        return ua_metadata(await page.evaluate(UA_DATA_JS, UA_HIGH_ENTROPY_HINTS))
    finally:
        await context.close()


# Scroll-until-stable: a lazy-loaded page (an infinite feed, a comment thread,
# a gallery) holds only its first screen after load; the rest arrives as the
# reader scrolls, or as the reader presses its "load more" control. Each round
# scrolls to the bottom, presses the visible load-more buttons (EXPAND_JS) —
# only when Go asks for a registered site ("press_loaders"; pressing can fire
# requests or navigation, so every other page is scrolled read-only) — and
# waits for the page to settle; the loop stops once the rendered text
# stopped growing for SCROLL_STABLE_ROUNDS rounds — or at a round or time cap,
# so a page that never stops growing cannot hold the rung.
SCROLL_MAX_ROUNDS = 60
SCROLL_MAX_SECONDS = 60.0
SCROLL_SETTLE_MS = 1500
SCROLL_STABLE_ROUNDS = 3

# A hash route (#/… or #!…) of an app shell is written by the app's router
# after the page loads, often after the network went idle: before scrolling,
# the render waits until the page's text left the shell's and held still for
# ROUTE_QUIET_ROUNDS pauses of ROUTE_PAUSE_MS, or ROUTE_SETTLE_MAX_SECONDS. A
# page whose text at load is longer than ROUTE_SHELL_TEXT_MAX is no empty
# shell: its fragment is an anchor, and it is not waited on.
ROUTE_SETTLE_MAX_SECONDS = 20.0
ROUTE_PAUSE_MS = 250
ROUTE_QUIET_ROUNDS = 4
ROUTE_SHELL_TEXT_MAX = 200
TEXT_JS = "() => (document.body ? document.body.innerText : '')"

# The rendered-content size the loop watches: the length of the visible text.
MEASURE_JS = "() => (document.body ? document.body.innerText.length : 0)"
# One scroll round: the window to the bottom of the document, and the page's
# largest inner scroll container (an app whose content scrolls inside a div,
# not the window) to its bottom. It reports whether anything moved, and
# "blocked" when the document still had room below the viewport yet nothing
# moved and no inner container carries the content — a scroll lock (a consent
# or modal overlay pinning the body), never a finished page. The height is
# the body's too: a lock that fixes the body leaves the document one viewport
# tall while the content overflows the body.
SCROLL_JS = r"""() => {
  const doc = document.documentElement, body = document.body;
  const height = Math.max(doc.scrollHeight, body ? body.scrollHeight : 0);
  const before = window.scrollY;
  const room = height - window.innerHeight - before > 2;
  window.scrollTo(0, height);
  let moved = window.scrollY !== before;
  let best = null, bestArea = 0;
  for (const el of document.querySelectorAll('body *')) {
    if (el.scrollHeight - el.clientHeight <= 2) continue;
    const overflow = getComputedStyle(el).overflowY;
    if (overflow !== 'auto' && overflow !== 'scroll' && overflow !== 'overlay') continue;
    const area = el.clientWidth * el.clientHeight;
    if (area > bestArea) { best = el; bestArea = area; }
  }
  if (best && bestArea >= window.innerWidth * window.innerHeight * 0.25) {
    const top = best.scrollTop;
    best.scrollTop = best.scrollHeight;
    if (best.scrollTop !== top) moved = true;
  } else {
    best = null;
  }
  return {moved: moved, blocked: room && !moved && !best};
}"""

# Consent: a consent-management overlay (OneTrust, Didomi, Quantcast/TCF,
# Cookiebot, Usercentrics, TrustArc, Sourcepoint in its iframe, a site's own
# cookie dialog) pins the body so the page never scrolls. render_page
# dismisses it before scrolling, on every page (it is not a loader press), in
# the main frame and every child frame: it presses the privacy-preserving
# choice only — reject, decline, necessary-only, never accept — first by the
# known CMP selectors, then by label inside a dialog or consent container (any
# control in a child frame: a consent iframe is its own container). A control
# that could navigate away (a link with an address, anything in a form) is
# never pressed. An overlay with no reject control, or one that stays after
# the press, is removed and the page's scroll lock undone; a lock that returns
# stops the scroll "blocked", stamped incomplete. The known CMP containers are
# removed before every snapshot, so a banner's text is never stored as content.
CONSENT_SETTLE_MS = 1000
CONSENT_REJECT_PATTERN = (
    r"^(?:(?:i\s+)?(?:reject|decline|deny|refuse|disagree)(?:\s+all)?(?:\s+cookies)?|(?:i\s+)?do\s+not\s+accept|"
    r"don'?t\s+accept|continue\s+without\s+(?:accepting|agreeing)|"
    r"(?:(?:use|accept|allow)\s+)?(?:only\s+)?(?:strictly\s+)?(?:necessary|essential|required)(?:\s+cookies)?(?:\s+only)?|"
    r"(?:alle\s+)?ablehnen|nur\s+(?:notwendige|erforderliche|essenzielle)(?:\s+cookies)?|"
    r"(?:tout\s+)?refuser(?:\s+tout)?|continuer\s+sans\s+accepter|rechazar(?:\s+tod[oa]s?)?|rifiuta(?:\s+tutt[oi])?|"
    r"(?:alles\s+)?weigeren|rejeitar(?:\s+tudo)?|avvisa\s+alla|afvis\s+alle|avslå\s+alle)$"
)
CONSENT_REJECT_SELECTORS = [
    "#onetrust-reject-all-handler", "#didomi-notice-disagree-button", ".didomi-continue-without-agreeing",
    "#CybotCookiebotDialogBodyButtonDecline", "#CybotCookiebotDialogBodyLevelButtonLevelOptinDeclineAll",
    "button[data-testid='uc-deny-all-button']", "#truste-consent-required", "button.sp_choice_type_REJECT_ALL",
    "[data-cookiefirst-action='reject']", ".cmpboxbtnno", "#cookiescript_reject", ".cky-btn-reject",
    "#tarteaucitronAllDenied2",
]
CONSENT_CONTAINER = (
    "[role=dialog],[role=alertdialog],dialog,[aria-modal=true],[id*=consent i],[class*=consent i],[id*=cookie i],"
    "[class*=cookie i],[id*=cmp i],[class*=cmp i],[id*=gdpr i],[class*=gdpr i],[id*=privacy i],[class*=privacy i],"
    "[id^=sp_message],[id*=onetrust i],[id*=didomi i],[id*=usercentrics i],[id*=truste i]"
)
# The known CMP containers removed before every snapshot and on an unblock.
CONSENT_CONTAINERS = [
    "#onetrust-consent-sdk", "#onetrust-banner-sdk", "#didomi-host", "#didomi-popup", ".qc-cmp2-container",
    "#qc-cmp2-container", "#CybotCookiebotDialog", "#CybotCookiebotDialogBodyUnderlay", "#usercentrics-root",
    "#usercentrics-cmp-ui", "#truste-consent-track", ".truste_box_overlay", ".truste_overlay",
    "[id^='sp_message_container']", "#cmpbox", "#cmpbox2", ".cky-consent-container", "#cookiescript_injected",
    "#tarteaucitronRoot",
]

# Presses one reject control in this frame; returns {"pressed": [labels]}.
CONSENT_DISMISS_JS = r"""(args) => {
  const harvesterConsent = true;
  const reject = new RegExp(args.pattern, 'i');
  const roots = [document];
  const collect = (root) => { for (const el of root.querySelectorAll('*')) if (el.shadowRoot) { roots.push(el.shadowRoot); collect(el.shadowRoot); } };
  collect(document);
  const label = (el) => (el.innerText || el.value || el.getAttribute('aria-label') || el.title || '').replace(/\s+/g, ' ').trim();
  const visible = (el) => { const box = el.getBoundingClientRect(); return box.width > 0 && box.height > 0; };
  const navigates = (el) => {
    if (el.closest('form')) return true;
    const link = el.closest('a[href]');
    return !!link && !/^(#|javascript:)/i.test(link.getAttribute('href'));
  };
  const inContainer = (el) => {
    for (let node = el; node; node = node.parentElement || (node.getRootNode && node.getRootNode().host) || null) {
      if (node.matches && node.matches(args.container)) return true;
    }
    return false;
  };
  const pressable = (el) => !el.disabled && visible(el) && !navigates(el);
  for (const root of roots) for (const selector of args.selectors) for (const el of root.querySelectorAll(selector)) {
    if (pressable(el)) { const name = label(el) || selector; el.click(); return {pressed: [name], removed: []}; }
  }
  for (const root of roots) for (const el of root.querySelectorAll('button, [role=button], input[type=button]')) {
    if (!pressable(el) || !reject.test(label(el))) continue;
    if (args.main && !inContainer(el)) continue;
    const name = label(el);
    el.click();
    return {pressed: [name], removed: []};
  }
  return {pressed: [], removed: []};
}"""

# Removes consent overlays from the main frame — the known CMP containers and
# any fixed or sticky element carrying consent markers (in its own names, or,
# when it covers most of the viewport, anywhere inside) — and, when it removed
# one or when *force* (a scroll found the page blocked), undoes the scroll lock:
# overflow back to auto on html and body, a fixed body released, scroll-lock
# classes dropped. Returns {"removed": [names]}.
CONSENT_CLEAR_JS = r"""(args) => {
  const harvesterConsent = true;
  const markers = /consent|cookie|\bcmp\b|cmp-|gdpr|onetrust|didomi|sp_message|usercentrics|cybot|truste/i;
  const removed = [];
  const name = (el) => el.id || (el.tagName.toLowerCase() + (typeof el.className === 'string' && el.className ? '.' + el.className.trim().split(/\s+/)[0] : ''));
  const remove = (el) => { if (el.isConnected) { removed.push(name(el)); el.remove(); } };
  for (const selector of args.selectors) for (const el of document.querySelectorAll(selector)) remove(el);
  const bodyText = document.body ? document.body.innerText.length : 0;
  const viewport = window.innerWidth * window.innerHeight;
  for (const el of document.querySelectorAll('body *')) {
    if (!el.isConnected) continue;
    const style = getComputedStyle(el);
    if (style.position !== 'fixed' && style.position !== 'sticky') continue;
    const own = markers.test(el.id + ' ' + (typeof el.className === 'string' ? el.className : '') + ' ' + (el.getAttribute('aria-label') || ''));
    const box = el.getBoundingClientRect();
    const covers = box.width * box.height >= viewport * 0.5;
    const inside = covers && (el.querySelector(args.container) !== null || /cookie|consent/i.test(el.innerText || ''));
    if (!own && !inside) continue;
    if (bodyText > 2000 && (el.innerText || '').length > bodyText * 0.5) continue;
    remove(el);
  }
  if (removed.length || args.force) {
    for (const el of [document.documentElement, document.body]) {
      if (!el) continue;
      el.style.setProperty('overflow', 'auto', 'important');
      el.style.setProperty('overflow-y', 'auto', 'important');
      if (getComputedStyle(el).position === 'fixed') {
        el.style.setProperty('position', 'static', 'important');
        el.style.setProperty('top', 'auto', 'important');
      }
      if (el.style.height === '100%' || getComputedStyle(el).height === window.innerHeight + 'px') el.style.setProperty('height', 'auto', 'important');
      for (const cls of Array.from(el.classList)) {
        if (/no-?scroll|scroll-?lock|overflow-?hidden|modal-open|disable-?scroll|consent|cookie|cmp|didomi|sp-message|onetrust/i.test(cls)) el.classList.remove(cls);
      }
    }
  }
  return {pressed: [], removed: removed};
}"""


def is_reject_label(label):
    """Whether a control's visible label is a privacy-preserving consent
    choice (reject, decline, necessary-only) — the only kind the rung presses.
    The same pattern runs in the page (CONSENT_DISMISS_JS)."""
    return bool(re.match(CONSENT_REJECT_PATTERN, " ".join(str(label or "").split()), re.IGNORECASE))


async def dismiss_consent(page):
    """Press one reject control in every frame (the main frame first, as
    patchright lists it), wait for the overlay to go, then remove what stayed.
    Returns {"pressed": [labels], "removed": [names]}; a frame that cannot be
    read (detached, cross-origin failure) is logged and skipped."""
    pressed = []
    for index, frame in enumerate(list(page.frames)):
        args = {"pattern": CONSENT_REJECT_PATTERN, "selectors": CONSENT_REJECT_SELECTORS,
                "container": CONSENT_CONTAINER, "main": index == 0}
        try:
            result = await frame.evaluate(CONSENT_DISMISS_JS, args)
        except Exception as e:  # noqa: BLE001 — one unreadable frame never stops the others
            print(f"browser consent check skipped a frame ({redact(str(getattr(frame, 'url', '')))}): {e}",
                  file=sys.stderr)
            continue
        pressed += (result or {}).get("pressed") or []
    if pressed:
        await page.wait_for_timeout(CONSENT_SETTLE_MS)
    return {"pressed": pressed, "removed": await clear_consent(page, force=False)}


async def clear_consent(page, force):
    """Remove the main frame's consent overlays and, when one was removed or
    *force*, undo its scroll lock (CONSENT_CLEAR_JS); returns the removed
    names."""
    result = await page.evaluate(CONSENT_CLEAR_JS, {"selectors": CONSENT_CONTAINERS, "container": CONSENT_CONTAINER,
                                                    "force": force})
    return (result or {}).get("removed") or []


async def snapshot(page):
    """page.content() with the consent overlays removed first, so a banner is
    never stored as content. A failed removal is logged and the snapshot still
    taken; page.content() raising propagates to the caller's navigation
    handling."""
    try:
        removed = await clear_consent(page, force=False)
    except Exception as e:  # noqa: BLE001 — the snapshot is still taken; the caller decides on its failure
        print(f"browser consent removal before the snapshot failed for {redact(page.url)}: {e}", file=sys.stderr)
    else:
        if removed:
            print(f"browser snapshot {redact(page.url)}: removed consent overlays {removed}", file=sys.stderr)
    return await page.content()

# The load-more controls a round presses: a visible, enabled <button> (never a
# link, never a form submit — neither may navigate away) whose whole label is a
# load-more phrase: "View more comments", "Load more", "Show 12 more replies",
# "3 more replies", "1 more reply". Each is pressed once; at most EXPAND_MAX_CLICKS per round.
EXPAND_MAX_CLICKS = 10
EXPAND_JS = r"""(limit) => {
  const phrase = /^(?:(?:view|load|show|see)\s+(?:\d[\d,]*\s+)?more(?:\s+(?:comments?|replies|answers|posts|results|items))?|\d[\d,]*\s+more\s+(?:comments?|repl(?:y|ies)|answers?))\s*…?$/i;
  let clicked = 0;
  for (const button of document.querySelectorAll('button')) {
    if (clicked >= limit) break;
    if (button.dataset.harvesterPressed || button.disabled || button.closest('form')) continue;
    if (button.getAttribute('aria-hidden') === 'true') continue;
    const label = (button.innerText || '').replace(/\s+/g, ' ').trim();
    if (!phrase.test(label)) continue;
    const box = button.getBoundingClientRect();
    if (!box.width || !box.height) continue;
    button.dataset.harvesterPressed = '1';
    button.click();
    clicked++;
  }
  return clicked;
}"""


async def scroll_until_stable(measure, scroll, settle, clock, expand=None, max_rounds=SCROLL_MAX_ROUNDS,
                              max_seconds=SCROLL_MAX_SECONDS, stable_rounds=SCROLL_STABLE_ROUNDS, unblock=None):
    """Scroll (and expand) until the measured content stops growing, or a cap
    is reached.

    measure/scroll/settle/expand/unblock are awaitables over the page (expand
    returns how many controls it pressed; scroll may return SCROLL_JS's
    {"moved", "blocked"}; unblock clears an overlay and returns its name);
    clock returns seconds. Pure over those, so the loop is testable with no
    browser. A scroll that reports "blocked" is never quiet growth: unblock
    runs once and the scroll is retried, and a page still blocked stops
    "blocked" — a render that never scrolled is INCOMPLETE, not stable.
    Returns {"rounds", "size", "initial", "expanded", "stopped", "growing"}
    (plus "overlay" once unblock ran): stopped is "stable", "blocked",
    "round-cap" or "time-cap", and growing is True when a cap stopped a page
    that grew and never went SCROLL_STABLE_ROUNDS rounds without growing —
    content still arriving, the render then INCOMPLETE."""
    start = clock()
    initial = size = await measure()
    rounds = unchanged = expanded = 0
    stopped = "stable"
    overlay = None

    def blocked(result):
        return isinstance(result, dict) and bool(result.get("blocked"))

    while True:
        if unchanged >= stable_rounds:
            stopped = "stable"
            break
        if rounds >= max_rounds:
            stopped = "round-cap"
            break
        if clock() - start >= max_seconds:
            stopped = "time-cap"
            break
        result = await scroll()
        if blocked(result) and unblock is not None and overlay is None:
            overlay = await unblock() or ""
            result = await scroll()
        if blocked(result):
            stopped = "blocked"
            break
        if expand is not None:
            expanded += await expand()
        await settle()
        rounds += 1
        current = await measure()
        if current > size:
            size, unchanged = current, 0
        else:
            unchanged += 1
    outcome = {
        "rounds": rounds,
        "size": size,
        "initial": initial,
        "expanded": expanded,
        "stopped": stopped,
        # A cap stop never proved stability; the content grew in some round
        # (unchanged counts the quiet rounds since the last growth), so it may
        # still be arriving even when the very last round brought nothing.
        "growing": stopped not in ("stable", "blocked") and unchanged < rounds,
    }
    if overlay is not None:
        outcome["overlay"] = overlay
    return outcome


# The navigation guard's window sentinel: planted in the document render_page
# scrolls, checked after scrolling. A document that no longer carries it was
# replaced (a navigation or a reload), not merely re-addressed by pushState.
DOCUMENT_PLANT_JS = "(token) => { window.__harvesterDocument = token; }"
DOCUMENT_CHECK_JS = "(token) => window.__harvesterDocument === token"

# The name of the marker an incomplete lazy-load leaves in the returned HTML.
# Go reads it (harvest's lazyLoadIncomplete) and flags the artifact partial at
# the visible surface — the HTML is the one channel every caller already reads.
LAZY_LOAD_MARKER = "harvester-lazy-load"
# The attribute carrying the request's marker_token on that marker. Go reads
# back only a marker holding its own token, so a page that ships a meta of the
# same name in its markup never flags itself partial.
MARKER_TOKEN_ATTR = "data-harvester-token"


def mark_incomplete(html, outcome, marker_token):
    """Stamp *html* with the lazy-load marker, carrying *marker_token*, when a
    cap stopped a page that was still growing, when scrolling failed (stopped
    "error"), when the page navigated away while scrolling (stopped
    "navigated"), or when a scroll lock the rung could not undo kept the page
    from scrolling (stopped "blocked", naming the overlay it removed); otherwise
    return it unchanged."""
    if outcome.get("stopped") == "blocked":
        named = f': {outcome["overlay"]}' if outcome.get("overlay") else ""
        reason = html_escape(f"incomplete: scrolling was blocked (a consent or modal overlay{named}) and could not "
                             "be unlocked", quote=True)
    elif outcome.get("stopped") == "error":
        reason = html_escape(f'incomplete: scrolling failed: {outcome.get("error", "")}', quote=True)
    elif outcome.get("stopped") == "navigated":
        reason = html_escape(f'incomplete: the page navigated away to {outcome.get("navigated_to", "")} while '
                             'scrolling; kept as it was before scrolling', quote=True)
    elif outcome.get("growing"):
        reason = (f'incomplete: content was still loading when the {outcome["stopped"]} stopped scrolling after '
                  f'{outcome["rounds"]} rounds')
    else:
        return html
    token = html_escape(marker_token or "", quote=True)
    meta = f'<meta name="{LAZY_LOAD_MARKER}" content="{reason}" {MARKER_TOKEN_ATTR}="{token}">'
    lower = html.lower()
    index = lower.find("<head")
    if index >= 0:
        close = lower.find(">", index)
        if close >= 0:
            return html[:close + 1] + meta + html[close + 1:]
    return meta + html


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


async def render_page(context, url, timeout_ms, referer=None, clock=None, press_loaders=False,
                      ua_override=None, marker_token=""):
    """Open *url* in *context*, let it settle, scroll it until stable and
    return (html, status, scroll outcome). The navigation carries *referer*
    (a provenance Referer: some anti-bot walls open for any Referer and refuse
    a Referer-less arrival); subresources keep Chrome's own. Load-more buttons
    are pressed only when *press_loaders* (Go asks for a registered site). A
    scroll that fails (the page navigated, its context died) keeps the page as
    rendered, stamped incomplete with the reason. A page whose document was
    replaced by one at another origin or path while scrolling (a login or
    consent redirect) returns the page as it stood when scrolling began,
    stamped incomplete — never the document it navigated to. *ua_override*
    (the headless render's UA and client hints) is sent to the page over CDP
    before it navigates; a failed send raises. The incomplete stamp carries
    *marker_token* (mark_incomplete), and the outcome's "url" is the address of
    the document returned — the page as it stood when scrolling began."""
    page = await context.new_page()
    if ua_override is not None:
        session = await context.new_cdp_session(page)
        await session.send("Emulation.setUserAgentOverride", ua_override)
    resp = await page.goto(url, timeout=timeout_ms, wait_until="domcontentloaded", referer=referer or None)
    shell_text = await page.evaluate(TEXT_JS) if is_hash_route(url) else None
    try:
        await page.wait_for_load_state("networkidle", timeout=15_000)
    except Exception as e:  # noqa: BLE001 — best-effort quiet-period wait
        print(f"browser networkidle wait ended early for {redact(url)}: {e}", file=sys.stderr)
    clock = clock or asyncio.get_event_loop().time
    if shell_text is not None and len(shell_text.strip()) <= ROUTE_SHELL_TEXT_MAX:
        settled = await settle_route(lambda: page.evaluate(TEXT_JS), lambda: page.wait_for_timeout(ROUTE_PAUSE_MS),
                                     clock, shell_text)
        print(f"browser hash route {redact(url)}: {settled}", file=sys.stderr)
    start_url = page.url
    try:
        consent = await dismiss_consent(page)
    except Exception as e:  # noqa: BLE001 — a failed dismissal leaves the lock to the scroll's blocked check
        print(f"browser consent dismissal failed for {redact(start_url)}: {e}", file=sys.stderr)
    else:
        if consent["pressed"] or consent["removed"]:
            print(f"browser consent {redact(start_url)}: pressed {consent['pressed']}, removed {consent['removed']}",
                  file=sys.stderr)

    async def unblock():
        return ", ".join(await clear_consent(page, force=True))

    before_scrolling = await snapshot(page)
    token = os.urandom(8).hex()
    try:
        await page.evaluate(DOCUMENT_PLANT_JS, token)
    except Exception as e:  # noqa: BLE001 — an unplanted sentinel reads as a replaced document below
        print(f"browser document sentinel not planted for {redact(start_url)}: {e}", file=sys.stderr)
    expand = (lambda: page.evaluate(EXPAND_JS, EXPAND_MAX_CLICKS)) if press_loaders else None
    try:
        outcome = await scroll_until_stable(
            lambda: page.evaluate(MEASURE_JS),
            lambda: page.evaluate(SCROLL_JS),
            lambda: page.wait_for_timeout(SCROLL_SETTLE_MS),
            clock,
            expand=expand,
            unblock=unblock,
        )
    except Exception as e:  # noqa: BLE001 — a failed scroll keeps the render, stamped incomplete
        print(f"browser scroll failed for {redact(url)}: {e}; keeping the page as rendered", file=sys.stderr)
        outcome = {"stopped": "error", "error": str(e), "growing": False}
    else:
        print(f"browser scroll {redact(url)}: {outcome['initial']} -> {outcome['size']} chars in "
              f"{outcome['rounds']} rounds, {outcome['expanded']} load-more presses, stopped={outcome['stopped']}",
              file=sys.stderr)
    status = resp.status if resp else None
    # The final snapshot is taken BEFORE the document check: a navigation still
    # in flight at the check could otherwise commit between the two, and its
    # document would be returned as the source. A document that passes the
    # check after the snapshot is the one the snapshot read. A snapshot that
    # raises (a navigation destroying the document) is retaken only when the
    # page did not navigate away.
    try:
        after_scrolling = await snapshot(page)
    except Exception as e:  # noqa: BLE001 — decided by the document check below
        print(f"browser snapshot after scrolling raised for {redact(start_url)}: {e}", file=sys.stderr)
        after_scrolling = None
    if await document_replaced(page, token) and not same_page(start_url, page.url):
        outcome = {"stopped": "navigated", "navigated_to": redact(page.url), "growing": False}
        print(f"browser render {redact(start_url)} navigated away to {redact(page.url)} while scrolling; "
              f"keeping the page as it was before scrolling", file=sys.stderr)
        outcome["url"] = start_url
        return mark_incomplete(before_scrolling, outcome, marker_token), status, outcome
    if after_scrolling is None:
        try:
            after_scrolling = await snapshot(page)
        except Exception as e:  # noqa: BLE001 — a lost retake keeps the pre-scroll snapshot, stamped incomplete
            print(f"browser retake snapshot raised for {redact(start_url)}: {e}; keeping the pre-scroll snapshot",
                  file=sys.stderr)
            outcome = {"stopped": "error", "error": str(e), "growing": False}
            outcome["url"] = start_url
            return mark_incomplete(before_scrolling, outcome, marker_token), status, outcome
    outcome["url"] = start_url
    html = mark_incomplete(after_scrolling, outcome, marker_token)
    return html, status, outcome


def is_hash_route(url):
    """Whether *url*'s fragment is a client router's route (#/… or #!…), not an anchor."""
    return urllib.parse.urlsplit(url).fragment.startswith(("/", "!"))


async def settle_route(text, pause, clock, shell, max_seconds=ROUTE_SETTLE_MAX_SECONDS,
                       quiet_rounds=ROUTE_QUIET_ROUNDS):
    """Wait for a hash route's view: until the page's text (the awaitable
    *text*) differs from *shell*, the text at load, and held unchanged for
    *quiet_rounds* pauses — "settled" — or *max_seconds* passed — "timeout",
    the page then captured as it stands. Pure over text/pause/clock."""
    start = clock()
    last, quiet = None, 0
    while clock() - start < max_seconds:
        current = await text()
        if current.strip() != shell.strip() and current == last:
            quiet += 1
            if quiet >= quiet_rounds:
                return "settled"
        else:
            quiet = 0
        last = current
        await pause()
    return "timeout"


async def document_replaced(page, token):
    """Whether the document render_page planted *token* in is gone. A check
    that raises (the navigation destroyed its context) counts as replaced."""
    try:
        return not await page.evaluate(DOCUMENT_CHECK_JS, token)
    except Exception as e:  # noqa: BLE001 — a destroyed context is a replaced document
        print(f"browser document check raised for {redact(page.url)}: {e}; counting it replaced", file=sys.stderr)
        return True


def same_page(first, second):
    """Whether two URLs name the same page: scheme, host and path equal;
    query and fragment ignored."""
    a, b = urllib.parse.urlsplit(first), urllib.parse.urlsplit(second)
    return (a.scheme, a.netloc.lower(), a.path) == (b.scheme, b.netloc.lower(), b.path)


async def fetch_browser(url, ask_fetchable, proxy_url=None, timeout_ms=45_000, headless=True,
                        host_resolver_rules=None, referer=None, press_loaders=False, marker_token="",
                        report=None):
    """Render *url* in a real system Chrome via Patchright; return (html, status, headless, error).
    A render also records, in the *report* dict when one is passed, "final_url": the address
    of the document html holds (render_page's outcome "url"); the incomplete stamp carries
    *marker_token*.

    Opt-in rung — Go gates it behind fetch.browser because a browser launch is ~100ms+ and
    needs Chrome installed. It renders in exactly the mode Go asks for: headless unless Go
    is spending the one headed (visible-window) retry on a wall the headless render met.
    It renders as a visitor would arrive: headless, a stock Chrome User-Agent
    (never HeadlessChrome) whose client hints agree with it (the engine's own,
    HeadlessChrome renamed Google Chrome); headed, Chrome's own UA; the provenance *referer* on the navigation, and the page
    scrolled until it stops growing — render_page(). Its load-more controls are
    pressed only when Go asks for a registered site (*press_loaders*); every
    other page is scrolled read-only.
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
                ua_override = None
                if headless:
                    metadata = await engine_ua_metadata(browser, guarded_ask)
                    user_agent = stock_user_agent(browser.version)
                    ua_override = {"userAgent": user_agent, "userAgentMetadata": metadata}
                    context = await browser.new_context(**context_options(user_agent))
                else:
                    context = await browser.new_context(**context_options(None))
                await install_route_guards(context, guarded_ask)
                await context.add_init_script(WEBRTC_BLOCK_SCRIPT)

                html, status, outcome = await render_page(context, url, timeout_ms, referer=referer,
                                                          press_loaders=press_loaders, ua_override=ua_override,
                                                          marker_token=marker_token)
                print(f"browser rung {redact(url)} -> HTTP {status} ({len(html)} chars, headless={headless})",
                      file=sys.stderr)
                if report is not None:
                    report["final_url"] = outcome.get("url", "")
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
    report = {}
    html, status, headless, error = await fetch_browser(
        request.get("url", ""),
        lambda target: _blocking_ask(target),
        proxy_url=request.get("proxy"),
        timeout_ms=int(request.get("timeout_ms") or 45_000),
        headless=bool(request.get("headless", True)),
        host_resolver_rules=request.get("host_resolver_rules") or None,
        referer=request.get("referer") or None,
        press_loaders=bool(request.get("press_loaders", False)),
        marker_token=request.get("marker_token") or "",
        report=report,
    )
    if error is not None and not html:
        return {"ok": False, "error": error}
    return {"ok": True, "html": html, "status": status, "headless": headless,
            "final_url": report.get("final_url", "")}


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
