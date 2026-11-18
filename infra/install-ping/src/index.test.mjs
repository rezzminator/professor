import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { DatabaseSync } from "node:sqlite";
import { test } from "node:test";
import { handle } from "./index.mjs";
import { UPSERT_SQL } from "./ping.mjs";

const MIGRATION = new URL("../migrations/0001_pings.sql", import.meta.url);
const VALID = {
  id: "3f9c2a7e-1b4d-4e8a-9c2f-6d1e0b7a5c43",
  product: "pfm",
  version: "0.42.0-alpha",
  os: "darwin",
  arch: "arm64",
  plugins: ["harvester", "flights"],
  event: "daily",
};
const NOW = new Date("2026-11-17T12:00:00Z");

// recordingD1 is a D1 binding that records each prepared statement and its bound values.
function recordingD1() {
  const calls = [];
  return {
    calls,
    prepare(sql) {
      return { bind: (...values) => ({ run: async () => calls.push({ sql, values }) }) };
    },
  };
}

// sqliteD1 is a D1 binding over an in-memory SQLite holding the real migration.
function sqliteD1() {
  const db = new DatabaseSync(":memory:");
  db.exec(readFileSync(MIGRATION, "utf8"));
  return {
    db,
    prepare(sql) {
      return { bind: (...values) => ({ run: async () => db.prepare(sql).run(...values) }) };
    },
  };
}

const post = (body, headers = { "content-type": "application/json" }, path = "/v1/ping") =>
  new Request(`https://install-ping.example/${path.replace(/^\//, "")}`, { method: "POST", headers, body });

test("a valid ping is stored as 204 with the exact bound row", async () => {
  const d1 = recordingD1();
  const response = await handle(post(JSON.stringify(VALID)), d1, NOW);
  assert.equal(response.status, 204);
  assert.equal(await response.text(), "");
  assert.deepEqual(d1.calls, [
    { sql: UPSERT_SQL, values: ["2026-11-17", VALID.id, "pfm", "0.42.0-alpha", "darwin", "arm64", '["flights","harvester"]', "daily"] },
  ]);
});

test("each refused request answers its status and one-line reason and stores nothing", async () => {
  const rows = [
    ["an invalid field", post(JSON.stringify({ ...VALID, os: "plan9" })), 400, "os must be darwin, linux, windows or freebsd\n"],
    ["a body over the limit", post(JSON.stringify({ ...VALID, pad: "x".repeat(5000) })), 400, "body is over 4096 bytes\n"],
    ["a text body", post(JSON.stringify(VALID), { "content-type": "text/plain" }), 400, "content-type must be application/json\n"],
    ["GET on the ping path", new Request("https://install-ping.example/v1/ping"), 405, "method not allowed\n"],
    ["another path", post(JSON.stringify(VALID), undefined, "/v2/ping"), 404, "not found\n"],
    ["POST on the home page", post("{}", undefined, "/"), 405, "method not allowed\n"],
  ];
  for (const [name, request, status, reason] of rows) {
    const d1 = recordingD1();
    const response = await handle(request, d1, NOW);
    assert.deepEqual([response.status, await response.text(), d1.calls], [status, reason, []], name);
  }
});

test("a charset parameter on the JSON content type is accepted", async () => {
  const d1 = recordingD1();
  const response = await handle(post(JSON.stringify(VALID), { "content-type": "application/json; charset=utf-8" }), d1, NOW);
  assert.deepEqual([response.status, d1.calls.length], [204, 1]);
});

test("GET / says what is stored and that no IP is kept", async () => {
  const response = await handle(new Request("https://install-ping.example/"), recordingD1(), NOW);
  const body = await response.text();
  assert.equal(response.status, 200);
  assert.match(body, /^install-ping: anonymous install counter/);
  assert.match(body, /No IP address, header, country, user agent or time finer than the day is kept\.\n$/);
});

test("a failed upsert answers 500 and logs the failure", async (t) => {
  const logged = t.mock.method(console, "error", () => {});
  const failing = { prepare: () => ({ bind: () => ({ run: async () => { throw new Error("D1_ERROR: disk full"); } }) }) };
  const response = await handle(post(JSON.stringify(VALID)), failing, NOW);
  assert.deepEqual([response.status, await response.text()], [500, "storage failed\n"]);
  assert.deepEqual(logged.mock.calls[0].arguments, [
    "install-ping: upsert failed",
    { day: "2026-11-17", product: "pfm", error: "Error: D1_ERROR: disk full" },
  ]);
});

test("the same id twice in a day keeps one row: the latest fields, install kept, a new day a new row", async () => {
  const d1 = sqliteD1();
  const send = (patch, now = NOW) => handle(post(JSON.stringify({ ...VALID, ...patch })), d1, now);
  assert.equal((await send({ event: "install", plugins: ["flights"] })).status, 204);
  assert.equal((await send({ version: "0.43.0", plugins: ["harvester"], event: "daily" })).status, 204);
  assert.equal((await send({}, new Date("2026-11-18T00:00:00Z"))).status, 204);
  const other = { id: "0a9c2a7e-1b4d-4e8a-8c2f-6d1e0b7a5c43" };
  assert.equal((await send({ ...other, event: "daily" })).status, 204);
  assert.equal((await send({ ...other, event: "install", arch: "amd64" })).status, 204);
  assert.equal((await send({ ...other, event: "daily" })).status, 204);
  const rows = d1.db.prepare("SELECT day, id, version, arch, plugins, event FROM pings ORDER BY day, id").all();
  assert.deepEqual(rows.map((row) => ({ ...row })), [
    { day: "2026-11-17", id: other.id, version: "0.42.0-alpha", arch: "arm64", plugins: '["flights","harvester"]', event: "install" },
    { day: "2026-11-17", id: VALID.id, version: "0.43.0", arch: "arm64", plugins: '["harvester"]', event: "install" },
    { day: "2026-11-18", id: VALID.id, version: "0.42.0-alpha", arch: "arm64", plugins: '["flights","harvester"]', event: "daily" },
  ]);
});

test("a streamed body with no content-length is cut off at the limit, never read whole", async () => {
  let pulled = 0;
  const body = new ReadableStream({
    pull(controller) {
      pulled += 1;
      if (pulled > 1000) return controller.close();
      controller.enqueue(new Uint8Array(1000).fill(0x20));
    },
  });
  const request = new Request("https://install-ping.example/v1/ping", {
    method: "POST", headers: { "content-type": "application/json" }, body, duplex: "half",
  });
  const d1 = recordingD1();
  const response = await handle(request, d1, NOW);
  assert.deepEqual([response.status, await response.text(), d1.calls], [400, "body is over 4096 bytes\n", []]);
  assert.ok(pulled <= 6, `the handler pulled ${pulled} 1000-byte chunks of a 1 MB stream, want at most 6`);
});
