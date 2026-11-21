package installer

import "testing"

// TestInsertHerdrConfigLines holds the one text door every Herdr config.toml
// key addition goes through: where the lines land, and the refusal when no
// placement keeps the rest of the config as it was.
func TestInsertHerdrConfigLines(t *testing.T) {
	for _, row := range []struct {
		name, original, lines, want, err string
		table                            []string
	}{
		{
			name: "a top-level key goes before an array of tables", original: "[[keys.command]]\nkey = \"a\"\n",
			lines: "onboarding = false\n", want: "onboarding = false\n[[keys.command]]\nkey = \"a\"\n",
		},
		{
			name: "a top-level key goes first in a file without a table", original: "theme = \"x\"",
			lines: "onboarding = false\n", want: "onboarding = false\ntheme = \"x\"",
		},
		{
			name:     "a top-level key goes above the comments and pfm marker of the first table",
			original: "theme = \"x\"\n\n# mine\n# >>> pfm herdr keys >>>\n[keys]\nquit = \"q\"\n# <<< pfm herdr keys <<<\n",
			lines:    "onboarding = false\n",
			want: "theme = \"x\"\n\nonboarding = false\n# mine\n# >>> pfm herdr keys >>>\n[keys]\nquit = \"q\"\n" +
				"# <<< pfm herdr keys <<<\n",
		},
		{
			name:     "a table only a pfm block holds takes no key",
			original: "# >>> pfm theme · x · gen 1 >>>\n[theme]\nname = \"x\"\n# <<< pfm theme <<<\n", table: []string{"theme"},
			lines: "accent = \"red\"\n", err: "cannot add accent without rewriting the existing theme table",
		},
		{
			name: "a quoted header is the table", original: "[\"ui\"]\nsound = false\n", table: []string{"ui"},
			lines: "width = 1\n", want: "[\"ui\"]\nwidth = 1\nsound = false\n",
		},
		{
			name: "a header ending in CRLF is the table", original: "[ui]\r\nsound = false\r\n", table: []string{"ui"},
			lines: "width = 1\n", want: "[ui]\r\nwidth = 1\nsound = false\r\n",
		},
		{
			name: "a subtable's header is not the table", original: "[ui.inner]\nsound = false\n", table: []string{"ui"},
			lines: "width = 1\n", want: "[ui.inner]\nsound = false\n\n[ui]\nwidth = 1\n",
		},
		{
			name: "a dotted table goes under its own header", original: "[ui]\n[ui.inner]\nsound = false\n",
			table: []string{"ui", "inner"}, lines: "width = 1\n", want: "[ui]\n[ui.inner]\nwidth = 1\nsound = false\n",
		},
		{
			name: "an inline table cannot take a key", original: "ui = { sound = false }\n", table: []string{"ui"},
			lines: "width = 1\n", err: "cannot add width without rewriting the existing ui table",
		},
		{
			name: "a key under a value that is no table", original: "ui = 3\n", table: []string{"ui"},
			lines: "width = 1\n", err: "ui is not a table",
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			got, err := insertHerdrConfigLines(row.original, row.table, row.lines)
			if row.err != "" {
				if err == nil || err.Error() != row.err {
					t.Fatalf("insertHerdrConfigLines(%q) = %q, %v; want error %q", row.original, got, err, row.err)
				}
				return
			}
			if err != nil || got != row.want {
				t.Fatalf("insertHerdrConfigLines(%q) = %q, %v; want %q", row.original, got, err, row.want)
			}
		})
	}
}
