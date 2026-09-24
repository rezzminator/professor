"""Render seam test — runs with NO browser, NO patchright. Pins the browser
rung's provenance (a stock Chrome User-Agent, never HeadlessChrome; the
provenance Referer on the navigation), its scroll-until-stable loop over a
generic lazy-loaded feed, the load-more presses only a registered site's
request asks for, a scroll failure that keeps the rendered page, the
navigation guard (a page that navigated away mid-scroll is kept as it was
before scrolling), and the headless UA override whose client hints are the
engine's own with HeadlessChrome renamed.

A fake patchright module is installed in sys.modules so fetch_browser's real
wiring runs end to end: what it hands to new_context and goto is what the
test reads.

Run: python3 browser_render_test.py
"""

import asyncio
import contextlib
import io
import sys
import types

import browser
from browser import (
    CONTEXT_OPTIONS,
    DOCUMENT_CHECK_JS,
    DOCUMENT_PLANT_JS,
    ENGINE_INFO_URL,
    LAZY_LOAD_MARKER,
    context_options,
    fetch_browser,
    handle_fetch,
    mark_incomplete,
    render_page,
    scroll_until_stable,
    stock_user_agent,
)


def run(coro):
    return asyncio.new_event_loop().run_until_complete(coro)


def test_stock_user_agent_is_never_headless():
    ua = stock_user_agent("153.0.8010.52", "linux")
    assert "Headless" not in ua, f"the rung announces headless Chrome: {ua!r}"
    assert ua == (
        "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) "
        "Chrome/153.0.0.0 Safari/537.36"
    ), ua
    assert "Macintosh; Intel Mac OS X 10_15_7" in stock_user_agent("153.0.8010.52", "darwin")
    assert "Windows NT 10.0; Win64; x64" in stock_user_agent("153.0.8010.52", "win32")


def test_stock_user_agent_refuses_an_unreadable_version():
    for version in ("", None, "HeadlessChrome/153", "latest"):
        try:
            stock_user_agent(version, "linux")
        except ValueError:
            continue
        raise AssertionError(f"an unreadable Chrome version {version!r} became a User-Agent")


def test_context_options_carry_the_stock_ua_and_keep_the_ssrf_posture():
    options = context_options(stock_user_agent("153.0.1.2", "linux"))
    assert options["user_agent"].endswith("Chrome/153.0.0.0 Safari/537.36"), options
    for key, value in CONTEXT_OPTIONS.items():
        assert options.get(key) == value, f"context option {key} dropped: {options!r}"


SCROLL_FAILURE = "Execution context was destroyed, most likely because of a navigation"
OTHER_DOCUMENT = "the document the page navigated to"
UNREADABLE = "unreadable"

# navigator.userAgentData as headless Chrome 153 reports it on Linux.
HEADLESS_UA_DATA = {
    "brands": [{"brand": "Not.A/Brand", "version": "99"}, {"brand": "HeadlessChrome", "version": "153"},
               {"brand": "Chromium", "version": "153"}],
    "mobile": False,
    "platform": "Linux",
    "architecture": "x86",
    "bitness": "64",
    "fullVersionList": [{"brand": "Not.A/Brand", "version": "99.0.0.0"},
                        {"brand": "HeadlessChrome", "version": "153.0.8010.52"},
                        {"brand": "Chromium", "version": "153.0.8010.52"}],
    "model": "",
    "platformVersion": "6.8.0",
    "wow64": False,
}


class FeedPage:
    """A generic lazy-loaded feed (no forum markup at all): every scroll to the
    bottom appends one batch of items until the feed runs out; a "Show more"
    button, while it lasts (show_more presses), appends one extra batch per
    press. fail_on_scroll makes that scroll round raise the way a page that
    navigated mid-scroll does. navigate_on_scroll = (round, url, replaced)
    moves the page to url on that scroll round: replaced, the document is
    swapped for another one (its window sentinel gone, its content that
    document's); not replaced, it is a same-document history.pushState.
    check_raises makes the document check raise, the way evaluate does in a
    context the navigation destroyed. ua_data is what the page answers for
    navigator.userAgentData (None: the engine exposes none). navigate_after_check
    is a url a navigation still in flight at the document check commits to right
    after it: the check still finds the planted document, the next read does not.
    content_raises is how many content() reads after scrolling began raise
    before one succeeds."""

    def __init__(self, batches, batch_chars=500, show_more=0, fail_on_scroll=None, navigate_on_scroll=None,
                 check_raises=False, ua_data=None, navigate_after_check=None, content_raises=0):
        self.remaining = batches
        self.size = batch_chars
        self.batch = batch_chars
        self.goto_calls = []
        self.scrolls = 0
        self.show_more = show_more
        self.expand_calls = 0
        self.fail_on_scroll = fail_on_scroll
        self.navigate_on_scroll = navigate_on_scroll
        self.check_raises = check_raises
        self.navigate_after_check = navigate_after_check
        self.content_raises = content_raises
        self.ua_data = HEADLESS_UA_DATA if ua_data is None else ua_data
        self.ua_data_reads = 0
        self.url = "about:blank"
        self.sentinel = None
        self.replaced = False
        self.context = None
        self.frames = [self]  # patchright lists the main frame first

    async def goto(self, url, **kwargs):
        self.goto_calls.append((url, kwargs))
        self.context.goto_urls.append(url)
        self.url = url
        return type("Response", (), {"status": 200})()

    async def wait_for_load_state(self, state, timeout=None):
        return None

    async def wait_for_timeout(self, ms):
        return None

    async def evaluate(self, script, arg=None):
        if script == DOCUMENT_PLANT_JS:
            self.sentinel = arg
            return None
        if script == DOCUMENT_CHECK_JS:
            if self.check_raises:
                raise RuntimeError(SCROLL_FAILURE)
            present = self.sentinel == arg
            if self.navigate_after_check:
                self.url, self.replaced, self.sentinel = self.navigate_after_check, True, None
                self.navigate_after_check = None
            return present
        if "harvesterConsent" in script:
            return {"pressed": [], "removed": []}
        if "userAgentData" in script:
            self.ua_data_reads += 1
            return None if self.ua_data == UNREADABLE else self.ua_data
        if "innerText.length" in script:
            return self.size
        if "scrollTo" in script:
            self.scrolls += 1
            if self.navigate_on_scroll and self.scrolls == self.navigate_on_scroll[0]:
                _, self.url, self.replaced = self.navigate_on_scroll
                if self.replaced:
                    self.sentinel = None
            if self.scrolls == self.fail_on_scroll:
                raise RuntimeError(SCROLL_FAILURE)
            if self.remaining > 0:
                self.remaining -= 1
                self.size += self.batch
            return None
        if "harvesterPressed" in script:
            self.expand_calls += 1
            if self.show_more > 0:
                self.show_more -= 1
                self.size += self.batch
                return 1
            return 0
        raise AssertionError(f"unexpected page script: {script[:60]!r}")

    async def content(self):
        if self.content_raises > 0 and self.scrolls > 0:
            self.content_raises -= 1
            raise RuntimeError(SCROLL_FAILURE)
        if self.replaced:
            return f"<html><head><title>elsewhere</title></head><body>{OTHER_DOCUMENT}</body></html>"
        return f"<html><head><title>feed</title></head><body>{'x' * self.size}</body></html>"


class FakeContext:
    def __init__(self, page, options=None):
        self.page = page
        self.options = options
        self.routes = []
        self.init_scripts = []
        self.goto_urls = []
        self.closed = False
        self.cdp_sends = []

    async def new_page(self):
        self.page.context = self
        return self.page

    async def new_cdp_session(self, page):
        return FakeCDPSession(self, page)

    async def close(self):
        self.closed = True

    async def route(self, glob, handler):
        self.routes.append(("route", glob))

    async def route_web_socket(self, glob, handler):
        self.routes.append(("ws", glob))

    async def add_init_script(self, script):
        self.init_scripts.append(script)


class FakeCDPSession:
    """A CDP session on one page: every send is recorded with how many
    navigations its context had made by then."""

    def __init__(self, context, page):
        self.context = context
        self.page = page

    async def send(self, method, params=None):
        self.context.cdp_sends.append((method, params, len(self.context.goto_urls)))


class FakeBrowser:
    """patchright's Browser as fetch_browser drives it: version read
    synchronously, one context per fetch, every context over the one page."""

    version = "153.0.8010.52"

    def __init__(self, page):
        self.page = page
        self.contexts = []
        self.closed = False

    async def new_context(self, **options):
        context = FakeContext(self.page, options)
        self.contexts.append(context)
        return context

    async def close(self):
        self.closed = True


class FakeChromium:
    def __init__(self, fake_browser):
        self.browser = fake_browser
        self.launches = []

    async def launch(self, **kwargs):
        self.launches.append(kwargs)
        return self.browser


class FakePlaywright:
    def __init__(self, fake_browser):
        self.chromium = FakeChromium(fake_browser)

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        return False


@contextlib.contextmanager
def fake_patchright(fake_browser):
    """Install a patchright stand-in for fetch_browser's lazy import."""
    module = types.ModuleType("patchright")
    api = types.ModuleType("patchright.async_api")
    api.async_playwright = lambda: FakePlaywright(fake_browser)
    module.async_api = api
    saved = {name: sys.modules.get(name) for name in ("patchright", "patchright.async_api")}
    sys.modules["patchright"] = module
    sys.modules["patchright.async_api"] = api
    try:
        yield
    finally:
        for name, previous in saved.items():
            if previous is None:
                sys.modules.pop(name, None)
            else:
                sys.modules[name] = previous


def fake_clock():
    now = [0.0]

    def clock():
        now[0] += 1.0
        return now[0]

    return clock


def test_render_page_navigates_with_the_provenance_referer():
    page = FeedPage(batches=0)
    run(render_page(FakeContext(page), "https://feed.example.test/items", 45_000,
                    referer="https://www.google.com/", clock=fake_clock()))
    assert page.goto_calls, "the page was never opened"
    _, kwargs = page.goto_calls[0]
    assert kwargs.get("referer") == "https://www.google.com/", (
        f"the navigation arrived Referer-less: {kwargs!r}"
    )


def test_render_page_scrolls_a_lazy_feed_until_it_stops_growing():
    page = FeedPage(batches=4)
    html, status, outcome = run(render_page(FakeContext(page), "https://feed.example.test/items", 45_000,
                                            clock=fake_clock()))
    assert status == 200
    assert outcome["size"] == 5 * 500, f"the feed was not scrolled to its end: {outcome!r}"
    assert outcome["stopped"] == "stable" and not outcome["growing"], outcome
    assert html.count("x") == 5 * 500, "the returned HTML is not the scrolled page"
    assert LAZY_LOAD_MARKER not in html, "a complete render was stamped incomplete"


def test_scroll_stops_at_the_round_cap_and_names_the_render_incomplete():
    page = FeedPage(batches=1000)
    outcome = run(scroll_until_stable(
        lambda: page.evaluate("innerText.length"),
        lambda: page.evaluate("scrollTo"),
        lambda: page.wait_for_timeout(0),
        fake_clock(),
        max_rounds=5, max_seconds=1_000, stable_rounds=3,
    ))
    assert outcome["rounds"] == 5 and outcome["stopped"] == "round-cap", outcome
    assert outcome["growing"], f"a feed still growing at the cap read as complete: {outcome!r}"


def test_scroll_stops_at_the_time_cap():
    page = FeedPage(batches=1000)
    outcome = run(scroll_until_stable(
        lambda: page.evaluate("innerText.length"),
        lambda: page.evaluate("scrollTo"),
        lambda: page.wait_for_timeout(0),
        fake_clock(),
        max_rounds=1_000, max_seconds=4, stable_rounds=3,
    ))
    assert outcome["stopped"] == "time-cap" and outcome["growing"], outcome
    assert outcome["rounds"] < 10, f"the time cap did not bound the loop: {outcome!r}"


def test_scroll_names_a_slow_feed_incomplete_even_when_its_last_round_was_quiet():
    """A slow feed lands a batch every other round: the cap stops it one quiet
    round after a batch, before stability was ever proven — still arriving."""
    size, scrolls = [500], [0]

    async def measure():
        return size[0]

    async def scroll():
        scrolls[0] += 1
        if scrolls[0] % 2 == 0:
            size[0] += 500

    async def settle():
        return None

    outcome = run(scroll_until_stable(measure, scroll, settle, fake_clock(),
                                      max_rounds=5, max_seconds=1_000, stable_rounds=3))
    assert outcome["stopped"] == "round-cap" and outcome["size"] > outcome["initial"], outcome
    assert outcome["growing"], f"a feed still arriving at the cap read as complete: {outcome!r}"
    static = FeedPage(batches=0)
    quiet = run(scroll_until_stable(
        lambda: static.evaluate("innerText.length"),
        lambda: static.evaluate("scrollTo"),
        lambda: static.wait_for_timeout(0),
        fake_clock(),
        max_rounds=1_000, max_seconds=2, stable_rounds=3,
    ))
    assert quiet["stopped"] == "time-cap" and not quiet["growing"], (
        f"a page that never grew was stamped incomplete: {quiet!r}"
    )


def test_scroll_presses_load_more_and_counts_its_growth():
    page = FeedPage(batches=0)
    presses = [3, 2, 0, 0, 0, 0]

    async def expand():
        pressed = presses.pop(0) if presses else 0
        page.size += pressed * 100
        return pressed

    outcome = run(scroll_until_stable(
        lambda: page.evaluate("innerText.length"),
        lambda: page.evaluate("scrollTo"),
        lambda: page.wait_for_timeout(0),
        fake_clock(),
        expand=expand,
    ))
    assert outcome["expanded"] == 5, outcome
    assert outcome["size"] == 500 + 500, f"load-more growth was not waited for: {outcome!r}"
    assert outcome["stopped"] == "stable", outcome


def test_fetch_browser_sends_the_stock_user_agent_and_the_referer_end_to_end():
    page = FeedPage(batches=1)
    fake = FakeBrowser(page)
    with fake_patchright(fake):
        html, status, error = run(fetch_browser(
            "https://forum.example.test/t/lazy-thread",
            lambda target: (True, None),
            proxy_url="http://127.0.0.1:8431",
            referer="https://www.google.com/",
        ))
    assert error is None, f"the render failed: {error!r}"
    assert status == 200 and "<body>" in html, (status, html[:60])
    navigating = [c for c in fake.contexts if web_navigations(c)]
    assert navigating == [fake.contexts[-1]], [(c.options, c.goto_urls) for c in fake.contexts]
    options = fake.contexts[-1].options
    agent = options.get("user_agent", "")
    assert agent and "HeadlessChrome" not in agent, f"the context announces no stock UA: {options!r}"
    assert "Chrome/153.0.0.0" in agent, agent
    assert options.get("service_workers") == "block", options
    (url, kwargs), = [call for call in page.goto_calls if call[0] != ENGINE_INFO_URL]
    assert url == "https://forum.example.test/t/lazy-thread"
    assert kwargs.get("referer") == "https://www.google.com/", f"the navigation arrived Referer-less: {kwargs!r}"
    assert fake.closed, "the browser was left open"


def test_render_page_presses_load_more_when_the_request_asks():
    page = FeedPage(batches=0, show_more=2)
    html, _, outcome = run(render_page(FakeContext(page), "https://www.reddit.com/r/x/comments/1/", 45_000,
                                       clock=fake_clock(), press_loaders=True))
    assert page.expand_calls > 0, "a registered site's render never evaluated EXPAND_JS"
    assert outcome["expanded"] == 2 and outcome["size"] == 3 * 500, outcome
    assert html.count("x") == 3 * 500, "the pressed content is not in the returned HTML"


def test_render_page_never_presses_an_unregistered_page():
    page = FeedPage(batches=2, show_more=2)
    _, _, outcome = run(render_page(FakeContext(page), "https://forum.example.test/t/1", 45_000,
                                    clock=fake_clock()))
    assert page.expand_calls == 0, f"EXPAND_JS ran {page.expand_calls} times on a page no request asked to press"
    assert page.scrolls > 0 and outcome["size"] == 3 * 500, f"scrolling did not still run: {outcome!r}"
    assert outcome["expanded"] == 0, outcome


HANDLED_URL = "https://www.reddit.com/r/x/comments/1/"


def handle_fetch_reply(page, request):
    saved = browser._blocking_ask
    browser._blocking_ask = lambda target: (True, None)
    try:
        with fake_patchright(FakeBrowser(page)):
            return run(handle_fetch({"op": "fetch", "url": HANDLED_URL, "proxy": "http://127.0.0.1:8431", **request}))
    finally:
        browser._blocking_ask = saved


def test_every_launch_is_headless_whatever_the_request_says():
    """A fetch and a download each carrying "headless": false (what an older Go
    sent for its visible-window retry) still launch Chrome with headless=True."""
    launches = []
    original = FakeChromium.launch

    async def recording(self, **kwargs):
        launches.append(kwargs)
        return await original(self, **kwargs)

    FakeChromium.launch = recording
    saved = browser._blocking_ask
    browser._blocking_ask = lambda target: (True, None)
    request = {"url": HANDLED_URL, "proxy": "http://127.0.0.1:8431", "headless": False, "timeout_ms": 1000}
    try:
        with fake_patchright(FakeBrowser(FeedPage(batches=0))), contextlib.redirect_stderr(io.StringIO()):
            run(handle_fetch({"op": "fetch", **request}))
            try:
                run(browser.handle_download({"op": "download", "path": "/nonexistent/x", "max_bytes": 64, **request}))
            except Exception:  # noqa: BLE001 — the fake context cannot download; only the launch is under test
                pass
    finally:
        FakeChromium.launch = original
        browser._blocking_ask = saved
    assert [launch.get("headless") for launch in launches] == [True, True], launches


def run_handle_fetch(request):
    page = FeedPage(batches=0, show_more=1)
    reply = handle_fetch_reply(page, request)
    assert reply.get("ok"), reply
    return page


def test_handle_fetch_reads_press_loaders_from_the_wire():
    assert run_handle_fetch({"press_loaders": True}).expand_calls > 0, "press_loaders true did not press"
    assert run_handle_fetch({}).expand_calls == 0, "a request without press_loaders pressed"
    assert run_handle_fetch({"press_loaders": False}).expand_calls == 0, "press_loaders false pressed"


def test_render_page_keeps_the_page_when_scrolling_fails():
    page = FeedPage(batches=5, fail_on_scroll=2)
    stderr = io.StringIO()
    with contextlib.redirect_stderr(stderr):
        html, status, outcome = run(render_page(FakeContext(page), "https://forum.example.test/t/1?token=s3cret",
                                                45_000, clock=fake_clock()))
    assert status == 200, status
    body = html.split("<body>", 1)[1].split("</body>", 1)[0]
    assert body == "x" * 2 * 500, "the render was not the page as it stood when scrolling failed"
    assert f'<meta name="{LAZY_LOAD_MARKER}"' in html, f"a failed scroll was not stamped incomplete: {html[:200]}"
    assert "scrolling failed" in html and SCROLL_FAILURE in html, html[:300]
    assert outcome["stopped"] == "error" and outcome["error"] == SCROLL_FAILURE, outcome
    lines = [line for line in stderr.getvalue().splitlines() if "scroll failed" in line]
    assert len(lines) == 1, stderr.getvalue()
    assert "https://forum.example.test/t/1" in lines[0] and SCROLL_FAILURE in lines[0], lines
    assert "s3cret" not in stderr.getvalue(), "the scroll failure logged an unredacted URL"


def test_mark_incomplete_escapes_a_failed_scroll_reason():
    html = "<html><head><title>t</title></head><body>b</body></html>"
    marked = mark_incomplete(html, {"stopped": "error", "error": 'boom "<script>"', "growing": False}, "t0k")
    assert 'content="incomplete: scrolling failed: boom &quot;&lt;script&gt;&quot;"' in marked, marked


def test_mark_incomplete_stamps_only_an_incomplete_render():
    html = "<html><head><title>t</title></head><body>b</body></html>"
    assert mark_incomplete(html, {"growing": False, "stopped": "stable", "rounds": 2}, "t0k") == html
    marked = mark_incomplete(html, {"growing": True, "stopped": "time-cap", "rounds": 9}, "t0k")
    assert f'<meta name="{LAZY_LOAD_MARKER}"' in marked, marked
    assert marked.index(LAZY_LOAD_MARKER) < marked.index("<title>"), "the marker is not in <head>"
    assert "time-cap" in marked and "9 rounds" in marked, marked


NAVIGATED_TO = "https://elsewhere.example.test/login?next=s3cret"


def render_navigating(page, url="https://forum.example.test/t/1?token=s3cret"):
    stderr = io.StringIO()
    with contextlib.redirect_stderr(stderr):
        html, status, outcome = run(render_page(FakeContext(page), url, 45_000, clock=fake_clock()))
    return html, status, outcome, stderr.getvalue()


def test_render_page_keeps_the_page_from_before_a_navigation_away():
    cases = [
        ("another origin", NAVIGATED_TO, {}, "https://elsewhere.example.test/login"),
        ("another path", "https://forum.example.test/login?next=s3cret", {}, "https://forum.example.test/login"),
        ("another origin, scroll error", NAVIGATED_TO, {"fail_on_scroll": 2}, "https://elsewhere.example.test/login"),
        ("another origin, check raises", NAVIGATED_TO, {"check_raises": True}, "https://elsewhere.example.test/login"),
    ]
    for name, target, extra, redacted in cases:
        page = FeedPage(batches=5, navigate_on_scroll=(2, target, True), **extra)
        html, status, outcome, stderr = render_navigating(page)
        assert status == 200, (name, status)
        body = html.split("<body>", 1)[1].split("</body>", 1)[0]
        assert body == "x" * 500, f"{name}: not the page as it stood when scrolling began: {html[:200]}"
        assert OTHER_DOCUMENT not in html, f"{name}: the navigated-to document was returned"
        marker = (f'<meta name="{LAZY_LOAD_MARKER}" content="incomplete: the page navigated away to {redacted} '
                  f'while scrolling; kept as it was before scrolling"')
        assert marker in html, f"{name}: {html[:400]}"
        assert outcome == {"stopped": "navigated", "navigated_to": redacted, "growing": False,
                           "url": "https://forum.example.test/t/1?token=s3cret"}, (name, outcome)
        lines = [line for line in stderr.splitlines() if "https://forum.example.test/t/1" in line and redacted in line]
        assert len(lines) == 1, f"{name}: {stderr}"
        assert "s3cret" not in stderr and "s3cret" not in html, f"{name}: an unredacted URL leaked"


def test_render_page_never_returns_a_document_that_committed_after_the_check():
    page = FeedPage(batches=2, navigate_after_check=NAVIGATED_TO)
    html, status, outcome, _ = render_navigating(page)
    assert status == 200, status
    assert OTHER_DOCUMENT not in html, f"the navigated-to document was returned as the source: {html[:300]}"
    assert "x" * 500 in html, html[:300]
    reloading = FeedPage(batches=2, content_raises=1)
    html, _, outcome, _ = render_navigating(reloading)
    assert outcome["stopped"] == "stable" and html.count("x") == 3 * 500, (outcome, html[:300])


def test_render_page_keeps_the_pre_scroll_snapshot_when_the_retake_also_raises():
    """F3: a renderer crash during scrolling can leave the first post-scroll
    content() read raising (caught, after_scrolling = None) with the page
    never having navigated, so the retake at the end of render_page runs. If
    the retake ALSO raises, the render must not be lost to it — it falls back
    to the good pre-scroll snapshot, stamped incomplete with an error
    outcome, instead of letting the exception escape render_page."""
    page = FeedPage(batches=2, content_raises=2)
    html, status, outcome = run(render_page(FakeContext(page), "https://forum.example.test/t/1", 45_000,
                                            clock=fake_clock()))
    assert status == 200, status
    body = html.split("<body>", 1)[1].split("</body>", 1)[0]
    assert body == "x" * 500, f"the render did not fall back to the pre-scroll snapshot: {html[:300]}"
    assert outcome["stopped"] == "error" and outcome["error"] == SCROLL_FAILURE, outcome
    assert outcome["url"] == "https://forum.example.test/t/1", outcome
    assert f'<meta name="{LAZY_LOAD_MARKER}"' in html and "scrolling failed" in html, html[:300]


def test_render_page_keeps_a_same_document_url_change():
    page = FeedPage(batches=4, navigate_on_scroll=(2, "https://forum.example.test/t/1/page-2", False))
    html, status, outcome, _ = render_navigating(page)
    assert status == 200 and outcome["stopped"] == "stable", outcome
    assert html.count("x") == 5 * 500, "a pushState render lost the scrolled page"
    assert LAZY_LOAD_MARKER not in html, f"a pushState render was stamped incomplete: {html[:300]}"


def test_render_page_returns_a_document_reloaded_at_the_same_url():
    page = FeedPage(batches=4, navigate_on_scroll=(2, "https://forum.example.test/t/1?challenge=1", True))
    html, _, outcome, _ = render_navigating(page)
    assert OTHER_DOCUMENT in html and outcome["stopped"] != "navigated", (outcome, html[:300])
    assert LAZY_LOAD_MARKER not in html, html[:300]
    failed = FeedPage(batches=4, fail_on_scroll=2, navigate_on_scroll=(2, "https://forum.example.test/t/1", True))
    html, _, outcome, _ = render_navigating(failed)
    assert OTHER_DOCUMENT in html and outcome["stopped"] == "error", (outcome, html[:300])
    assert "scrolling failed" in html, html[:300]


def test_mark_incomplete_escapes_a_navigated_to_url():
    html = "<html><head><title>t</title></head><body>b</body></html>"
    marked = mark_incomplete(html, {"stopped": "navigated", "navigated_to": 'https://x.test/"<a>', "growing": False},
                             "t0k")
    assert 'navigated away to https://x.test/&quot;&lt;a&gt; while scrolling' in marked, marked


def web_navigations(context):
    """The navigations a context made anywhere but the engine's own info page."""
    return [url for url in context.goto_urls if url != ENGINE_INFO_URL]


def fetch_with(page):
    fake = FakeBrowser(page)
    with fake_patchright(fake):
        result = run(fetch_browser("https://forum.example.test/t/lazy-thread", lambda target: (True, None),
                                   proxy_url="http://127.0.0.1:8431"))
    return fake, result


def test_high_entropy_hints_request_every_client_hint_the_stock_engine_reports():
    """F6: the UA override must carry every client hint a stock Chrome reports
    for itself. formFactors and the legacy uaFullVersion were never requested
    from the engine, so getHighEntropyValues(['formFactors']) and
    Sec-CH-UA-Form-Factors come back empty under the override where a stock
    Chrome of the same version answers ["Desktop"] — a fingerprint
    inconsistency."""
    for hint in ("formFactors", "uaFullVersion"):
        assert hint in browser.UA_HIGH_ENTROPY_HINTS, (
            f"{hint} is never requested from the engine: {browser.UA_HIGH_ENTROPY_HINTS!r}"
        )


def test_headless_render_overrides_the_ua_and_its_client_hints_together():
    page = FeedPage(batches=0)
    fake, (html, _, error) = fetch_with(page)
    assert error is None and "<body>" in html, error
    probe, render = fake.contexts[0], fake.contexts[-1]
    assert len(fake.contexts) == 2 and probe.closed, [c.options for c in fake.contexts]
    assert probe.goto_urls == [ENGINE_INFO_URL], f"the metadata page went to the web: {probe.goto_urls!r}"
    assert probe.options == CONTEXT_OPTIONS and probe.routes, f"the metadata context: {probe.options!r}"
    ua = stock_user_agent(FakeBrowser.version)
    assert render.options == {**CONTEXT_OPTIONS, "user_agent": ua}, render.options
    assert render.goto_urls == ["https://forum.example.test/t/lazy-thread"], render.goto_urls
    (method, params, gotos_before), = render.cdp_sends
    assert method == "Emulation.setUserAgentOverride" and gotos_before == 0, render.cdp_sends
    assert params["userAgent"] == ua, params
    metadata = params["userAgentMetadata"]
    renamed = lambda entries: [{**e, "brand": "Google Chrome" if e["brand"] == "HeadlessChrome" else e["brand"]}
                               for e in entries]
    assert metadata["brands"] == renamed(HEADLESS_UA_DATA["brands"]), metadata
    assert metadata["fullVersionList"] == renamed(HEADLESS_UA_DATA["fullVersionList"]), metadata
    assert "HeadlessChrome" not in repr(metadata), metadata
    for key in ("mobile", "platform", "architecture", "bitness", "model", "platformVersion", "wow64"):
        assert metadata[key] == HEADLESS_UA_DATA[key], (key, metadata)


def test_headless_render_refuses_unreadable_engine_metadata():
    for ua_data in (UNREADABLE, {"mobile": False, "platform": "Linux"}):
        page = FeedPage(batches=0, ua_data=ua_data)
        stderr = io.StringIO()
        with contextlib.redirect_stderr(stderr):
            fake, (html, status, error) = fetch_with(page)
        assert html == "" and status is None and error and "userAgentData" in error, (error, html[:60])
        assert not any(web_navigations(c) for c in fake.contexts), page.goto_calls
        assert fake.contexts[0].closed, "the metadata context was left open"


class RedirectingPage(FeedPage):
    """A page whose navigation lands elsewhere: a load-time redirect to a
    consent, login or age-gate page, before any scrolling begins."""

    def __init__(self, landing, **kwargs):
        super().__init__(**kwargs)
        self.landing = landing

    async def goto(self, url, **kwargs):
        response = await super().goto(url, **kwargs)
        if url != ENGINE_INFO_URL:
            self.url = self.landing
        return response


def test_handle_fetch_reports_the_address_the_render_landed_on():
    landing = "https://www.reddit.com/over18?dest=https%3A%2F%2Fwww.reddit.com%2Fr%2Fx%2Fcomments%2F1%2F"
    for name, page, want in [
        ("no redirect", FeedPage(batches=0), HANDLED_URL),
        ("a load-time redirect", RedirectingPage(landing, batches=0), landing),
    ]:
        reply = handle_fetch_reply(page, {})
        assert reply.get("ok") and reply.get("final_url") == want, (name, reply.get("final_url"), want)


def test_the_incomplete_stamp_carries_the_request_marker_token():
    reply = handle_fetch_reply(FeedPage(batches=3, fail_on_scroll=1), {"marker_token": "t0k-9f"})
    assert reply.get("ok"), reply
    stamp = f'<meta name="{LAZY_LOAD_MARKER}" content="incomplete: scrolling failed: '
    assert stamp in reply["html"], reply["html"][:300]
    assert 'data-harvester-token="t0k-9f"' in reply["html"], f"the stamp lacks the request token: {reply['html'][:300]}"


if __name__ == "__main__":
    failures = 0
    for name, case in sorted(globals().items()):
        if name.startswith("test_") and callable(case):
            try:
                case()
            except Exception as e:  # noqa: BLE001 — a raising case is a failure, never the end of the run
                print(f"FAIL {name}: {type(e).__name__}: {e}", file=sys.stderr)
                failures += 1
            else:
                print(f"ok {name}")
    sys.exit(1 if failures else 0)
