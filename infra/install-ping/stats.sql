-- The install-ping stats: one read-only query per paragraph, its first line
-- `-- title`. stats.sh runs each against the live D1; stats.test.mjs runs each
-- against the migration's table.

-- unique install ids ever, per product
SELECT product, COUNT(DISTINCT id) AS ids
FROM pings GROUP BY product ORDER BY product;

-- unique install ids ever, release vs -alpha builds
SELECT product, CASE WHEN instr(version, '-alpha') > 0 THEN 'alpha' ELSE 'release' END AS build,
  COUNT(DISTINCT id) AS ids
FROM pings GROUP BY product, build ORDER BY product, build;

-- daily active ids, last 30 days, per product
SELECT day, product, COUNT(DISTINCT id) AS ids
FROM pings WHERE day >= date('now', '-29 days')
GROUP BY day, product ORDER BY day, product;

-- ids active in the last 7 and 30 days, per product
SELECT product,
  COUNT(DISTINCT CASE WHEN day >= date('now', '-6 days') THEN id END) AS active_7d,
  COUNT(DISTINCT CASE WHEN day >= date('now', '-29 days') THEN id END) AS active_30d
FROM pings GROUP BY product ORDER BY product;

-- ids active in the last 30 days, per product and OS
SELECT product, os, COUNT(DISTINCT id) AS ids
FROM pings WHERE day >= date('now', '-29 days')
GROUP BY product, os ORDER BY product, ids DESC, os;

-- pfm ids active in the last 30 days, per enabled Professor plugin
SELECT plugin.value AS plugin, COUNT(DISTINCT pings.id) AS ids
FROM pings, json_each(pings.plugins) AS plugin
WHERE pings.product = 'pfm' AND pings.day >= date('now', '-29 days')
GROUP BY plugin.value ORDER BY ids DESC, plugin;

-- installs (event=install) per day, per product
SELECT day, product, COUNT(*) AS installs
FROM pings WHERE event = 'install'
GROUP BY day, product ORDER BY day, product;
