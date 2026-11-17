-- One row per (UTC day, install id, product). Nothing else is kept: no IP, no
-- header, no country, no user agent, no time finer than the day.
CREATE TABLE pings (
  day     TEXT NOT NULL, -- YYYY-MM-DD, the server's UTC date
  id      TEXT NOT NULL, -- the install's random UUIDv4
  product TEXT NOT NULL, -- pfm | harvester
  version TEXT NOT NULL,
  os      TEXT NOT NULL,
  arch    TEXT NOT NULL,
  plugins TEXT NOT NULL, -- JSON array, sorted, unique
  event   TEXT NOT NULL, -- install | daily; install wins within a day
  PRIMARY KEY (day, id, product)
);
