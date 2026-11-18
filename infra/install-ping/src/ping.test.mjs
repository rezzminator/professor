import assert from "node:assert/strict";
import { test } from "node:test";
import { MAX_BODY_BYTES, buildRow, rowValues, validatePing } from "./ping.mjs";

const VALID = {
  id: "3f9c2a7e-1b4d-4e8a-9c2f-6d1e0b7a5c43",
  product: "pfm",
  version: "0.42.0-alpha",
  os: "darwin",
  arch: "arm64",
  plugins: ["harvester", "flights"],
  event: "daily",
};

const body = (patch) => JSON.stringify({ ...VALID, ...patch });

test("validatePing accepts each contract form and rejects each invalid field with its reason", () => {
  const rows = [
    ["the contract example", body({}), null],
    ["a v-prefixed release", body({ version: "v1.2.3" }), null],
    ["a 32-char suffix", body({ version: `1.2.3-${"a".repeat(32)}` }), null],
    ["a 33-char suffix", body({ version: `1.2.3-${"a".repeat(33)}` }), "version must be [v]MAJOR.MINOR.PATCH with an optional -suffix of 1-32 [0-9A-Za-z.-]"],
    ["harvester with no plugins", body({ product: "harvester", plugins: [] }), null],
    ["32 plugins", body({ plugins: Array.from({ length: 32 }, (_, i) => `p${i}`) }), null],
    ["33 plugins", body({ plugins: Array.from({ length: 33 }, (_, i) => `p${i}`) }), "plugins has more than 32 names"],
    ["a 40-char plugin name", body({ plugins: ["a".repeat(40)] }), null],
    ["a 41-char plugin name", body({ plugins: ["a".repeat(41)] }), "plugins holds a name outside ^[a-z0-9][a-z0-9-]{0,39}$"],
    ["an uppercase plugin name", body({ plugins: ["Flights"] }), "plugins holds a name outside ^[a-z0-9][a-z0-9-]{0,39}$"],
    ["a plugin that is no string", body({ plugins: [7] }), "plugins holds a name outside ^[a-z0-9][a-z0-9-]{0,39}$"],
    ["plugins as a string", body({ plugins: "flights" }), "plugins must be an array"],
    ["harvester with plugins", body({ product: "harvester" }), "plugins must be [] for harvester"],
    ["not JSON", "{", "body is not JSON"],
    ["a JSON array", "[]", "body is not a JSON object"],
    ["JSON null", "null", "body is not a JSON object"],
    ["an extra field", body({ ip: "x" }), 'unknown field "ip"'],
    ["an uppercase id", body({ id: VALID.id.toUpperCase() }), "id must be a lowercase UUIDv4"],
    ["a v1 id", body({ id: "3f9c2a7e-1b4d-1e8a-9c2f-6d1e0b7a5c43" }), "id must be a lowercase UUIDv4"],
    ["a missing id", JSON.stringify({ ...VALID, id: undefined }), "id must be a lowercase UUIDv4"],
    ["another product", body({ product: "codex" }), "product must be pfm or harvester"],
    ["a dev version", body({ version: "dev" }), "version must be [v]MAJOR.MINOR.PATCH with an optional -suffix of 1-32 [0-9A-Za-z.-]"],
    ["another os", body({ os: "plan9" }), "os must be darwin, linux, windows or freebsd"],
    ["a 17-char arch", body({ arch: "a".repeat(17) }), "arch must be 1-16 of [a-z0-9_]"],
    ["a 16-char arch", body({ arch: "a".repeat(16) }), null],
    ["another event", body({ event: "uninstall" }), "event must be install or daily"],
  ];
  for (const [name, text, want] of rows) {
    const got = validatePing(text);
    if (want === null) assert.deepEqual(got, { ping: JSON.parse(text) }, name);
    else assert.deepEqual(got, { error: want }, name);
  }
});

test("validatePing refuses a body one byte over the limit and takes one at it", () => {
  const base = body({});
  const pad = (n) => base.slice(0, -1) + " ".repeat(n - base.length) + "}";
  assert.deepEqual(validatePing(pad(MAX_BODY_BYTES)), { ping: VALID });
  assert.deepEqual(validatePing(pad(MAX_BODY_BYTES + 1)), { error: "body is over 4096 bytes" });
});

test("buildRow keys the row on the UTC day and stores plugins sorted and unique", () => {
  const row = buildRow({ ...VALID, plugins: ["harvester", "flights", "harvester"] }, new Date("2026-11-17T23:59:59.999Z"));
  assert.deepEqual(row, {
    day: "2026-11-17",
    id: VALID.id,
    product: "pfm",
    version: "0.42.0-alpha",
    os: "darwin",
    arch: "arm64",
    plugins: '["flights","harvester"]',
    event: "daily",
  });
  assert.equal(buildRow(VALID, new Date("2026-11-18T00:00:00.000Z")).day, "2026-11-18");
  assert.deepEqual(rowValues(row), ["2026-11-17", VALID.id, "pfm", "0.42.0-alpha", "darwin", "arm64", '["flights","harvester"]', "daily"]);
});
