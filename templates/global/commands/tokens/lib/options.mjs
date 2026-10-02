// options.mjs — the command line: every flag, the time window, and the refusals that exit 2.
import os from "node:os";

export const die = (msg) => { console.error("token-audit: " + msg); process.exit(2); };

// The only reader of process.argv: one options object carries every flag and the window.
export function parseOptions(argv = process.argv.slice(2)) {
  const opt = (k, d) => { const i = argv.indexOf(k); return i >= 0 && argv[i + 1] !== undefined ? argv[i + 1] : d; };
  const optAll = (k) => argv.flatMap((a, i) => (a === k && argv[i + 1] ? [argv[i + 1]] : []));
  const sm = /^(\d+(?:\.\d+)?)([hd])$/.exec(opt("--since", "24h"));
  if (!sm) die("--since wants a number plus h or d, e.g. 24h or 3d");
  const hours = sm[2] === "d" ? +sm[1] * 24 : +sm[1], now = Date.now();
  const opts = { now, hours, since: now - hours * 3600e3,
    top: +opt("--top", 12), project: opt("--project", ""), family: opt("--family", ""), session: opt("--session", ""), out: opt("--out", ""), view: opt("--view", ""), briefs: opt("--briefs", ""), famrx: new RegExp(opt("--family-rx", "."), "i"),
    flight: opt("--flight", ""), metricsOut: opt("--metrics-out", ""), codex: argv.includes("--codex"), codexRoot: opt("--codex-root", ""), hostLabel: opt("--host-label", os.hostname()),
    root: optAll("--root"),
    // --timeline FILE: one transcript, whole file, one row per model call. No window applies.
    timeline: optAll("--timeline") };
  opts.tl = opts.timeline.length > 0;
  if (argv.includes("--timeline") && !opts.tl) die("--timeline wants a transcript path");
  if (opts.tl) { if (opts.flight || opts.codex || opts.project || argv.includes("--since")) die("--timeline reads whole files: it takes no --flight, --codex, --project or --since"); opts.since = 0; }
  return opts;
}
