package modelglyph

// SparkLevels is the ladder every one-cell sparkline draws with, lowest to
// highest: the scan lines (Miscellaneous Technical U+23BA–U+23BD between _ and
// ¯) rather than Block Elements ▁…█ or Braille, both of which are WebGL "custom
// glyphs" in VS Code's terminal, drawn from its atlas instead of the font, and
// painted stale or blank cells on live refresh (2026-09-11).
// cmd/pfm/webgl_glyph_guard_test.go pins the banned ranges.
var SparkLevels = [...]string{"_", "⎽", "⎼", "⎻", "⎺", "¯"}
