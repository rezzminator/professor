# install-ping

A Cloudflare Worker and D1 database that count real installs of Professor's `pfm` and of Harvester: unique installs, daily-active installs, versions, OS and the enabled Professor plugins. `pfm` sends its ping from `pfm/internal/installping/`.

- `POST /v1/ping` takes a JSON body of at most 4096 bytes: `id` (a random UUIDv4 made once per install), `product` (`pfm` or `harvester`), `version`, `os`, `arch`, `plugins` (enabled plugins from Professor's own marketplace, `[]` for Harvester) and `event` (`install` or `daily`). It answers `204` stored, `400` with a one-line reason, `404` for another path, `405` for another method.
- `GET /` says what the endpoint stores.
- Code: `src/ping.mjs` validates the body and builds the row, `src/index.mjs` is the fetch handler; plain ES modules, no npm dependency.

## Storage

One row per (UTC day, id, product) in the `pings` table (`migrations/0001_pings.sql`): `day`, `id`, `product`, `version`, `os`, `arch`, `plugins` (a sorted JSON array) and `event`. A second ping from the same install that day updates `version`, `os`, `arch` and `plugins` and keeps `install` if either ping was one.

## Privacy

Stored: the row above and nothing else. Never stored: the IP address, any request header, country, user agent, a time finer than the day, or anything derived from the machine or the user; the id is random and made on the install. `pfm` sends nothing when the machine `pfm.config.json` sets `"installPing": false` (a project's own file cannot set it) or is unreadable. A client sends nothing when `{PRODUCT}_INSTALL_PING_OFFLINE=1` (`PFM_INSTALL_PING_OFFLINE`, `HARVESTER_INSTALL_PING_OFFLINE`) or `DO_NOT_TRACK` is set (any value but `0` or `false`), under CI, or inside the project's test jail and fence.

## Deploy

From this directory, with Cloudflare credentials wrangler can use:

```bash
CLOUDFLARE_ACCOUNT_ID=… npx --yes wrangler@4 d1 migrations apply install-ping --remote
CLOUDFLARE_ACCOUNT_ID=… npx --yes wrangler@4 deploy
```

## Stats

`./stats.sh` (needs `jq`; `/install-stats` runs it from a chat) runs each read-only query in `stats.sql` against the live database and prints it as an aligned table: unique ids per product, release vs development (`-alpha`, `-dev`) builds, daily active ids over 30 days, ids active in the last 7 and 30 days, ids by OS, `pfm` ids per enabled plugin, installs per day, and every install id with its first and last day, version and OS. A new query is a new paragraph in `stats.sql`.

## Tests

`node --test infra/install-ping/src/` (validation, the handler with a fake D1, the upsert and every stats query run against the migration in `node:sqlite`) and `bash infra/install-ping/stats_test.sh`; the gate step `templates.install-ping-tests` runs both.
