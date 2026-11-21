import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { DatabaseSync } from "node:sqlite";
import { test } from "node:test";

// The stats queries, split as stats.sh splits them: paragraphs, first line the title.
function queries() {
  const text = readFileSync(new URL("../stats.sql", import.meta.url), "utf8");
  return text.split(/\n\s*\n/).flatMap((block) => {
    const lines = block.trim().split("\n");
    const sql = lines.filter((line) => !line.startsWith("--")).join(" ").trim();
    return sql ? [{ title: lines[0].replace(/^-- /, ""), sql }] : [];
  });
}

function day(offset) {
  return new Date(Date.now() - offset * 86400000).toISOString().slice(0, 10);
}

test("each stats query counts the fixture rows as its title says", () => {
  const db = new DatabaseSync(":memory:");
  db.exec(readFileSync(new URL("../migrations/0001_pings.sql", import.meta.url), "utf8"));
  const insert = db.prepare("INSERT INTO pings VALUES (?, ?, ?, ?, ?, ?, ?, ?)");
  const a = "aaaaaaaa-1b4d-4e8a-9c2f-6d1e0b7a5c43";
  const b = "bbbbbbbb-1b4d-4e8a-9c2f-6d1e0b7a5c43";
  const c = "cccccccc-1b4d-4e8a-9c2f-6d1e0b7a5c43";
  insert.run(day(0), a, "pfm", "0.42.0-alpha", "darwin", "arm64", '["flights","harvester"]', "daily");
  insert.run(day(3), a, "pfm", "0.41.0", "darwin", "arm64", '["flights"]', "install");
  insert.run(day(10), b, "pfm", "0.41.0", "linux", "amd64", '["flights"]', "install");
  insert.run(day(40), c, "harvester", "1.0.0", "linux", "amd64", "[]", "install");
  const got = Object.fromEntries(queries().map(({ title, sql }) => [title, db.prepare(sql).all().map((row) => ({ ...row }))]));
  assert.deepEqual(got, {
    "unique install ids ever, per product": [{ product: "harvester", ids: 1 }, { product: "pfm", ids: 2 }],
    "unique install ids ever, release vs -alpha builds": [
      { product: "harvester", build: "release", ids: 1 },
      { product: "pfm", build: "alpha", ids: 1 },
      { product: "pfm", build: "release", ids: 2 },
    ],
    "daily active ids, last 30 days, per product": [
      { day: day(10), product: "pfm", ids: 1 },
      { day: day(3), product: "pfm", ids: 1 },
      { day: day(0), product: "pfm", ids: 1 },
    ],
    "ids active in the last 7 and 30 days, per product": [
      { product: "harvester", active_7d: 0, active_30d: 0 },
      { product: "pfm", active_7d: 1, active_30d: 2 },
    ],
    "ids active in the last 30 days, per product and OS": [
      { product: "pfm", os: "darwin", ids: 1 },
      { product: "pfm", os: "linux", ids: 1 },
    ],
    "pfm ids active in the last 30 days, per enabled Professor plugin": [
      { plugin: "flights", ids: 2 },
      { plugin: "harvester", ids: 1 },
    ],
    "installs (event=install) per day, per product": [
      { day: day(40), product: "harvester", installs: 1 },
      { day: day(10), product: "pfm", installs: 1 },
      { day: day(3), product: "pfm", installs: 1 },
    ],
    "every install id: first and last day seen, days, latest version and OS": [
      { product: "harvester", id: "cccccccc", first_day: day(40), last_day: day(40), days: 1, version: "1.0.0", os: "linux", arch: "amd64" },
      { product: "pfm", id: "aaaaaaaa", first_day: day(3), last_day: day(0), days: 2, version: "0.42.0-alpha", os: "darwin", arch: "arm64" },
      { product: "pfm", id: "bbbbbbbb", first_day: day(10), last_day: day(10), days: 1, version: "0.41.0", os: "linux", arch: "amd64" },
    ],
  });
});
