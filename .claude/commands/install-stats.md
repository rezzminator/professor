---
name: install-stats
description: Reads pfm and Harvester counts — `/install-stats`, or asks like "how many installs", "show the install counter", "who installed Professor". Returns the counts and the saved output's path.
---

# Install stats

Source: the live D1 database `install-ping`, one row per (UTC day, install id, product); `infra/install-ping/README.md` says what a row holds.

1. Check the login: `npx --yes wrangler@4 whoami`. Signed out → step 4, then back here.
2. From the repo root, Bash timeout 300000: `mkdir -p /tmp/professor/install-stats && infra/install-ping/stats.sh > /tmp/professor/install-stats/stats.txt 2>&1; echo "exit $?"`. Exit 1: the file's `stats.sh:` lines name each failed query; report the rest. Exit 2: `stats.sql` was refused; report the refusal line and stop. `You are not authenticated` in the file → step 4.
3. Report from the file:
   - First line: unique installs per product, each split release vs alpha. A version containing `-alpha` is a develop build, so far the owner's own machines; a release version is a real user.
   - The per-install table: every install id with first and last day, days seen, version and OS.
   - 7- and 30-day active counts, installs per day, and the OS and plugin splits only where they add to the above; pfm installs carry their enabled Professor plugins, Harvester's none.
   - Last line: `/tmp/professor/install-stats/stats.txt`.
4. Logging in is the owner's account action; the chat never starts one. Ask the owner to run `npx wrangler login` on this host and paste the `http://localhost:8976/oauth/callback?code=…` URL the browser lands on. Then `ss -ltnp | grep 8976`: nothing listening = the login timed out, the owner runs it again; else `curl -sS -o /dev/null -w '%{http_code}' '{URL}'`, and a 307 followed by `whoami` showing the account = signed in.
