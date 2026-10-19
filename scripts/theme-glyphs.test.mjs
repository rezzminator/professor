import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { test } from "node:test";

const themeURL = new URL("../templates/themes/tokyo-night/herdr.json", import.meta.url);
const { tabGlyphs } = JSON.parse(readFileSync(themeURL, "utf8"));

test("tokyo-night uses sci-fi status glyphs with activity and attention loops", () => {
  assert.deepEqual(tabGlyphs, {
    blocked: ["⚡", "🔋"],
    dead: "🪫",
    error: "👾",
    idle: "📡",
    unknown: "🤖",
    working: ["🚀", "🌌", "🛸", "🌌"],
  });
});

test("tokyo-night frames are single default-emoji codepoints with East Asian width two", () => {
  const frames = Object.entries(tabGlyphs).flatMap(([state, value]) =>
    (Array.isArray(value) ? value : [value]).map((glyph, index) => ({ state, index, glyph })),
  );
  assert.ok(frames.length > 0, "no frames checked");
  for (const { state, index, glyph } of frames) {
    assert.match(glyph, /^\p{Emoji_Presentation}$/u, `${state}[${index}]: one default-emoji codepoint required`);
  }
  // Python's Unicode database supplies East_Asian_Width; JS exposes Emoji_Presentation, not width.
  const result = spawnSync("python3", ["-I", "-c", [
    "import json, sys, unicodedata",
    "for frame in json.load(sys.stdin):",
    "    glyph = frame['glyph']",
    "    assert len(glyph) == 1 and unicodedata.east_asian_width(glyph) in ('W', 'F'), frame",
  ].join("\n")], { input: JSON.stringify(frames), encoding: "utf8" });
  assert.ifError(result.error);
  assert.equal(result.status, 0, `East Asian width check failed: ${result.stderr}`);
});
