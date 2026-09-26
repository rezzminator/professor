#!/usr/bin/env node

// release-check — the release family's deterministic gate: whatever a release
// decides that can be computed, computed here (docs/design/release/release-check.md).
//   scope --base REF --head REF         the range's hunks, tiers and review areas
//   notes FILE [groups…]                the note grammar N1–N8; coverage C1–C5 with
//                                       --base/--head/--coverage; version V1–V3 with
//                                       --version/--previous/--bump; P1 with --pfm-help
//   notes --all DIR                     the grammar of every v*.md at or above v0.78.0
//   ready DIR --worktree PATH [--stamp] R1–R6: every release verdict holds at HEAD
// Exit 0 ends on `release-check {mode}: clean`; exit 1 prints one
// `FAIL {rule}: {where} — {what}` per failure (ready adds `RERUN {phase}` lines).
// When the check itself is broken — bad usage, a missing input file, a git
// command that failed — it exits 2 with `ERROR {what}` naming the input, or the
// git command and its stderr: never a FAIL, never clean.

import { execFileSync } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  statSync,
  writeFileSync,
} from "node:fs";
import { dirname, join, resolve } from "node:path";

class CheckError extends Error {}

const USAGE = [
  "usage: release-check.mjs scope --base REF --head REF",
  "       release-check.mjs notes FILE [--base REF --head REF --coverage TSV] [--version [v]X.Y.Z [--previous [v]X.Y.Z --bump patch|minor|major]] [--pfm-help FILE] [--record FILE]",
  "       release-check.mjs notes --all DIR",
  "       release-check.mjs ready DIR --worktree PATH [--stamp]",
].join("\n");

const HUNK_CAP = 600; // one reviewer run's capacity: an area over it splits
const GRAMMAR_FIRST = "0.78.0"; // notes --all: the first release written to this grammar

// The tier table. Tier 1 is the adopter contract, the only paths a release
// reviews in full; everything else is tier 2. An entry is a directory (ends
// in /), an exact file, the named `subdirs` of `dir`, or the files directly
// in `dir` whose name starts with one of `starts`. An area over the hunk cap
// splits by the path segment after its entry: a directory's child, a
// subdir's own name. release-check.md § The tier table is its one description.
const TIER1 = [
  {
    area: "templates",
    paths: ["templates/", "workflows/", "pfm/harness-prompts/"],
  },
  {
    area: "pfm-update",
    paths: [
      {
        dir: "pfm/internal/",
        subdirs: [
          "update",
          "updatecheck",
          "installer",
          "doctor",
          "config",
          "professor",
          "codexgen",
          "opencodegen",
          "picker",
          "mcpserv",
          "harvestmcp",
        ],
      },
      {
        dir: "pfm/cmd/pfm/",
        starts: ["install", "uninstall", "update", "init", "doctor", "config", "mcp_serve"],
      },
    ],
  },
  {
    area: "public",
    paths: [
      "README.md",
      "INSTALL.md",
      "CHANGELOG.md",
      "docs/SETUP.md",
      "docs/RELEASE.md",
      "docs/BLUEPRINT.md",
      "docs/PLACEHOLDERS.md",
    ],
  },
  {
    area: "gates",
    paths: [
      ".github/",
      ".githooks/",
      "scripts/",
      "infra/fence/release-rehearsal.sh",
      "infra/check-self-hosted-manifest.sh",
    ],
  },
];

// The files a release commit itself writes; the gate's verdict carries over them.
const RELEASE_FILES = [
  "CHANGELOG.md",
  "VERSION",
  ".professor/VERSION",
  ".professor/manifest.json",
];
const isReleaseFile = (path) =>
  path.startsWith("releases/") || RELEASE_FILES.includes(path);

const ROLE_ROOTS = ["templates/global/", "templates/project/", ".claude/"];
const SECTIONS = [
  "Breaking",
  "Migration",
  "Added",
  "Changed",
  "Fixed",
  "Removed",
  "Verification",
];
const LABELS = ["Global", "Project", "pfm", "Repo"];
const TIMINGS = ["before update", "after update", "per project"];
const BUMPS = ["patch", "minor", "major"];
const PHASES = ["REVIEW", "NOTES", "GATE", "REHEARSE", "READY"];
const LANES = ["templates", "pfm", "e2e"];
const MACHINES = ["main", "behind"];

// ---------------------------------------------------------------- plumbing

// Inside the dev fence a linked worktree's gitdir is not at its host path:
// dev.sh iso hands it in as PFM_DEV_REPO_GIT_DIR + PFM_DEV_REPO_WORK_TREE (the
// contract scripts/leak-check.sh honors too). Applied only to a cwd inside
// that worktree, so a repository elsewhere is never redirected.
function gitPrefix(cwd) {
  const gitDir = process.env.PFM_DEV_REPO_GIT_DIR;
  const tree = process.env.PFM_DEV_REPO_WORK_TREE;
  if (!gitDir && !tree) return [];
  if (!gitDir || !tree)
    throw new CheckError("PFM_DEV_REPO_GIT_DIR and PFM_DEV_REPO_WORK_TREE must be set together");
  const root = resolve(tree);
  const here = resolve(cwd);
  if (here !== root && !here.startsWith(`${root}/`)) return [];
  return [`--git-dir=${gitDir}`, `--work-tree=${root}`, "-c", `safe.directory=${root}`];
}

function git(cwd, args) {
  try {
    return execFileSync("git", [...gitPrefix(cwd), "-c", "core.quotePath=false", ...args], {
      cwd,
      encoding: "utf8",
      maxBuffer: 1 << 30,
      stdio: ["ignore", "pipe", "pipe"],
    });
  } catch (err) {
    const stderr = String(err.stderr ?? "").trim();
    throw new CheckError(
      `git ${args.join(" ")} (in ${cwd}) failed: ${stderr || err.message}`,
    );
  }
}

const nulSplit = (out) => out.split("\0").filter((s) => s !== "");

function resolveCommit(cwd, ref, flag) {
  if (!ref || ref.startsWith("-")) throw new CheckError(`usage: ${flag} ${ref ?? ""} is not a ref`);
  return git(cwd, ["rev-parse", "--verify", `${ref}^{commit}`]).trim();
}

// A sha written into a verdict file: the commit it names, or null when the
// worktree holds no such commit (a verdict defect, judged by the caller).
function lookupCommit(cwd, sha) {
  try {
    return execFileSync(
      "git",
      [...gitPrefix(cwd), "rev-parse", "--verify", "--quiet", `${sha}^{commit}`],
      { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "pipe"] },
    ).trim();
  } catch (err) {
    if (err.status === 1 && !String(err.stderr ?? "").trim()) return null;
    throw new CheckError(
      `git rev-parse --verify --quiet ${sha}^{commit} (in ${cwd}) failed: ${String(err.stderr ?? "").trim() || err.message}`,
    );
  }
}

// A git yes/no question: exit 0 is yes, exit 1 with no stderr is no; any
// other exit is a git failure, never a no.
function gitYes(cwd, args) {
  try {
    execFileSync("git", [...gitPrefix(cwd), ...args], { cwd, stdio: ["ignore", "pipe", "pipe"] });
    return true;
  } catch (err) {
    if (err.status === 1 && !String(err.stderr ?? "").trim()) return false;
    throw new CheckError(
      `git ${args.join(" ")} (in ${cwd}) failed: ${String(err.stderr ?? "").trim() || err.message}`,
    );
  }
}

// A range runs forward: swapped or unrelated refs would read additions as
// removals and an empty ledger as complete.
function requireAncestor(cwd, base, head, o) {
  if (!gitYes(cwd, ["merge-base", "--is-ancestor", base, head]))
    throw new CheckError(`--base ${o.base} is not an ancestor of --head ${o.head}`);
}

const reEscape = (s) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

function readInput(path, what) {
  if (!existsSync(path)) throw new CheckError(`${what} ${path} does not exist`);
  try {
    return readFileSync(path, "utf8");
  } catch (err) {
    throw new CheckError(`${what} ${path} could not be read: ${err.message}`);
  }
}

// A verdict file inside the release directory: absent is null (the phase has
// not run — the rule fails), unreadable is an error.
function readVerdict(path) {
  if (!existsSync(path)) return null;
  try {
    return readFileSync(path, "utf8");
  } catch (err) {
    throw new CheckError(`${path} could not be read: ${err.message}`);
  }
}

const textLines = (text) => {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  if (lines.length && lines[lines.length - 1] === "") lines.pop();
  return lines;
};

const semver = (value, flag) => {
  const m = /^v?(\d+)\.(\d+)\.(\d+)$/.exec(value ?? "");
  if (!m) throw new CheckError(`usage: ${flag} ${value} is not X.Y.Z`);
  return { str: `${m[1]}.${m[2]}.${m[3]}`, parts: [+m[1], +m[2], +m[3]] };
};

const cmpVersion = (a, b) =>
  a[0] - b[0] || a[1] - b[1] || a[2] - b[2];

const fail = (rule, where, what) => ({ rule, where, what });

const today = () => {
  const d = new Date();
  const pad = (n) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
};

// ---------------------------------------------------------------- git range

function revList(cwd, base, head) {
  return git(cwd, ["rev-list", "--no-merges", `${base}..${head}`])
    .split("\n")
    .filter(Boolean);
}

// [{status: A|M|D|R, path, oldPath?}] — a rename is its new path.
function nameStatus(cwd, base, head) {
  const tokens = nulSplit(
    git(cwd, ["diff", "--name-status", "-z", "-M", "--no-relative", base, head]),
  );
  const changes = [];
  for (let i = 0; i < tokens.length; ) {
    const status = tokens[i++];
    const letter = status[0];
    if (letter === "R" || letter === "C") {
      const oldPath = tokens[i++];
      const path = tokens[i++];
      if (letter === "C")
        changes.push({ status: "A", path });
      else changes.push({ status: "R", path, oldPath });
    } else if ("AMD".includes(letter)) {
      changes.push({ status: letter, path: tokens[i++] });
    } else if (letter === "T") {
      changes.push({ status: "M", path: tokens[i++] });
    } else {
      throw new CheckError(
        `git diff --name-status ${base} ${head}: unclassifiable status ${status} for ${tokens[i]}`,
      );
    }
  }
  return changes;
}

function cUnquote(quoted) {
  const chars = Array.from(quoted.slice(1, -1));
  const bytes = [];
  const escapes = { n: 10, t: 9, r: 13, a: 7, b: 8, f: 12, v: 11 };
  for (let i = 0; i < chars.length; i++) {
    const c = chars[i];
    if (c !== "\\") {
      bytes.push(...Buffer.from(c, "utf8"));
      continue;
    }
    const n = chars[++i];
    if (/[0-7]/.test(n)) {
      bytes.push(parseInt(chars.slice(i, i + 3).join(""), 8));
      i += 2;
    } else bytes.push(escapes[n] ?? n.charCodeAt(0));
  }
  return Buffer.from(bytes).toString("utf8");
}

function headerPath(raw, prefix) {
  let s = raw.replace(/\t$/, "");
  if (s === "/dev/null") return null;
  if (s.startsWith('"')) s = cUnquote(s);
  if (!s.startsWith(prefix))
    throw new CheckError(`diff header path ${raw} lacks its ${prefix} prefix`);
  return s.slice(prefix.length);
}

const DIFF_U0 = (base, head) => [
  "diff",
  "-U0",
  "--no-color",
  "--no-ext-diff",
  "--no-relative",
  "--src-prefix=a/",
  "--dst-prefix=b/",
  "-M",
  base,
  head,
];

// Hunks per file (a rename under its new path): the `@@` lines of `git diff -U0`.
function hunkCounts(cwd, base, head) {
  const counts = new Map();
  let entry = null;
  let inHeader = false;
  const close = () => {
    if (!entry || !entry.hunks) return;
    const path = entry.newPath ?? entry.oldPath;
    if (!path)
      throw new CheckError(
        `git diff -U0 ${base} ${head}: a file with ${entry.hunks} hunks has no path in its header`,
      );
    counts.set(path, (counts.get(path) ?? 0) + entry.hunks);
  };
  for (const line of git(cwd, DIFF_U0(base, head)).split("\n")) {
    if (line.startsWith("diff --git ")) {
      close();
      entry = { oldPath: null, newPath: null, hunks: 0 };
      inHeader = true;
    } else if (inHeader && line.startsWith("--- ")) {
      entry.oldPath = headerPath(line.slice(4), "a/");
    } else if (inHeader && line.startsWith("+++ ")) {
      entry.newPath = headerPath(line.slice(4), "b/");
    } else if (entry && line.startsWith("@@")) {
      inHeader = false;
      entry.hunks++;
    }
  }
  close();
  return counts;
}

const treeCache = new Map();
function treeHas(cwd, ref, prefix) {
  const key = `${cwd}\0${ref}\0${prefix}`;
  if (!treeCache.has(key))
    treeCache.set(
      key,
      nulSplit(git(cwd, ["ls-tree", "-r", "--name-only", "-z", ref, "--", prefix]))
        .length > 0,
    );
  return treeCache.get(key);
}

// The agent, command or skill a path under a role root belongs to; `rel` is
// where that role lives under any root.
function roleOf(path) {
  for (const root of ROLE_ROOTS) {
    if (!path.startsWith(root)) continue;
    const rest = path.slice(root.length);
    let m;
    if ((m = /^agents\/([^/]+)\.md$/.exec(rest)))
      return { kind: "agent", name: m[1], rel: `agents/${m[1]}.md` };
    if ((m = /^commands\/([^/]+)\/([^/]+)\.md$/.exec(rest)))
      return { kind: "command", name: `/${m[1]}:${m[2]}`, rel: `commands/${m[1]}/${m[2]}.md` };
    if ((m = /^commands\/([^/]+)\.md$/.exec(rest)))
      return { kind: "command", name: `/${m[1]}`, rel: `commands/${m[1]}.md` };
    if ((m = /^skills\/([^/]+)\/./.exec(rest)))
      return { kind: "skill", name: m[1], rel: `skills/${m[1]}/` };
  }
  return null;
}

// Added and removed roles, deduplicated by kind and name. A role is removed
// only when no root holds it at head (a move between roots removes nothing),
// added only when no root held it at base; a skill is held while any file is
// left under its directory.
function roleChanges(cwd, base, head, changes) {
  const added = new Map();
  const removed = new Map();
  const held = (ref, r) => ROLE_ROOTS.some((root) => treeHas(cwd, ref, `${root}${r.rel}`));
  for (const c of changes) {
    const gone = c.status === "D" ? [c.path] : c.status === "R" ? [c.oldPath] : [];
    const born = c.status === "A" || c.status === "R" ? [c.path] : [];
    for (const p of gone) {
      const r = roleOf(p);
      if (r && !held(head, r)) removed.set(`${r.kind} ${r.name}`, r);
    }
    for (const p of born) {
      const r = roleOf(p);
      if (r && !held(base, r)) added.set(`${r.kind} ${r.name}`, r);
    }
  }
  const sorted = (m) => [...m.keys()].sort().map((k) => m.get(k));
  return { added: sorted(added), removed: sorted(removed) };
}

// ---------------------------------------------------------------- tiers

function tierOf(path) {
  for (const { area, paths } of TIER1) {
    for (const entry of paths) {
      if (typeof entry === "object") {
        if (!path.startsWith(entry.dir)) continue;
        const name = path.slice(entry.dir.length);
        const sub = name.split("/")[0];
        if (entry.subdirs?.includes(sub) && name.includes("/"))
          return { area, group: `${entry.dir}${sub}/` };
        if (entry.starts && !name.includes("/") && entry.starts.some((s) => name.startsWith(s)))
          return { area, group: path };
      } else if (entry.endsWith("/")) {
        if (!path.startsWith(entry)) continue;
        const rest = path.slice(entry.length);
        return { area, group: entry + rest.split("/")[0] + (rest.includes("/") ? "/" : "") };
      } else if (path === entry) {
        return { area, group: path };
      }
    }
  }
  return null;
}

const areaPathspecs = (area) =>
  TIER1.find((a) => a.area === area).paths.flatMap((e) =>
    typeof e !== "object"
      ? [e]
      : e.subdirs
        ? e.subdirs.map((d) => `${e.dir}${d}/`)
        : e.starts.map((s) => `:(glob)${e.dir}${s}*`),
  );

const sumHunks = (files) => files.reduce((n, f) => n + f.hunks, 0);

// Tier-1 files into review areas of at most HUNK_CAP hunks: an area over the
// cap splits by its next path segment, packing segments in path order; one
// segment over the cap splits by file the same way.
function buildAreas(files) {
  const areas = [];
  const push = (a) => {
    for (const f of a.members) {
      f.area = a.name;
      // A file filed by its old path: both of its paths join the pathspecs, so
      // the reviewer's diff shows the move out of the contract.
      if (f.fromOld) for (const p of [f.oldPath, f.path]) if (!a.specs.includes(p)) a.specs.push(p);
    }
    areas.push(a);
  };
  for (const { area } of TIER1) {
    const mine = files.filter((f) => f.tier?.area === area);
    if (!mine.length) continue;
    const total = sumHunks(mine);
    if (total <= HUNK_CAP) {
      push({ name: area, hunks: total, files: mine.length, specs: areaPathspecs(area), members: mine });
      continue;
    }
    let n = 0; // a split area's parts are {area}-1, {area}-2, … in path order
    const groups = new Map();
    for (const f of mine) {
      const g = groups.get(f.tier.group) ?? { files: [] };
      g.files.push(f);
      groups.set(f.tier.group, g);
    }
    let pack = null;
    const flush = () => {
      if (pack) push(pack);
      pack = null;
    };
    for (const key of [...groups.keys()].sort()) {
      const g = groups.get(key);
      const hunks = sumHunks(g.files);
      if (hunks > HUNK_CAP && g.files.length > 1) {
        flush();
        let filePack = null;
        for (const f of [...g.files].sort((a, b) => (a.path < b.path ? -1 : 1))) {
          if (filePack && filePack.hunks + f.hunks > HUNK_CAP) {
            push(filePack);
            filePack = null;
          }
          filePack ??= {
            name: `${area}-${++n}`,
            hunks: 0,
            files: 0,
            specs: [],
            members: [],
          };
          filePack.hunks += f.hunks;
          filePack.files++;
          filePack.specs.push(f.path);
          filePack.members.push(f);
        }
        push(filePack);
        continue;
      }
      if (pack && pack.hunks + hunks > HUNK_CAP) flush();
      pack ??= { name: `${area}-${++n}`, hunks: 0, files: 0, specs: [], members: [] };
      pack.hunks += hunks;
      pack.files += g.files.length;
      pack.specs.push(key);
      pack.members.push(...g.files);
    }
    flush();
  }
  return areas;
}

// ---------------------------------------------------------------- scope

function scope(o) {
  if (o._.length) throw new CheckError(`usage: scope takes no positional argument (${o._.join(" ")})`);
  if (!o.base || !o.head) throw new CheckError("usage: scope needs --base and --head");
  const cwd = process.cwd();
  const base = resolveCommit(cwd, o.base, "--base");
  const head = resolveCommit(cwd, o.head, "--head");
  requireAncestor(cwd, base, head, o);
  const commits = revList(cwd, base, head);
  const changes = nameStatus(cwd, base, head);
  const hunks = hunkCounts(cwd, base, head);
  const known = new Set(changes.map((c) => c.path));
  for (const path of hunks.keys())
    if (!known.has(path))
      throw new CheckError(
        `git diff -U0 counted hunks for ${path}, which git diff --name-status ${base} ${head} does not list`,
      );
  // A rename out of tier 1 keeps its old path's tier and area: moving a file
  // out of the contract is still reviewed.
  const files = changes
    .map((c) => {
      const own = tierOf(c.path);
      const tier = own ?? (c.oldPath ? tierOf(c.oldPath) : null);
      return { ...c, hunks: hunks.get(c.path) ?? 0, tier, fromOld: !own && !!tier };
    })
    .sort((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0));
  const areas = buildAreas(files);
  const tier2 = files.filter((f) => !f.tier);
  const removed = files
    .flatMap((f) => (f.status === "D" ? [f.path] : f.status === "R" ? [f.oldPath] : []))
    .sort();
  const roles = roleChanges(cwd, base, head, changes).removed;
  const lines = [
    `RANGE ${base.slice(0, 7)}..${head.slice(0, 7)} COMMITS ${commits.length} FILES ${files.length} HUNKS ${sumHunks(files)}`,
    ...files.map(
      (f) =>
        `FILE ${f.status} ${f.hunks} ${f.tier ? "tier1" : "tier2"} ${f.area ?? "-"} ${f.path}`,
    ),
    ...areas.map((a) => `AREA ${a.name} ${a.hunks} ${a.files} ${a.specs.join(" ")}`),
    `TIER2 ${sumHunks(tier2)} ${tier2.length}`,
    ...removed.map((p) => `REMOVED ${p}`),
    ...roles.map((r) => `REMOVED-ROLE ${r.kind} ${r.name}`),
  ];
  return report("scope", [], lines);
}

// ---------------------------------------------------------------- notes

// The note grammar, N1–N8, and what the other note rules read from it.
function parseNote(text, where, expectVersion, expectFrom = `--version ${expectVersion}`) {
  const fails = [];
  const lines = textLines(text);
  const at = (n) => `${where}:${n}`;
  const title = /^# v(\d+\.\d+\.\d+) — (\d{4}-\d{2}-\d{2})$/.exec(lines[0] ?? "");
  if (!title)
    fails.push(fail("N1", at(1), `line 1 is not \`# v{X.Y.Z} — {YYYY-MM-DD}\`: ${JSON.stringify(lines[0] ?? "")}`));
  else if (expectVersion && title[1] !== expectVersion)
    fails.push(fail("N1", at(1), `title version v${title[1]} differs from ${expectFrom}`));

  const sections = new Map(); // valid section name -> {line, bullets: [{key, line}]}
  const items = []; // bullet and action lines: {text, line, kind}
  let region = "head"; // head | section
  let leadProse = false;
  let leadChecked = false;
  let stopSeen = false;
  let current = null; // {name, line, kind: category|verification|unknown, content}
  let lastOrder = -1;
  let prev = null; // last bullet/action kind under the current category
  let inFence = false;
  let fenceLine = 0; // where the open fence opened: one never closed hides the rest of the note

  const checkLead = (n) => {
    if (leadChecked) return;
    leadChecked = true;
    if (!leadProse)
      fails.push(fail("N2", at(n), "no prose line stands between the title and the first `##` or `#### → Stop:`"));
  };
  const closeSection = () => {
    if (current && current.content === 0)
      fails.push(fail("N4", at(current.line), `\`## ${current.name}\` heads no content`));
  };

  for (let i = 1; i < lines.length; i++) {
    const line = lines[i];
    const n = i + 1;
    let kind;
    if (inFence || /^(```|~~~)/.test(line)) {
      if (/^(```|~~~)/.test(line)) {
        inFence = !inFence;
        fenceLine = n;
      }
      kind = "prose";
    } else if (line.trim() === "") kind = "blank";
    else {
      const h = /^(#{1,6})(?: |$)/.exec(line);
      if (h) {
        const level = h[1].length;
        if (level === 2) kind = "h2";
        else if (level === 4 && line.startsWith("#### → Stop:")) kind = "stop";
        else if (level === 4 && line.startsWith("#### → For:")) kind = "action";
        else kind = "badheading";
      } else if (/^#{1,6}[^#\s]/.test(line)) kind = "nospace";
      else if (/^[-*+] /.test(line)) kind = "bullet";
      else kind = "prose";
    }
    if (kind === "blank") continue;
    if (current) current.content += kind === "h2" ? 0 : 1;

    if (kind === "badheading") {
      fails.push(fail("N8", at(n), `heading or \`####\` line outside the grammar: ${line}`));
      continue;
    }
    // `##Added` is no heading to a renderer: its section would read as prose.
    if (kind === "nospace") {
      fails.push(fail("N8", at(n), `a line opens with \`#\` but lacks the space after its hashes: ${line}`));
      continue;
    }
    if (kind === "stop") {
      const reason = line.slice("#### → Stop:".length).trim();
      if (!reason) fails.push(fail("N3", at(n), "`#### → Stop:` line has no reason"));
      if (region === "section")
        fails.push(fail("N3", at(n), "`#### → Stop:` line sits after the first `##`; it belongs between the lead and the first `##`"));
      else {
        checkLead(n);
        stopSeen = true;
      }
      continue;
    }
    if (kind === "action") {
      const m = /^#### → For: (.*?) · (.*?) · (.*?) — (.*)$/.exec(line);
      const ok =
        m &&
        m[1].trim() &&
        TIMINGS.includes(m[2]) &&
        m[3].trim() &&
        m[4].trim();
      if (region === "section" && current.kind === "verification") {
        fails.push(fail("N7", at(n), "`## Verification` holds an action line"));
        continue;
      }
      if (!ok)
        fails.push(fail("N6", at(n), "action line does not read `#### → For: {audience} · {before update|after update|per project} · {surface} — {action}` with every part non-empty"));
      if (region === "head" || current.kind !== "category")
        fails.push(fail("N5", at(n), "action line outside a category section"));
      else if (!prev)
        fails.push(fail("N5", at(n), "action line before any bullet of its section"));
      items.push({ text: line, line: n, kind: "action" });
      if (region === "section") prev = "action";
      continue;
    }
    if (kind === "h2") {
      if (region === "head") checkLead(n);
      closeSection();
      region = "section";
      prev = null;
      const name = line.slice(3).trim();
      const order = SECTIONS.indexOf(name);
      current = { name, line: n, content: 0, kind: "unknown" };
      if (order < 0) {
        fails.push(fail("N4", at(n), `\`## ${name}\` is not one of ${SECTIONS.join(", ")}`));
        continue;
      }
      if (sections.has(name)) {
        fails.push(fail("N4", at(n), `\`## ${name}\` repeats (first at line ${sections.get(name).line})`));
      } else if (order < lastOrder) {
        fails.push(fail("N4", at(n), `\`## ${name}\` breaks the order ${SECTIONS.join(", ")}`));
      }
      lastOrder = Math.max(lastOrder, order);
      current.kind = name === "Verification" ? "verification" : "category";
      if (!sections.has(name)) sections.set(name, { line: n, bullets: [] });
      continue;
    }
    // bullet or prose
    if (region === "head") {
      if (stopSeen)
        fails.push(fail("N3", at(n), "a lead line follows a `#### → Stop:` line; Stop lines sit between the lead and the first `##`"));
      else leadProse = true;
      continue;
    }
    if (current.kind === "verification") {
      if (kind === "bullet") fails.push(fail("N7", at(n), "`## Verification` holds a bullet"));
      continue;
    }
    if (current.kind !== "category") continue;
    if (kind === "bullet") {
      const m = /^- ([^:]+): (.*?) — (.*)$/.exec(line);
      if (!m || !LABELS.includes(m[1]) || !m[2].trim() || !m[3].trim())
        fails.push(fail("N5", at(n), `bullet does not read \`- {Label}: {scope} — {change}\` with Label in ${LABELS.join(", ")} and non-empty scope and change`));
      else sections.get(current.name).bullets.push({ key: `${m[1]}: ${m[2].trim()}`, line: n });
      items.push({ text: line, line: n, kind: "bullet" });
      prev = "bullet";
      continue;
    }
    fails.push(fail("N5", at(n), `line under \`## ${current.name}\` is neither a bullet nor an action line after a bullet`));
  }
  if (inFence) fails.push(fail("N8", at(fenceLine), "code fence never closes"));
  if (region === "head") checkLead(lines.length);
  closeSection();

  let verification = null;
  const v = sections.get("Verification");
  if (v) {
    let end = lines.length;
    for (let i = v.line; i < lines.length; i++)
      if (/^## /.test(lines[i])) {
        end = i;
        break;
      }
    verification = { start: v.line, end };
  }
  return { fails, sections, items, text, verification };
}

// The paths a single non-merge commit touches (its diff against its parent).
function commitPaths(cwd, sha) {
  return nulSplit(git(cwd, ["diff-tree", "--no-commit-id", "--name-only", "-r", "-z", sha]));
}

function coverageRules(cwd, o, note, head, notePath) {
  const fails = [];
  const base = resolveCommit(cwd, o.base, "--base");
  requireAncestor(cwd, base, head, o);
  const tsv = readInput(o.coverage, "coverage file");
  const commits = revList(cwd, base, head);
  // A commit whose changed paths are all release files (§ ready) is the
  // release's own; it needs no ledger row.
  const releaseOnly = new Set(
    commits.filter((c) => {
      const paths = commitPaths(cwd, c);
      return paths.length > 0 && paths.every(isReleaseFile);
    }),
  );
  const seen = new Map(commits.filter((c) => !releaseOnly.has(c)).map((c) => [c, 0]));
  textLines(tsv).forEach((row, idx) => {
    if (!row.trim()) return;
    const where = `${o.coverage}:${idx + 1}`;
    const fields = row.split("\t");
    const sha = fields[0].trim();
    if (fields.length < 2 || !/^[0-9a-f]{7,40}$/.test(sha)) {
      fails.push(fail("C1", where, "row is not `{sha7}\\t{section}\\t{Label}: {scope}` or `{sha7}\\tnone\\t{reason}`"));
      return;
    }
    const hits = commits.filter((c) => c.startsWith(sha));
    if (hits.length === 0)
      fails.push(fail("C1", where, `${sha} is not a non-merge commit of ${base.slice(0, 7)}..${head.slice(0, 7)}`));
    else if (hits.length > 1)
      fails.push(fail("C1", where, `${sha} names ${hits.length} commits of the range`));
    else if (seen.has(hits[0])) seen.set(hits[0], seen.get(hits[0]) + 1);
    const section = fields[1].trim();
    const rest = fields.slice(2).join("\t").trim();
    if (section === "none") {
      if (!rest) fails.push(fail("C3", where, `${sha} is \`none\` with no reason`));
      return;
    }
    const bullets = note.sections.get(section)?.bullets ?? [];
    if (!bullets.some((b) => b.key === rest))
      fails.push(fail("C2", where, `\`${section}\` holds no bullet \`- ${rest} — …\``));
  });
  for (const [c, count] of seen) {
    if (count === 0)
      fails.push(fail("C1", o.coverage, `${c.slice(0, 7)} is missing from the ledger`));
    else if (count > 1)
      fails.push(fail("C1", o.coverage, `${c.slice(0, 7)} appears ${count} times`));
  }

  const changes = nameStatus(cwd, base, head);
  const prefix = "templates/project/";
  const changed = new Set(
    changes.flatMap((c) => [c.path, c.oldPath]).filter((p) => p?.startsWith(prefix)),
  );
  for (const path of [...changed].sort()) {
    const rest = path.slice(prefix.length);
    // `{rest}` as a whole path, bare or under templates/project/: `a.md`
    // inside `a.md.bak` names another file.
    const whole = new RegExp(`(?<![\\w./-])(?:${reEscape(prefix)})?${reEscape(rest)}(?![\\w./-])`);
    if (!note.items.some((it) => whole.test(it.text)))
      fails.push(fail("C4", notePath, `${path} changed in the range; \`${rest}\` appears in no bullet or action line`));
  }

  const roles = roleChanges(cwd, base, head, changes);
  for (const [how, list] of [["added", roles.added], ["removed", roles.removed]])
    for (const r of list) {
      const token = reEscape(r.name.replace(/^\//, ""));
      if (!new RegExp(`(?<![A-Za-z0-9_-])${token}(?![A-Za-z0-9_-])`).test(note.text))
        fails.push(fail("C5", notePath, `${how} ${r.kind} ${r.name} appears nowhere in the note`));
    }
  return fails;
}

function versionRules(cwd, note, version, previous, bump, notePath) {
  const fails = [];
  const stamps = [
    ["VERSION", (t) => t.trim()],
    [".professor/VERSION", (t) => t.trim()],
    [
      ".professor/manifest.json",
      (t, path) => {
        try {
          return JSON.parse(t)?.installed_from?.version;
        } catch (err) {
          throw new CheckError(`${path} is not valid JSON: ${err.message}`);
        }
      },
    ],
  ];
  for (const [file, read] of stamps) {
    const path = join(cwd, file);
    const value = read(readInput(path, "version stamp"), path);
    if (value !== version.str)
      fails.push(fail("V1", file, `${value === undefined ? "no installed_from.version" : JSON.stringify(value)} differs from --version ${version.str}`));
  }

  const changelog = textLines(readInput(join(cwd, "CHANGELOG.md"), "changelog"));
  const heading = changelog.findIndex((l) => /^## Releases\s*$/.test(l));
  if (heading < 0) fails.push(fail("V2", "CHANGELOG.md", "no `## Releases` heading"));
  else {
    let entry = -1;
    for (let i = heading + 1; i < changelog.length && !/^#/.test(changelog[i]); i++)
      if (changelog[i].startsWith("- ")) {
        entry = i;
        break;
      }
    const v = version.str.replace(/\./g, "\\.");
    const m =
      entry >= 0 &&
      new RegExp(`^- \\[v${v}\\]\\(releases/v${v}\\.md\\) — (.*)$`).exec(changelog[entry]);
    if (entry < 0)
      fails.push(fail("V2", `CHANGELOG.md:${heading + 1}`, "`## Releases` holds no entry"));
    else if (!m || !m[1].trim())
      fails.push(fail("V2", `CHANGELOG.md:${entry + 1}`, `first entry is not \`- [v${version.str}](releases/v${version.str}.md) — {summary}\` with a summary`));
  }

  const [M, m, p] = previous.parts;
  const expected = { patch: [M, m, p + 1], minor: [M, m + 1, 0], major: [M + 1, 0, 0] }[bump];
  if (cmpVersion(expected, version.parts) !== 0)
    fails.push(fail("V3", "--version", `${version.str} is not ${previous.str} bumped by ${bump} (${expected.join(".")})`));
  const present = [...note.sections.keys()];
  const floor = present.some((s) => ["Breaking", "Migration", "Removed"].includes(s))
    ? M === 0 ? "minor" : "major"
    : present.includes("Added")
      ? "minor"
      : "patch";
  if (BUMPS.indexOf(bump) < BUMPS.indexOf(floor))
    fails.push(fail("V3", notePath, `--bump ${bump} is below the floor ${floor} of the note's sections (${present.join(", ") || "none"})`));
  return fails;
}

function commandRule(helpPath, note, notePath) {
  const help = readInput(helpPath, "pfm help file");
  const commands = new Set();
  for (const line of textLines(help)) {
    const m = /^ {2}(\S+)\s/.exec(line);
    if (m) commands.add(m[1]);
  }
  if (!commands.size)
    throw new CheckError(`pfm help file ${helpPath} lists no command (no line of two spaces then a command name) — the command list could not be read`);
  const fails = [];
  for (const it of note.items.filter((i) => i.kind === "action"))
    for (const m of it.text.matchAll(/`pfm ([^`\s]+)[^`]*`/g))
      if (!m[1].startsWith("-") && !commands.has(m[1]))
        fails.push(fail("P1", `${notePath}:${it.line}`, `\`pfm ${m[1]}\` is not in the command list of ${helpPath}`));
  return fails;
}

function writeRecord(path, lines) {
  try {
    mkdirSync(dirname(path), { recursive: true });
    writeFileSync(path, `${lines.join("\n")}\n`);
  } catch (err) {
    throw new CheckError(`--record ${path} could not be written: ${err.message}`);
  }
}

function groupGiven(o, names) {
  const given = names.filter((n) => o[n] !== undefined);
  if (given.length && given.length !== names.length)
    throw new CheckError(
      `usage: notes ${names.map((n) => `--${n}`).join(", ")} go together; missing ${names.filter((n) => o[n] === undefined).map((n) => `--${n}`).join(", ")}`,
    );
  return given.length === names.length;
}

function notesAll(o) {
  const extra = Object.keys(o).filter((k) => k !== "_" && k !== "all");
  if (extra.length || o._.length !== 1)
    throw new CheckError("usage: notes --all takes one directory and no other flag");
  const dir = o._[0];
  if (!existsSync(dir) || !statSync(dir).isDirectory())
    throw new CheckError(`notes directory ${dir} does not exist`);
  let names;
  try {
    names = readdirSync(dir).filter((f) => /^v.*\.md$/.test(f)).sort();
  } catch (err) {
    throw new CheckError(`notes directory ${dir} could not be listed: ${err.message}`);
  }
  const first = semver(GRAMMAR_FIRST, "GRAMMAR_FIRST").parts;
  const fails = [];
  let checked = 0;
  for (const name of names) {
    const m = /^v(\d+)\.(\d+)\.(\d+)\.md$/.exec(name);
    const where = join(dir, name);
    if (!m) {
      fails.push(fail("N1", where, "file name is not v{X.Y.Z}.md, so its grammar version cannot be placed"));
      continue;
    }
    if (cmpVersion([+m[1], +m[2], +m[3]], first) < 0) continue;
    checked++;
    fails.push(...parseNote(readInput(where, "note"), where, `${m[1]}.${m[2]}.${m[3]}`, `its file name ${name}`).fails);
  }
  return report("notes", fails, [`CHECKED ${checked} notes at or above v${GRAMMAR_FIRST} in ${dir}`]);
}

// An error once --record is known still records NOTES ERROR, so no earlier
// PASS survives it; a record that cannot be written is reported beside the
// original error, never in its place.
function recordError(path, head, err) {
  const message = err instanceof CheckError ? err.message : `internal: ${err.stack ?? err}`;
  try {
    writeRecord(path, [`NOTES ERROR ${head ?? "unknown"}`, `ERROR ${message}`]);
  } catch (writeErr) {
    return new CheckError(`${message}; and ${writeErr.message}`);
  }
  return err;
}

// The paths --record judges as committed: the note and the version stamps.
const RECORD_PATHS = ["VERSION", ".professor/VERSION", ".professor/manifest.json", "CHANGELOG.md"];

function notes(o) {
  const cwd = process.cwd();
  let head = null;
  try {
    if (o.all) return notesAll(o);
    if (o._.length !== 1) throw new CheckError("usage: notes takes one note file");
    const file = o._[0];
    const wantRange = groupGiven(o, ["base", "head", "coverage"]);
    // --version alone binds N1; --previous and --bump bring V1–V3 and need all three.
    const wantVersion =
      o.previous !== undefined || o.bump !== undefined
        ? groupGiven(o, ["version", "previous", "bump"])
        : false;
    const version = o.version === undefined ? null : semver(o.version, "--version");
    let previous;
    if (wantVersion) {
      previous = semver(o.previous, "--previous");
      if (!BUMPS.includes(o.bump))
        throw new CheckError(`usage: --bump ${o.bump} is not one of ${BUMPS.join(", ")}`);
    }
    const text = readInput(file, "note");
    if (wantRange || o.record) head = resolveCommit(cwd, o.head ?? "HEAD", "--head");
    // The record names a commit, so the note and stamps it judged must be that
    // commit's: a working-tree edit would pass a note HEAD does not hold.
    if (o.record) {
      const differs = nulSplit(
        git(cwd, ["status", "--porcelain", "-z", "--untracked-files=all", "--", file, ...RECORD_PATHS]),
      ).map((e) => (/^.. /.test(e) ? e.slice(3) : e));
      if (differs.length)
        throw new CheckError(`${differs.join(", ")} differs from HEAD — commit the note and stamps before recording`);
    }
    const note = parseNote(text, file, version?.str ?? null);
    const fails = [...note.fails];
    if (wantRange) fails.push(...coverageRules(cwd, o, note, head, file));
    if (wantVersion) fails.push(...versionRules(cwd, note, version, previous, o.bump, file));
    if (o["pfm-help"] !== undefined) fails.push(...commandRule(o["pfm-help"], note, file));
    const result = report("notes", fails);
    const groups = [
      wantRange && "range",
      wantVersion && "version",
      o["pfm-help"] !== undefined && "commands",
    ].filter(Boolean);
    if (o.record)
      writeRecord(o.record, [[`NOTES ${fails.length ? "FAIL" : "PASS"}`, head, ...groups].join(" "), ...result.lines]);
    return result;
  } catch (err) {
    if (o.record) throw recordError(o.record, head, err);
    throw err;
  }
}

// ---------------------------------------------------------------- ready

function reviewReports(dir) {
  const root = join(dir, "review");
  const found = [];
  const walk = (rel) => {
    let entries;
    try {
      entries = readdirSync(join(root, rel), { withFileTypes: true });
    } catch (err) {
      throw new CheckError(`${join(root, rel)} could not be listed: ${err.message}`);
    }
    for (const e of entries) {
      const path = rel ? `${rel}/${e.name}` : e.name;
      if (e.isDirectory()) {
        if (!rel && e.name.startsWith("sandbox-")) continue; // a reviewer's sandbox, not a report
        walk(path);
      } else if (e.name.endsWith(".md")) found.push(`review/${path}`);
    }
  };
  if (existsSync(root)) walk("");
  return found.sort();
}

function ready(o) {
  if (o._.length !== 1) throw new CheckError("usage: ready takes one release directory");
  if (!o.worktree) throw new CheckError("usage: ready needs --worktree PATH");
  const dir = resolve(o._[0]);
  const wt = resolve(o.worktree);
  if (!existsSync(dir) || !statSync(dir).isDirectory())
    throw new CheckError(`release directory ${dir} does not exist`);
  if (!existsSync(wt) || !statSync(wt).isDirectory())
    throw new CheckError(`worktree ${wt} does not exist`);
  const head = resolveCommit(wt, "HEAD", "HEAD");
  const version = git(wt, ["show", "HEAD:VERSION"]).trim();
  const notePath = `releases/v${version}.md`;
  const fails = [];
  const rerun = new Set();
  const isHead = (sha) => sha.length >= 7 && head.startsWith(sha);
  const failR = (rule, phase, where, what) => {
    fails.push(fail(rule, where, what));
    rerun.add(phase);
  };
  const since = new Map();
  const changedSince = (sha) => {
    if (!since.has(sha))
      since.set(sha, nulSplit(git(wt, ["diff", "--name-only", "-z", "--no-renames", "--no-relative", sha, head])));
    return since.get(sha);
  };

  // R1 — a report for every AREA of the kept scope, and every report an
  // allow-list: each finding carries a status, and every status reads
  // `resolved @{sha}` or `waived`, markup stripped and case ignored.
  const scopeText = readVerdict(join(dir, "scope.md"));
  if (scopeText === null) failR("R1", "REVIEW", "scope.md", "absent — the REVIEW scope was not kept");
  else
    for (const line of textLines(scopeText)) {
      const area = /^AREA (\S+) /.exec(line)?.[1];
      if (area && !existsSync(join(dir, "review", `${area}.md`)))
        failR("R1", "REVIEW", `review/${area}.md`, `no report for AREA ${area}`);
    }
  const reports = reviewReports(dir);
  if (!reports.length) failR("R1", "REVIEW", "review/", "no review/*.md exists");
  for (const rel of reports)
    for (const [where, what] of judgeReport(rel, readVerdict(join(dir, rel)) ?? ""))
      failR("R1", "REVIEW", where, what);

  // R2 — no tier-1 path outside the release files changed since the review.
  const reviewed = readVerdict(join(dir, "review/HEAD"))?.trim();
  if (!reviewed) failR("R2", "REVIEW", "review/HEAD", "absent or empty — no REVIEW recorded");
  else {
    const sha = /^[0-9a-f]{7,40}$/.test(reviewed) ? lookupCommit(wt, reviewed) : null;
    if (!sha) failR("R2", "REVIEW", "review/HEAD", `${reviewed} is not a commit of the worktree`);
    else
      for (const p of changedSince(sha))
        if (tierOf(p) && !isReleaseFile(p))
          failR("R2", "REVIEW", p, `tier-1 path changed since the review at ${sha.slice(0, 7)}`);
  }

  // R3 — every gate lane green at HEAD, or red on stable too; carried over
  // commits that touch only the release files.
  const lastExit = (text) => {
    const last = textLines(text ?? "").filter((l) => l.trim()).pop() ?? "";
    const m = /^EXIT (\d+) @([0-9a-f]{7,40})$/.exec(last.trim());
    return m ? { code: +m[1], sha: m[2], line: last.trim() } : { line: last.trim() };
  };
  for (const lane of LANES) {
    const rel = `gate/candidate-${lane}.log`;
    const text = readVerdict(join(dir, rel));
    if (text === null) {
      failR("R3", "GATE", rel, "absent");
      continue;
    }
    const cand = lastExit(text);
    if (cand.sha === undefined) {
      failR("R3", "GATE", rel, `does not end \`EXIT {code} @{sha}\`: ${JSON.stringify(cand.line)}`);
      continue;
    }
    if (cand.code !== 0) {
      const stableRel = `gate/stable-${lane}.log`;
      const stable = lastExit(readVerdict(join(dir, stableRel)));
      if (!(stable.code > 0)) {
        failR("R3", "GATE", rel, `candidate red (EXIT ${cand.code}) and ${stableRel} is not red — a new red`);
        continue;
      }
    }
    if (isHead(cand.sha)) continue;
    const sha = lookupCommit(wt, cand.sha);
    if (!sha) {
      failR("R3", "GATE", rel, `${cand.sha} is not a commit of the worktree`);
      continue;
    }
    const outside = changedSince(sha).filter((p) => !isReleaseFile(p));
    if (outside.length)
      failR("R3", "GATE", rel, `ran at ${sha.slice(0, 7)}, not HEAD; since then ${outside.length} path(s) outside the release files changed: ${outside.slice(0, 5).join(", ")}${outside.length > 5 ? ", …" : ""}`);
  }

  // R4 — the rehearsal CLEAN on both machines, or a recorded user ruling
  // that ships in the note's ## Verification; at HEAD or carried over
  // commits that touch only the note's ## Verification section.
  const rehearsal = readVerdict(join(dir, "rehearsal.md"));
  const lineOne = textLines(rehearsal ?? "")[0]?.trim() ?? "";
  const ruledLine = /^REHEARSAL RULED ([0-9a-f]{7,40}) round (\d+)(?:\s+—(.*))?$/.exec(lineOne);
  const r4 = ruledLine ?? /^REHEARSAL CLEAN ([0-9a-f]{7,40}) round (\d+)$/.exec(lineOne);
  if (!r4)
    failR("R4", "REHEARSE", "rehearsal.md", rehearsal === null ? "absent" : `line one is not \`${lineOne.startsWith("REHEARSAL RULED") ? "REHEARSAL RULED {sha} round {n} — {ruling}" : "REHEARSAL CLEAN {sha} round {n}"}\`: ${JSON.stringify(textLines(rehearsal)[0] ?? "")}`);
  else {
    if (ruledLine) {
      if (!(ruledLine[3] ?? "").trim())
        failR("R4", "REHEARSE", "rehearsal.md", "`REHEARSAL RULED {sha} round {n} — {ruling}` carries no ruling text");
      if (!treeHas(wt, head, notePath))
        failR("R4", "REHEARSE", notePath, `absent at HEAD ${head.slice(0, 7)}; the ruling ships in its \`## Verification\``);
      else {
        const noteText = git(wt, ["show", `HEAD:${notePath}`]);
        const range = verificationRange(noteText);
        const body = range ? textLines(noteText).slice(range.start, range.end) : [];
        const round = new RegExp(`\\bREHEARSAL RULED\\b.*\\bround ${ruledLine[2]}\\b`);
        if (!range)
          failR("R4", "REHEARSE", notePath, "holds no `## Verification` section; the ruling ships in it");
        else if (!body.some((l) => round.test(l)))
          failR("R4", "REHEARSE", notePath, `\`## Verification\` holds no line with \`REHEARSAL RULED\` and round ${ruledLine[2]}; the ruling ships in the note`);
      }
    }
    for (const machine of ruledLine ? [] : MACHINES) {
      const rel = `rehearsal/${machine}-${r4[2]}/result.json`;
      const raw = readVerdict(join(dir, rel));
      let verdict;
      try {
        verdict = raw === null ? "absent" : JSON.parse(raw)?.verdict;
      } catch (err) {
        verdict = `invalid JSON (${err.message})`;
      }
      if (verdict !== "CLEAN") failR("R4", "REHEARSE", rel, `verdict is not CLEAN: ${verdict}`);
    }
    if (!isHead(r4[1])) {
      const sha = lookupCommit(wt, r4[1]);
      if (!sha) failR("R4", "REHEARSE", "rehearsal.md", `${r4[1]} is not a commit of the worktree`);
      else carryRehearsal(wt, sha, head, notePath, changedSince(sha), failR);
    }
  }

  // R5 — the notes check passed at HEAD with every rule group, over a
  // committed worktree whose VERSION is X.Y.Z and whose note is at HEAD.
  const check = readVerdict(join(dir, "notes/check.txt"));
  const first = textLines(check ?? "")[0]?.trim() ?? "";
  const r5 = /^NOTES PASS ([0-9a-f]{7,40}) range version commands$/.exec(first);
  if (!r5 || !isHead(r5[1]))
    failR("R5", "NOTES", "notes/check.txt", check === null ? "absent" : `line one is not \`NOTES PASS ${head} range version commands\`: ${JSON.stringify(first)}`);
  const dirty = textLines(git(wt, ["status", "--porcelain", "--untracked-files=no"]))
    .map((l) => l.trim())
    .filter(Boolean);
  if (dirty.length)
    failR("R5", "NOTES", wt, `uncommitted tracked change: ${dirty.slice(0, 5).join("; ")}${dirty.length > 5 ? "; …" : ""}`);
  if (!/^\d+\.\d+\.\d+$/.test(version))
    failR("R5", "NOTES", "VERSION", `HEAD's VERSION ${JSON.stringify(version)} is not X.Y.Z`);
  else if (!treeHas(wt, head, notePath))
    failR("R5", "NOTES", notePath, `absent at HEAD ${head.slice(0, 7)}`);

  // R6 — the stamp names HEAD (the form gitter Phase RELEASE runs).
  const info = [];
  if (!o.stamp) {
    const stamp = readVerdict(join(dir, "READY"));
    const m = /^([0-9a-f]{7,40}) v\S+ \d{4}-\d{2}-\d{2}$/.exec(textLines(stamp ?? "")[0]?.trim() ?? "");
    if (stamp === null) failR("R6", "READY", "READY", "absent");
    else if (!m) failR("R6", "READY", "READY", `is not \`{sha} v{NEW} {YYYY-MM-DD}\`: ${JSON.stringify(textLines(stamp)[0] ?? "")}`);
    else if (!isHead(m[1])) failR("R6", "READY", "READY", `names ${m[1]}, not HEAD ${head}`);
  } else if (!fails.length) {
    const line = `${head} v${version} ${today()}`;
    try {
      writeFileSync(join(dir, "READY"), `${line}\n`);
    } catch (err) {
      throw new CheckError(`READY ${join(dir, "READY")} could not be written: ${err.message}`);
    }
    info.push(`STAMPED READY ${line}`);
  }
  return report("ready", fails, info, PHASES.filter((p) => rerun.has(p)).map((p) => `RERUN ${p}`));
}

// A finding opens at its `F{n}`, `S{n}` or `P{n}` id — bare, bulleted,
// numbered or a heading — and runs to the next finding or the end.
const FINDING = /^\s*(?:[-+]\s+|\d+\.\s+|#{1,6}\s+)?([FSP]\d+)\s*[·:|—-]/;

// One review report's R1 defects, as [where, what] pairs.
function judgeReport(rel, text) {
  if (!text.trim()) return [[rel, "report is empty"]];
  const out = [];
  let open = null; // the finding being read: {id, line, status}
  const close = () => {
    if (open && !open.status) out.push([`${rel}:${open.line}`, `finding ${open.id} carries no status`]);
  };
  textLines(text).forEach((raw, i) => {
    const line = raw.replace(/[*`_]/g, "");
    const id = FINDING.exec(line)?.[1];
    if (id) {
      close();
      open = { id, line: i + 1, status: false };
    }
    for (const m of line.matchAll(/status:/gi)) {
      if (open) open.status = true;
      const value = line.slice(m.index + m[0].length).trim();
      if (!/^(?:resolved @[0-9a-f]{7,40}\b|waived\b)/i.test(value))
        out.push([`${rel}:${i + 1}`, `status "${value}" is neither resolved @{sha} nor waived`]);
    }
  });
  close();
  return out;
}

const verificationRange = (text) =>
  text === null ? null : parseNote(text, "", null).verification;

// Every change since the rehearsed commit lies inside the note's
// ## Verification section: old-side lines inside it at the rehearsed commit,
// new-side lines inside it at HEAD, and no other file touched.
function carryRehearsal(wt, sha, head, notePath, changed, failR) {
  for (const p of changed)
    if (p !== notePath)
      failR("R4", "REHEARSE", p, `changed since the rehearsed ${sha.slice(0, 7)}; only ${notePath} § Verification carries the rehearsal over`);
  if (!changed.includes(notePath)) return;
  const show = (ref) =>
    nulSplit(git(wt, ["ls-tree", "--name-only", "-z", ref, "--", notePath])).length
      ? git(wt, ["show", `${ref}:${notePath}`])
      : null;
  const oldRange = verificationRange(show(sha));
  const newRange = verificationRange(show(head));
  const diff = git(wt, [
    "diff", "-U0", "--no-color", "--no-ext-diff", "--no-relative", "--no-renames", sha, head, "--", notePath,
  ]);
  const inside = (range, start, count) =>
    range && start >= range.start && start + count - 1 <= range.end;
  for (const line of diff.split("\n")) {
    const m = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(line);
    if (!m) continue;
    const [a, b, c, d] = [+m[1], m[2] === undefined ? 1 : +m[2], +m[3], m[4] === undefined ? 1 : +m[4]];
    const ok = (b === 0 || inside(oldRange, a, b)) && (d === 0 || inside(newRange, c, d));
    if (!ok)
      failR("R4", "REHEARSE", `${notePath}:${c}`, `${line.replace(/ @@.*$/, " @@")} changes lines outside ## Verification since the rehearsed ${sha.slice(0, 7)}`);
  }
}

// ---------------------------------------------------------------- main

// Info lines first; then one FAIL line per failure and the RERUN lines, or the clean line.
function report(mode, fails, info = [], reruns = []) {
  const lines = fails.length
    ? [...info, ...fails.map((f) => `FAIL ${f.rule}: ${f.where} — ${f.what}`), ...reruns]
    : [...info, `release-check ${mode}: clean`];
  return { lines, code: fails.length ? 1 : 0 };
}

const FLAGS = {
  scope: { values: ["base", "head"], bools: [] },
  notes: {
    values: ["base", "head", "coverage", "version", "previous", "bump", "pfm-help", "record"],
    bools: ["all"],
  },
  ready: { values: ["worktree"], bools: ["stamp"] },
};

function parseArgs(mode, args, o) {
  const { values, bools } = FLAGS[mode];
  for (let i = 0; i < args.length; i++) {
    const a = args[i];
    if (!a.startsWith("--")) {
      o._.push(a);
      continue;
    }
    const name = a.slice(2);
    if (o[name] !== undefined) throw new CheckError(`usage: ${mode} ${a} given twice`);
    if (bools.includes(name)) o[name] = true;
    else if (values.includes(name)) {
      const v = args[++i];
      if (v === undefined || v.startsWith("--"))
        throw new CheckError(`usage: ${mode} ${a} needs a value`);
      o[name] = v;
    } else throw new CheckError(`usage: ${mode} has no flag ${a}`);
  }
  return o;
}

function main(argv) {
  const [mode, ...args] = argv;
  if (!FLAGS[mode]) throw new CheckError(`usage: unknown mode ${JSON.stringify(mode ?? "")}`);
  const o = { _: [] };
  try {
    parseArgs(mode, args, o);
  } catch (err) {
    // notes --record: a usage error after the record path is parsed still records.
    if (o.record) throw recordError(o.record, null, err);
    throw err;
  }
  return { scope, notes, ready }[mode](o);
}

try {
  const { lines, code } = main(process.argv.slice(2));
  process.stdout.write(`${lines.join("\n")}\n`);
  process.exitCode = code;
} catch (err) {
  if (err instanceof CheckError) {
    process.stderr.write(`ERROR ${err.message}\n`);
    if (err.message.startsWith("usage:")) process.stderr.write(`${USAGE}\n`);
  } else {
    process.stderr.write(`ERROR internal: ${err.stack ?? err}\n`);
  }
  process.exitCode = 2;
}
