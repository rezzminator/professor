"""Consent seam test — a consent banner never blocks the browser render, and
never silently.

The pure cases run with NO browser and NO patchright: the reject-label
pattern presses only a privacy-preserving choice, and a scroll that did not
move is never "stable" — it is unblocked once, and a page that stays blocked
is stopped "blocked" and stamped incomplete.

The live cases (BROWSER_LIVE=1) render local fixture pages in a real Chrome
through render_page: a scroll-locking consent overlay dismissed by "Reject
all", one with only "Accept" removed without accepting, a banner inside a
cross-origin iframe, a page whose lock cannot be undone, and a page that
scrolls in an inner container. With BROWSER_LIVE=1 a missing patchright or
Chrome is a failure, never a skip.

Run: python3 browser_consent_test.py            (pure cases)
     BROWSER_LIVE=1 python3 browser_consent_test.py   (pure + live cases)
"""

import asyncio
import functools
import http.server
import os
import sys
import tempfile
import threading

import browser
from browser import mark_incomplete, render_page, scroll_until_stable


def run(coro):
    return asyncio.new_event_loop().run_until_complete(coro)


def counting_clock():
    now = [0.0]

    def clock():
        now[0] += 1.0
        return now[0]

    return clock


async def settle():
    return None


def test_reject_labels_press_only_the_privacy_preserving_choice():
    for label in ("Reject all", "REJECT ALL", "Decline", "Decline all cookies", "Only necessary", "Necessary only",
                  "Use necessary cookies only", "Accept only essential cookies", "Deny", "Continue without accepting",
                  "Alle ablehnen", "Nur notwendige Cookies", "Tout refuser", "Continuer sans accepter",
                  "Rechazar todo", "Rifiuta tutto", "Alles weigeren", "Refuse all", "I do not accept"):
        assert browser.is_reject_label(label), f"a reject control was not recognised: {label!r}"
    for label in ("Accept", "Accept all", "Accept all cookies", "I accept", "Agree", "Allow all", "Alle akzeptieren",
                  "Tout accepter", "Aceptar todo", "Accetta tutto", "Manage options", "More options", "Settings",
                  "Reject all and subscribe to read", ""):
        assert not browser.is_reject_label(label), f"a non-reject control would be pressed: {label!r}"


def test_a_scroll_that_never_moves_is_blocked_never_stable():
    async def measure():
        return 1000

    async def scroll():
        return {"moved": False, "blocked": True}

    outcome = run(scroll_until_stable(measure, scroll, settle, counting_clock()))
    assert outcome["stopped"] == "blocked", f"a scroll that never moved was judged {outcome['stopped']!r}: {outcome}"
    html = mark_incomplete("<html><head></head><body>x</body></html>", outcome, "tok")
    assert "incomplete: scrolling was blocked (a consent or modal overlay" in html, html


def test_a_blocked_scroll_is_unblocked_once_and_then_loads():
    state = {"locked": True, "size": 1000, "unblocks": 0}

    async def measure():
        return state["size"]

    async def scroll():
        if state["locked"]:
            return {"moved": False, "blocked": True}
        if state["size"] < 4000:
            state["size"] += 1000
            return {"moved": True, "blocked": False}
        return {"moved": False, "blocked": False}

    async def unblock():
        state["unblocks"] += 1
        state["locked"] = False
        return "consent-banner"

    outcome = run(scroll_until_stable(measure, scroll, settle, counting_clock(), unblock=unblock))
    assert outcome["stopped"] == "stable" and outcome["size"] == 4000, outcome
    assert state["unblocks"] == 1, f"unblock ran {state['unblocks']} times, want once"


def test_a_page_that_stays_blocked_names_the_overlay():
    async def measure():
        return 1000

    async def scroll():
        return {"moved": False, "blocked": True}

    async def unblock():
        return "onetrust-consent-sdk"

    outcome = run(scroll_until_stable(measure, scroll, settle, counting_clock(), unblock=unblock))
    assert outcome["stopped"] == "blocked" and outcome.get("overlay") == "onetrust-consent-sdk", outcome
    html = mark_incomplete("<html><head></head><body>x</body></html>", outcome, "tok")
    assert "scrolling was blocked (a consent or modal overlay: onetrust-consent-sdk)" in html, html


def test_a_scroll_already_at_the_bottom_is_stable():
    async def measure():
        return 1000

    async def scroll():
        return {"moved": False, "blocked": False}

    outcome = run(scroll_until_stable(measure, scroll, settle, counting_clock()))
    assert outcome["stopped"] == "stable" and not outcome["growing"], outcome
    assert mark_incomplete("<html></html>", outcome, "tok") == "<html></html>"


# --- live fixtures (BROWSER_LIVE=1) -----------------------------------------

BLOCKS = 9
BLOCK_JS = """
function addBlocks(host, upTo) {
  const have = host.querySelectorAll('.block').length;
  for (let i = have + 1; i <= Math.min(have + 2, upTo); i++) {
    const block = document.createElement('section');
    block.className = 'block';
    block.style.height = '900px';
    block.textContent = 'BLOCK-' + i + ' ' + 'lorem ipsum '.repeat(20);
    host.appendChild(block);
  }
}
"""

WINDOW_FEED = """
<script>%s
addBlocks(document.getElementById('feed'), %d);
window.addEventListener('scroll', () => {
  if (window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 50) {
    addBlocks(document.getElementById('feed'), %d);
  }
});
</script>""" % (BLOCK_JS, BLOCKS, BLOCKS)

# The common scroll lock: the body pinned in place, the overflow hidden.
LOCK = "<style>html,body{overflow:hidden;height:100%} body{position:fixed;width:100%}</style>"


def overlay(buttons):
    return ('<div id="cmp-consent-banner" role="dialog" aria-label="Cookie consent" '
            'style="position:fixed;inset:0;background:rgba(0,0,0,.6);z-index:9999">'
            '<p>We use cookies to improve your visit. CONSENT-BANNER-TEXT</p>%s</div>' % buttons)


CONSENT_HANDLERS = """
<script>
function consent(choice) {
  document.body.dataset.consent = choice;
  document.getElementById('cmp-consent-banner').remove();
  document.head.querySelector('style').remove();
}
</script>"""

FIXTURES = {
    "reject.html": ("<html><head>%s</head><body><main id='feed'></main>%s%s%s</body></html>" % (
        LOCK, overlay('<button onclick="consent(\'accepted\')">Accept all</button>'
                      '<button onclick="consent(\'rejected\')">Reject all</button>'), CONSENT_HANDLERS, WINDOW_FEED)),
    "accept-only.html": ("<html><head>%s</head><body><main id='feed'></main>%s%s%s</body></html>" % (
        LOCK, overlay('<button onclick="consent(\'accepted\')">Accept all</button>'), CONSENT_HANDLERS, WINDOW_FEED)),
    # Sourcepoint's shape: a fixed container in the page, the choice inside a
    # cross-origin iframe that tells its parent over postMessage.
    "iframe.html": ("<html><head>%s</head><body><main id='feed'></main>"
                    "<div id='sp_message_container_1' style='position:fixed;inset:0;z-index:9999'>"
                    "<iframe id='sp_message_iframe_1' src='http://localhost:{PORT}/sp.html' "
                    "style='width:100%%;height:100%%'></iframe></div>"
                    "<script>window.addEventListener('message', (e) => { if (e.data === 'rejected') {"
                    "document.body.dataset.consent = 'rejected';"
                    "document.getElementById('sp_message_container_1').remove();"
                    "document.head.querySelector('style').remove(); } });</script>%s</body></html>" % (
                        LOCK, WINDOW_FEED)),
    "sp.html": ("<html><body><p>Privacy choices CONSENT-BANNER-TEXT</p>"
                "<button title='Accept all' onclick=\"parent.postMessage('accepted','*')\">Accept all</button>"
                "<button title='Reject all' class='message-button sp_choice_type_13' "
                "onclick=\"parent.postMessage('rejected','*')\">Reject all</button></body></html>"),
    # A lock nothing may undo: a modal (no consent markers) whose page re-locks
    # the moment anything touches the overflow.
    "blocked.html": ("<html><head></head><body><main id='feed'></main>"
                     "<div class='modal' style='position:fixed;inset:0;z-index:9999'>Subscribe to continue</div>"
                     "<script>function lock() { for (const el of [document.documentElement, document.body]) {"
                     "if (el.style.getPropertyValue('position') !== 'fixed' || el.style.getPropertyValue('overflow') !== 'hidden') {"
                     "el.style.setProperty('overflow', 'hidden', 'important'); el.style.setProperty('position', 'fixed', 'important');"
                     "el.style.setProperty('height', '100%%', 'important'); el.style.setProperty('width', '100%%', 'important'); } } }"
                     "lock(); new MutationObserver(lock).observe(document.documentElement, "
                     "{attributes: true, subtree: true, attributeFilter: ['style', 'class']});"
                     "setInterval(lock, 20);</script>%s</body></html>" % WINDOW_FEED),
    "container.html": ("<html><head><style>html,body{height:100%%;margin:0;overflow:hidden}</style></head><body>"
                       "<header style='height:40px'>app</header>"
                       "<div id='feed' style='height:calc(100%% - 40px);overflow-y:auto'></div>"
                       "<script>%s const feed = document.getElementById('feed'); addBlocks(feed, %d);"
                       "feed.addEventListener('scroll', () => { if (feed.scrollTop + feed.clientHeight >= "
                       "feed.scrollHeight - 50) addBlocks(feed, %d); });</script></body></html>" % (
                           BLOCK_JS, BLOCKS, BLOCKS)),
}


def serve(root):
    handler = functools.partial(http.server.SimpleHTTPRequestHandler, directory=root)
    handler.log_message = lambda *args: None
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    return server


async def render_fixtures(names):
    from patchright.async_api import async_playwright  # type: ignore[import-not-found]
    if not browser.chrome_binary():
        raise AssertionError("BROWSER_LIVE=1 but no system Chrome resolves")
    root = tempfile.mkdtemp(prefix="consent-fixtures-")
    server = serve(root)
    port = server.server_address[1]
    for name, body in FIXTURES.items():
        with open(os.path.join(root, name), "w", encoding="utf-8") as handle:
            handle.write(body.replace("{PORT}", str(port)))
    results = {}
    try:
        async with async_playwright() as p:
            chrome = await p.chromium.launch(channel="chrome", headless=True)
            try:
                for name in names:
                    context = await chrome.new_context()
                    try:
                        results[name] = await render_page(context, f"http://127.0.0.1:{port}/{name}", 30_000,
                                                          marker_token="tok")
                    finally:
                        await context.close()
            finally:
                await chrome.close()
    finally:
        server.shutdown()
    return results


LIVE = {}


def live(name):
    if name not in LIVE:
        LIVE.update(run(render_fixtures([n for n in FIXTURES if n != "sp.html"])))
    return LIVE[name]


def assert_every_block(html, outcome, name):
    missing = [i for i in range(1, BLOCKS + 1) if f"BLOCK-{i} " not in html]
    assert not missing, f"{name}: blocks {missing} never loaded; outcome={outcome}"
    assert browser.LAZY_LOAD_MARKER not in html, f"{name}: a complete render was stamped incomplete: {outcome}"


def live_test_reject_all_dismisses_the_overlay_and_loads_every_block():
    html, _, outcome = live("reject.html")
    assert 'data-consent="rejected"' in html, f"Reject all was not pressed: {outcome}"
    assert_every_block(html, outcome, "reject.html")
    assert "CONSENT-BANNER-TEXT" not in html and 'id="cmp-consent-banner"' not in html, "the stored HTML carries the banner"


def live_test_an_accept_only_overlay_is_removed_never_accepted():
    html, _, outcome = live("accept-only.html")
    assert "data-consent" not in html, f"the render accepted consent: {outcome}"
    assert_every_block(html, outcome, "accept-only.html")
    assert "CONSENT-BANNER-TEXT" not in html, "the stored HTML carries the banner"


def live_test_a_banner_inside_an_iframe_is_dismissed():
    html, _, outcome = live("iframe.html")
    assert 'data-consent="rejected"' in html, f"the iframe's Reject all was not pressed: {outcome}"
    assert_every_block(html, outcome, "iframe.html")
    assert 'id="sp_message_container_1"' not in html, "the stored HTML carries the banner container"


def live_test_a_lock_that_cannot_be_undone_is_stamped_blocked():
    html, _, outcome = live("blocked.html")
    assert outcome.get("stopped") == "blocked", f"a locked page was judged {outcome.get('stopped')!r}: {outcome}"
    assert "incomplete: scrolling was blocked" in html, "the blocked render is not stamped incomplete"


def live_test_an_inner_scroll_container_is_scrolled():
    html, _, outcome = live("container.html")
    assert_every_block(html, outcome, "container.html")


if __name__ == "__main__":
    prefixes = ("test_", "live_test_") if os.environ.get("BROWSER_LIVE") == "1" else ("test_",)
    failures = 0
    for name, case in sorted(globals().items()):
        if name.startswith(prefixes) and callable(case):
            try:
                case()
            except Exception as e:  # noqa: BLE001 — a raising case is a failure, never the end of the run
                print(f"FAIL {name}: {type(e).__name__}: {e}", file=sys.stderr)
                failures += 1
            else:
                print(f"ok {name}")
    sys.exit(1 if failures else 0)
