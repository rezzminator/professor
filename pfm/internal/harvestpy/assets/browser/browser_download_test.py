"""Download seam test — the file policy's last rung captures a file's bytes,
byte-exact, or ends in a named failure.

The pure cases run with NO browser and NO patchright: the capped copy is
byte-exact under the cap and a named "too-large" failure over it with nothing
left behind, and a download request without the Go-owned proxy launches
nothing.

The live cases (BROWSER_LIVE=1) drive capture_download in a real Chrome
against a local server: a Content-Disposition attachment (the browser's
download event), a direct binary navigation (an inline image: the
navigation's own response body) and a PDF, each byte-exact; an attachment
over the cap is "too-large"; a page that never starts a download is
"no-download", carrying the page it showed. With BROWSER_LIVE=1 a missing
patchright or Chrome is a failure, never a skip.

Run: python3 browser_download_test.py            (pure cases)
     BROWSER_LIVE=1 python3 browser_download_test.py   (pure + live cases)
"""

import asyncio
import http.server
import io
import os
import sys
import tempfile
import threading

import browser
from browser import DownloadFailure, capture_download, copy_capped


def run(coro):
    return asyncio.new_event_loop().run_until_complete(coro)


def test_copy_capped_is_byte_exact_under_the_cap():
    payload = bytes(range(256)) * 300
    dest = os.path.join(tempfile.mkdtemp(prefix="download-seam-"), "file")
    size = copy_capped(io.BytesIO(payload), dest, len(payload), chunk=1000)
    with open(dest, "rb") as handle:
        assert handle.read() == payload and size == len(payload), f"copy was not byte-exact ({size} bytes)"


def test_copy_capped_over_the_cap_is_named_and_leaves_nothing():
    dest = os.path.join(tempfile.mkdtemp(prefix="download-seam-"), "file")
    try:
        copy_capped(io.BytesIO(b"x" * 33), dest, 32, chunk=8)
    except DownloadFailure as failure:
        assert failure.reason == "too-large", f"over-cap copy failed as {failure.reason!r}"
    else:
        raise AssertionError("a copy past the cap was not a named failure")
    assert not os.path.exists(dest), "an over-cap copy left a partial file behind"


def test_a_download_without_the_go_owned_proxy_launches_nothing():
    response = run(browser.handle_download({"op": "download", "url": "https://example.test/f.pdf",
                                            "path": "/nonexistent/f", "max_bytes": 10}))
    assert response == {"ok": False, "error": browser.PROXY_REQUIRED}, f"no-proxy download answered {response}"


# --- live fixtures (BROWSER_LIVE=1) -----------------------------------------

ATTACHMENT = os.urandom(70_000)
PNG = (b"\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89"
       b"\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82")
PDF = b"%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n" + os.urandom(4096)
ROUTES = {
    "/report.bin": ("application/octet-stream", ATTACHMENT, {"Content-Disposition": 'attachment; filename="report.bin"'}),
    "/figure.png": ("image/png", PNG, {}),
    "/paper.pdf": ("application/pdf", PDF, {}),
    "/page.html": ("text/html; charset=utf-8", b"<html><body><p>Welcome, no file here.</p></body></html>", {}),
}


class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):  # noqa: N802 — http.server's own name
        content_type, body, headers = ROUTES.get(self.path, ("text/plain", b"missing", {}))
        self.send_response(200 if self.path in ROUTES else 404)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(body)))
        for name, value in headers.items():
            self.send_header(name, value)
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *args):
        return None


async def capture_all(cases):
    from patchright.async_api import async_playwright  # type: ignore[import-not-found]
    if not browser.chrome_binary():
        raise AssertionError("BROWSER_LIVE=1 but no system Chrome resolves")
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    root = tempfile.mkdtemp(prefix="download-fixtures-")
    results = {}
    try:
        async with async_playwright() as p:
            chrome = await p.chromium.launch(channel="chrome", headless=True)
            try:
                for name, (path, max_bytes) in cases.items():
                    context = await chrome.new_context(accept_downloads=True)
                    dest = os.path.join(root, name)
                    try:
                        url = f"http://127.0.0.1:{server.server_address[1]}{path}"
                        info = await capture_download(context, url, dest, max_bytes, 30_000, grace_ms=1500)
                        with open(dest, "rb") as handle:
                            results[name] = (info, handle.read())
                    except DownloadFailure as failure:
                        results[name] = (failure, os.path.exists(dest))
                    except Exception as e:  # noqa: BLE001 — one case's crash fails that case, not the rest
                        results[name] = (e, os.path.exists(dest))
                    finally:
                        await context.close()
            finally:
                await chrome.close()
    finally:
        server.shutdown()
    return results


LIVE = {}


def live(name):
    if not LIVE:
        LIVE.update(run(capture_all({
            "attachment": ("/report.bin", 1 << 20),
            "inline": ("/figure.png", 1 << 20),
            "pdf": ("/paper.pdf", 1 << 20),
            "over-cap": ("/report.bin", 1000),
            "page": ("/page.html", 1 << 20),
        })))
    return LIVE[name]


def succeeded(name):
    info, body = live(name)
    if isinstance(info, BaseException):
        raise AssertionError(f"{name} raised {info!r}")
    return info, body


def live_test_an_attachment_is_captured_byte_exact():
    info, body = succeeded("attachment")
    assert body == ATTACHMENT, f"attachment bytes differ ({len(body)} of {len(ATTACHMENT)}): {info}"
    assert info["via"] == "download" and info["bytes"] == len(ATTACHMENT), info


def live_test_a_direct_binary_navigation_is_captured_byte_exact():
    info, body = succeeded("inline")
    assert body == PNG, f"inline image bytes differ: {info}"
    assert info["bytes"] == len(PNG) and info["status"] == 200, info


def live_test_a_pdf_is_captured_byte_exact():
    info, body = succeeded("pdf")
    assert body == PDF, f"pdf bytes differ ({len(body)} of {len(PDF)}): {info}"


def live_test_an_attachment_over_the_cap_is_too_large():
    failure, left = live("over-cap")
    assert isinstance(failure, DownloadFailure) and failure.reason == "too-large", f"over-cap answered {failure}"
    assert not left, "an over-cap download left a partial file behind"


def live_test_a_page_that_never_downloads_is_named():
    failure, _ = live("page")
    assert isinstance(failure, DownloadFailure) and failure.reason == "no-download", f"a page answered {failure}"
    assert "Welcome, no file here." in failure.head and failure.status == 200, (failure.status, failure.head)


if __name__ == "__main__":
    prefixes = ("test_", "live_test_") if os.environ.get("BROWSER_LIVE") == "1" else ("test_",)
    failures = 0
    for name, case in sorted(globals().items()):
        if name.startswith(prefixes) and callable(case):
            try:
                case()
                print(f"ok   {name}")
            except Exception as e:  # noqa: BLE001 — every case reports, the run fails at the end
                failures += 1
                print(f"FAIL {name}: {type(e).__name__}: {e}")
    sys.exit(1 if failures else 0)
