// The install ping's pure half: body validation and the D1 row it becomes.
// index.mjs is the fetch handler that binds UPSERT_SQL with the row.

export const MAX_BODY_BYTES = 4096;
export const MAX_PLUGINS = 32;

const ID = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;
const VERSION = /^v?[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]{1,32})?$/;
const ARCH = /^[a-z0-9_]{1,16}$/;
const PLUGIN = /^[a-z0-9][a-z0-9-]{0,39}$/;
const PRODUCTS = ["pfm", "harvester"];
const OSES = ["darwin", "linux", "windows", "freebsd"];
const EVENTS = ["install", "daily"];
const FIELDS = ["id", "product", "version", "os", "arch", "plugins", "event"];

// Same key again that day: the latest version/os/arch/plugins win, and the
// row stays `install` when either ping was one.
export const UPSERT_SQL = `INSERT INTO pings (day, id, product, version, os, arch, plugins, event)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (day, id, product) DO UPDATE SET
  version = excluded.version,
  os = excluded.os,
  arch = excluded.arch,
  plugins = excluded.plugins,
  event = CASE WHEN pings.event = 'install' OR excluded.event = 'install' THEN 'install' ELSE 'daily' END`;

export const HOME_TEXT = `install-ping: anonymous install counter for Professor's pfm and Harvester.
POST /v1/ping stores one row per UTC day per install: a random install id, the
product, its version, OS, CPU architecture, the enabled Professor plugins and
whether the ping came from an install or a daily run.
No IP address, header, country, user agent or time finer than the day is kept.
`;

// validatePing judges one request body (a string) against the contract and
// returns {ping} or {error}, error being the one-line reason a 400 carries.
export function validatePing(text) {
  if (new TextEncoder().encode(text).length > MAX_BODY_BYTES) {
    return { error: `body is over ${MAX_BODY_BYTES} bytes` };
  }
  let body;
  try {
    body = JSON.parse(text);
  } catch {
    return { error: "body is not JSON" };
  }
  if (body === null || typeof body !== "object" || Array.isArray(body)) {
    return { error: "body is not a JSON object" };
  }
  for (const key of Object.keys(body)) {
    if (!FIELDS.includes(key)) return { error: `unknown field ${JSON.stringify(key).slice(0, 48)}` };
  }
  const { id, product, version, os, arch, plugins, event } = body;
  if (typeof id !== "string" || !ID.test(id)) return { error: "id must be a lowercase UUIDv4" };
  if (!PRODUCTS.includes(product)) return { error: "product must be pfm or harvester" };
  if (typeof version !== "string" || !VERSION.test(version)) {
    return { error: "version must be [v]MAJOR.MINOR.PATCH with an optional -suffix of 1-32 [0-9A-Za-z.-]" };
  }
  if (!OSES.includes(os)) return { error: "os must be darwin, linux, windows or freebsd" };
  if (typeof arch !== "string" || !ARCH.test(arch)) return { error: "arch must be 1-16 of [a-z0-9_]" };
  if (!Array.isArray(plugins)) return { error: "plugins must be an array" };
  if (plugins.length > MAX_PLUGINS) return { error: `plugins has more than ${MAX_PLUGINS} names` };
  for (const name of plugins) {
    if (typeof name !== "string" || !PLUGIN.test(name)) {
      return { error: "plugins holds a name outside ^[a-z0-9][a-z0-9-]{0,39}$" };
    }
  }
  if (product === "harvester" && plugins.length !== 0) return { error: "plugins must be [] for harvester" };
  if (!EVENTS.includes(event)) return { error: "event must be install or daily" };
  return { ping: { id, product, version, os, arch, plugins, event } };
}

// buildRow is the D1 row of a valid ping received at now: the server's UTC
// day and the plugins as a sorted, unique JSON array.
export function buildRow(ping, now) {
  return {
    day: now.toISOString().slice(0, 10),
    id: ping.id,
    product: ping.product,
    version: ping.version,
    os: ping.os,
    arch: ping.arch,
    plugins: JSON.stringify([...new Set(ping.plugins)].sort()),
    event: ping.event,
  };
}

// rowValues is the row in UPSERT_SQL's placeholder order.
export function rowValues(row) {
  return [row.day, row.id, row.product, row.version, row.os, row.arch, row.plugins, row.event];
}
