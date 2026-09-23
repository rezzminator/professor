"""Hash-route seam test — the browser renders a hash route's own view, never
the app's home view or its empty shell.

The pure cases run with NO browser and NO patchright: settle_route waits for
the page's text to leave the shell's and hold still, and gives up at its cap.

The live case (BROWSER_LIVE=1) renders a local app shell in a real Chrome
through render_page at its #/quickstart address: the router writes the
route's view only after a delay longer than a scroll takes to go stable.
With BROWSER_LIVE=1 a missing patchright or Chrome is a failure, never a skip.

Run: python3 browser_route_test.py            (pure cases)
     BROWSER_LIVE=1 python3 browser_route_test.py   (pure + live cases)
"""

import os
import sys
import tempfile

import browser
from browser_consent_test import run, serve

# The route's view arrives after ROUTE_DELAY_MS; the home view (no route) at once.
ROUTE_DELAY_MS = 8000
APP_SHELL = """<!doctype html><html><head><title>docs</title></head><body><div id="app"></div>
<script>
// The markers are split so the stored HTML holds them only once the router wrote them.
const views = {"#/quickstart": "<h1>Quick start</h1><p>ROUTE-" + "VIEW-QUICKSTART install the tool and preview.</p>"};
const home = "<h1>Home</h1><p>HOME-" + "VIEW the project's home README.</p>";
const view = views[location.hash];
if (view) {
  setTimeout(() => { document.getElementById("app").innerHTML = view; }, %d);
} else {
  document.getElementById("app").innerHTML = home;
}
</script></body></html>""" % ROUTE_DELAY_MS


def stepping_clock():
    now = [0.0]

    def clock():
        return now[0]

    async def pause():
        now[0] += 0.25

    return clock, pause


def test_is_hash_route_names_only_router_fragments():
    for url in ("https://docs.example.test/#/quickstart", "https://docs.example.test/#!/guide"):
        assert browser.is_hash_route(url), url
    for url in ("https://docs.example.test/guide#install", "https://docs.example.test/", "https://docs.example.test/#"):
        assert not browser.is_hash_route(url), url


def test_settle_route_waits_for_the_view_to_leave_the_shell_and_hold():
    clock, pause = stepping_clock()
    texts = iter(["", "", "", "", "Quick start", "Quick start install"] + ["Quick start install"] * 40)

    async def text():
        return next(texts)

    outcome = run(browser.settle_route(text, pause, clock, ""))
    assert outcome == "settled", outcome
    assert clock() >= 1.5, f"settled before the view held still: t={clock()}"


def test_settle_route_gives_up_at_its_cap():
    clock, pause = stepping_clock()

    async def text():
        return ""

    assert run(browser.settle_route(text, pause, clock, "")) == "timeout"
    assert clock() >= browser.ROUTE_SETTLE_MAX_SECONDS


async def render_route():
    from patchright.async_api import async_playwright  # type: ignore[import-not-found]
    if not browser.chrome_binary():
        raise AssertionError("BROWSER_LIVE=1 but no system Chrome resolves")
    root = tempfile.mkdtemp(prefix="route-fixtures-")
    with open(os.path.join(root, "index.html"), "w", encoding="utf-8") as handle:
        handle.write(APP_SHELL)
    server = serve(root)
    try:
        async with async_playwright() as p:
            chrome = await p.chromium.launch(channel="chrome", headless=True)
            try:
                context = await chrome.new_context()
                try:
                    return await render_page(context, server.server_address[1])
                finally:
                    await context.close()
            finally:
                await chrome.close()
    finally:
        server.shutdown()


async def render_page(context, port):
    return await browser.render_page(context, f"http://127.0.0.1:{port}/#/quickstart", 30_000, marker_token="tok")


def live_test_a_hash_route_renders_the_routes_view():
    html, _, outcome = run(render_route())
    assert "ROUTE-VIEW-QUICKSTART" in html, f"the #/quickstart view was not rendered: {outcome}; {html[:300]!r}"
    assert "HOME-VIEW" not in html, f"the home view was rendered for the route: {outcome}"


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
