import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { test } from "node:test";

const themesURL = new URL("../templates/themes/", import.meta.url);
const defaultsURL = new URL("../pfm/internal/herdr/tab_glyphs.go", import.meta.url);
const { tabGlyphs } = JSON.parse(readFileSync(new URL("tokyo-night/herdr.json", themesURL), "utf8"));

// The user's law: only the states that need the user (blocked, error) may animate.
const steadyStates = ["working", "done", "idle", "dead", "unknown"];

const asFrames = (value) => (Array.isArray(value) ? value : [value]);

// defaultTabGlyphs as pfm ships it, read from its Go source so the JS check
// judges the values the painter falls back to, never a copy of them.
function readDefaultTabGlyphs() {
  const source = readFileSync(defaultsURL, "utf8");
  const block = source.match(/var defaultTabGlyphs = TabGlyphs\{\n([\s\S]*?)\n\}/);
  assert.ok(block, `defaultTabGlyphs not found in ${defaultsURL.pathname}`);
  const defaults = {};
  for (const line of block[1].split("\n")) {
    const field = line.match(/^\s*(\w+):\s*TabFrames\{(.*)\},\s*$/);
    assert.ok(field, `unparsed defaultTabGlyphs line: ${line}`);
    defaults[field[1].toLowerCase()] = [...field[2].matchAll(/"([^"]*)"/g)].map((m) => m[1]);
  }
  assert.equal(Object.keys(defaults).length, 7, "defaultTabGlyphs must set all seven states");
  return defaults;
}

function shippedTabGlyphs() {
  const profiles = readdirSync(themesURL, { withFileTypes: true }).filter((entry) => entry.isDirectory());
  assert.ok(profiles.length > 0, `no profiles found in ${themesURL.pathname}`);
  return profiles.map(({ name }) => {
    const herdrURL = new URL(`${name}/herdr.json`, themesURL);
    const herdr = existsSync(herdrURL) ? JSON.parse(readFileSync(herdrURL, "utf8")) : {};
    return { name, glyphs: herdr.tabGlyphs ?? {} };
  });
}

function assertWideEmojiFrames(label, glyphs) {
  const frames = Object.entries(glyphs).flatMap(([state, value]) =>
    asFrames(value).map((glyph, index) => ({ state, index, glyph })),
  );
  assert.ok(frames.length > 0, `${label}: no frames checked`);
  for (const { state, index, glyph } of frames) {
    assert.match(glyph, /^\p{Emoji_Presentation}$/u, `${label} ${state}[${index}]: one default-emoji codepoint required`);
  }
  // Python's Unicode database supplies East_Asian_Width; JS exposes Emoji_Presentation, not width.
  const result = spawnSync("python3", ["-I", "-c", [
    "import json, sys, unicodedata",
    "for frame in json.load(sys.stdin):",
    "    glyph = frame['glyph']",
    "    assert len(glyph) == 1 and unicodedata.east_asian_width(glyph) in ('W', 'F'), frame",
  ].join("\n")], { input: JSON.stringify(frames), encoding: "utf8" });
  assert.ifError(result.error);
  assert.equal(result.status, 0, `${label}: East Asian width check failed: ${result.stderr}`);
}

test("tokyo-night colours carry status and only attention states move", () => {
  assert.deepEqual(tabGlyphs, {
    blocked: ["🚨", "🔴"],
    dead: "🪫",
    done: "⭐",
    error: ["👾", "💥"],
    idle: "🌑",
    unknown: "🤖",
    working: "🚀",
  });
});

test("tokyo-night frames are single default-emoji codepoints with East Asian width two", () => {
  assertWideEmojiFrames("tokyo-night", tabGlyphs);
});

test("default tab glyphs are the user's set, still except blocked and error", () => {
  const defaults = readDefaultTabGlyphs();
  assert.deepEqual(defaults, {
    working: ["🔵"],
    idle: ["⚪"],
    blocked: ["🔴", "⭕"],
    done: ["🟡"],
    error: ["🟥", "❌"],
    dead: ["⬛"],
    unknown: ["❔"],
  });
  assertWideEmojiFrames("defaultTabGlyphs", defaults);
});

test("every shipped profile holds working, done, idle, dead and unknown to one frame", () => {
  const defaults = readDefaultTabGlyphs();
  const profiles = shippedTabGlyphs();
  assert.ok(profiles.some(({ glyphs }) => Object.keys(glyphs).length > 0), "no profile sets tabGlyphs; the check judged only defaults");
  const animated = profiles.flatMap(({ name, glyphs }) =>
    steadyStates.flatMap((state) => {
      const source = state in glyphs ? `${name}/herdr.json` : "defaultTabGlyphs";
      const frames = asFrames(state in glyphs ? glyphs[state] : defaults[state]);
      return frames.length === 1 ? [] : [`${name} ${state} (from ${source}) animates ${JSON.stringify(frames)}`];
    }),
  );
  assert.deepEqual(animated, [], "a steady state takes one frame");
});
