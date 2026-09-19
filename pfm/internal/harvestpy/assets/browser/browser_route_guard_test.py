"""Route-guard seam test — runs with NO browser, NO patchright (the seam exists
so CI can pin the R4 CRITICAL: a redirect/subresource ask that Go denies must
ABORT the request, never let Chrome connect).

Run: python3 browser_route_guard_test.py
"""

import asyncio
import sys
import types

from browser import browser_route_guard


# The complete CDP Network.ErrorReason enum Playwright's Route.abort accepts.
CDP_ERROR_REASONS = {
    "aborted", "accessdenied", "addressunreachable", "blockedbyclient",
    "blockedbyresponse", "connectionaborted", "connectionclosed",
    "connectionfailed", "connectionrefused", "connectionreset",
    "internetdisconnected", "namenotresolved", "timedout", "failed",
}


class FakeRoute:
    def __init__(self, url):
        self.request = types.SimpleNamespace(url=url)
        self.abort_reason = None
        self.continued = False

    async def abort(self, reason="failed"):
        # Playwright validates this against the fixed CDP Network.ErrorReason
        # enum and RAISES on anything else. The fake must reject exactly what
        # the real Route rejects — a fake that accepts any string turns this
        # suite into a coincidence detector, which is how an invented code
        # ("blocked", "guard-error") reached the SSRF refusal path unnoticed.
        if reason not in CDP_ERROR_REASONS:
            raise ValueError(f"invalid CDP error reason {reason!r}")
        self.abort_reason = reason

    async def continue_(self):
        self.continued = True


class FakeWebSocket:
    """Stands in for Patchright's WebSocketRoute: connect_to_server() is
    SYNCHRONOUS (no await), close() is async and takes code/reason kwargs."""

    def __init__(self, url):
        self.url = url
        self.connected = False
        self.close_code = None
        self.close_reason = None

    def connect_to_server(self):
        self.connected = True

    async def close(self, *, code=None, reason=None):
        self.close_code = code
        self.close_reason = reason


class FakeContext:
    """Stands in for Patchright's BrowserContext, recording exactly what
    install_route_guards() registers and at what scope/glob."""

    def __init__(self):
        self.routes = []
        self.ws_routes = []

    async def route(self, url, handler):
        self.routes.append((url, handler))

    async def route_web_socket(self, url, handler):
        self.ws_routes.append((url, handler))


def run(coro):
    return asyncio.new_event_loop().run_until_complete(coro)


def test_denied_ask_aborts_before_connecting():
    route = FakeRoute("http://169.254.169.254/latest/meta-data/")

    async def deny(url):
        return False, "refusing private/internal host 169.254.169.254"

    guard = browser_route_guard(deny)
    run(guard(route))
    assert route.abort_reason == "blockedbyclient", (
        f"expected abort('blockedbyclient'), got {route.abort_reason!r}"
    )
    assert not route.continued, "denied route was continued() — Chrome would have connected"


def test_allowed_ask_continues():
    route = FakeRoute("https://example.test/article")

    async def allow(url):
        return True, ""

    guard = browser_route_guard(allow)
    run(guard(route))
    assert route.continued, "allowed route was not continued()"
    assert route.abort_reason is None


def test_raising_ask_aborts_never_continues():
    # B2: the ask channel can die mid-guard (Go closed stdin, malformed
    # reply). A raised handler left Playwright's default in charge — which
    # may CONTINUE the request. The guard must abort on its own.
    route = FakeRoute("http://169.254.169.254/latest/meta-data/")

    async def broken(url):
        raise RuntimeError("go closed the ask channel mid-guard")

    guard = browser_route_guard(broken)
    run(guard(route))
    assert route.abort_reason == "blockedbyclient", (
        f"expected abort('blockedbyclient'), got {route.abort_reason!r}"
    )
    assert not route.continued, "a raising ask must never let the request through"


def test_serialized_ask_never_overlaps():
    # Playwright dispatches route handlers CONCURRENTLY (measured live on
    # patchright 1.62.1). The stdin/stdout ask protocol has no correlation
    # id, so overlapping exchanges could let an ALLOW meant for one URL
    # answer another handler's ask — a fail-open SSRF race.
    from browser import serialized_ask

    state = {"inflight": 0, "max": 0}

    async def slow_ask(url):
        state["inflight"] += 1
        state["max"] = max(state["max"], state["inflight"])
        await asyncio.sleep(0.01)
        state["inflight"] -= 1
        return True, ""

    async def fire_all():
        guarded = serialized_ask(slow_ask)
        await asyncio.gather(*(guarded(f"u{i}") for i in range(5)))

    run(fire_all())
    assert state["max"] == 1, (
        f"asks overlapped (max in-flight {state['max']}) — the protocol raced"
    )


def test_install_route_guards_registers_both_routes_at_context_scope():
    from browser import CONTEXT_OPTIONS, install_route_guards

    context = FakeContext()

    async def allow(url):
        return True, ""

    run(install_route_guards(context, allow))

    assert len(context.routes) == 1, "context.route() must be registered exactly once"
    assert context.routes[0][0] == "**/*", (
        f"context.route() must cover every request, not a narrower glob: {context.routes[0][0]!r}"
    )
    assert len(context.ws_routes) == 1, "context.route_web_socket() must be registered exactly once"
    assert context.ws_routes[0][0] == "**/*", (
        f"route_web_socket() must cover every websocket, not a narrower glob: {context.ws_routes[0][0]!r}"
    )
    assert CONTEXT_OPTIONS.get("service_workers") == "block", (
        "CONTEXT_OPTIONS must block service workers, or SW-originated fetches skip every guard"
    )


def test_websocket_guard_connects_on_allow():
    from browser import install_route_guards

    context = FakeContext()

    async def allow(url):
        return True, ""

    run(install_route_guards(context, allow))
    _, handler = context.ws_routes[0]
    ws = FakeWebSocket("wss://example.test/socket")
    run(handler(ws))
    assert ws.connected, "an allowed websocket must connect_to_server()"
    assert ws.close_code is None, "an allowed websocket must not be closed"


def test_websocket_guard_closes_on_deny():
    from browser import install_route_guards

    context = FakeContext()

    async def deny(url):
        return False, "refusing private/internal host 169.254.169.254"

    run(install_route_guards(context, deny))
    _, handler = context.ws_routes[0]
    ws = FakeWebSocket("ws://169.254.169.254/latest/meta-data/")
    run(handler(ws))
    assert not ws.connected, "a denied websocket must never connect_to_server() — that reaches the real host"
    assert ws.close_code is not None, "a denied websocket must be closed, not left dangling"


def test_websocket_guard_closes_on_raising_ask():
    # B2's WebSocket twin: the ask channel can die mid-guard. A raised
    # handler must not leave the routed socket open for the page to use.
    from browser import install_route_guards

    context = FakeContext()

    async def broken(url):
        raise RuntimeError("go closed the ask channel mid-guard")

    run(install_route_guards(context, broken))
    _, handler = context.ws_routes[0]
    ws = FakeWebSocket("ws://169.254.169.254/latest/meta-data/")
    run(handler(ws))
    assert not ws.connected, "a raising ask must never let the websocket connect_to_server()"
    assert ws.close_code is not None, "a raising ask must close the websocket, never leave it open"


def test_no_proxy_refuses_to_launch():
    # L2-F7: Go pinned exactly ONE host into Chrome's resolver (the initial
    # URL's). Every redirect/subresource/XHR host was re-ASKED, but Chrome
    # then resolved it AGAIN itself — a TTL-0 rebind lands on 127.0.0.1 or
    # 169.254.169.254 while the ask saw a public address. Only a proxy Go owns
    # makes the validated address the dialled one, so with no proxy the rung
    # renders NOTHING.
    from browser import PROXY_REQUIRED, fetch_browser

    html, status, headless, error = run(fetch_browser("https://example.test/", None, proxy_url=None))
    assert error == PROXY_REQUIRED, f"an unproxied fetch was not refused: {error!r}"
    assert html == "" and status is None, f"an unproxied fetch rendered something: {html!r}/{status!r}"
    assert headless is True, f"the refusal lost the requested mode: {headless!r}"


def test_launch_arguments_refuse_an_empty_proxy():
    from browser import launch_arguments

    try:
        launch_arguments("")
    except ValueError:
        return
    raise AssertionError("launch_arguments() composed a launch with no proxy")


def test_launch_arguments_leave_chrome_no_resolver_of_its_own():
    from browser import NO_LOCAL_DNS_RULE, WEBRTC_POLICY_ARG, launch_arguments

    args = launch_arguments("http://127.0.0.1:8431", "MAP publisher.example.test 93.184.216.34")
    rules = [a for a in args if a.startswith("--host-resolver-rules=")]
    assert len(rules) == 1, f"exactly one resolver-rules switch, got {rules!r}"
    value = rules[0].split("=", 1)[1]
    assert value.endswith(NO_LOCAL_DNS_RULE), (
        f"Chrome keeps a resolver of its own — a rebind is still dialled: {value!r}"
    )
    assert value.startswith("MAP publisher.example.test 93.184.216.34"), (
        f"the Go-validated pin was dropped: {value!r}"
    )
    assert "EXCLUDE 127.0.0.1" in value, f"the proxy's own host cannot resolve: {value!r}"
    assert WEBRTC_POLICY_ARG in args, "WebRTC can still dial an ICE candidate past the proxy and the guard"


def test_launch_arguments_keep_a_named_proxy_resolvable():
    from browser import launch_arguments

    value = [
        a for a in launch_arguments("http://proxy.internal.test:3128") if a.startswith("--host-resolver-rules=")
    ][0]
    assert "EXCLUDE proxy.internal.test" in value, f"a named proxy host cannot resolve: {value!r}"


def test_proxy_settings_strip_chromes_loopback_bypass():
    from browser import proxy_settings

    assert proxy_settings(None) is None
    settings = proxy_settings("http://127.0.0.1:8431")
    assert settings["server"] == "http://127.0.0.1:8431"
    assert settings["bypass"] == "<-loopback>", (
        "Chrome's implicit loopback bypass survives — 127.0.0.1 and 169.254.169.254 "
        f"would be dialled DIRECTLY, past the proxy: {settings!r}"
    )


if __name__ == "__main__":
    failures = 0
    for name, case in sorted(globals().items()):
        if name.startswith("test_") and callable(case):
            try:
                case()
            except AssertionError as e:
                print(f"FAIL {name}: {e}", file=sys.stderr)
                failures += 1
            else:
                print(f"ok {name}")
    sys.exit(1 if failures else 0)
