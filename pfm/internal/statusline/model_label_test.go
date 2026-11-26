package statusline

import "testing"

// A row names its model the way the main line does, from an id, an alias or a
// display name alike; a gpt- model keeps its tier-version form.
func TestModelLabel(t *testing.T) {
	for _, tc := range []struct{ model, want string }{
		{"claude-opus-5-5[1m]", "Opus 5.5"},
		{"claude-opus-5-5", "Opus 5.5"},
		{"claude-fable-5-2", "Fable 5.2"},
		{"claude-sonnet-5-5", "Sonnet 5.5"},
		{"claude-haiku-4-5-20251001", "Haiku 4.5"},
		{"claude-opus-5", "Opus 5"},
		{"gw:catalog/claude-opus-5-5[1m]", "Opus 5.5"},
		{"Claude-Opus-5-5", "Opus 5.5"},
		{"opus", "Opus"},
		{"Opus 5.5", "Opus 5.5"},
		{" claude-sonnet-5-5 ", "Sonnet 5.5"},
		{"gpt-6-sol", "sol-6"},
		{"gpt-5.6-terra", "terra-5.6"},
		{"gpt-6-sol-fast[1m]", "sol-6"},
		{"gpt-6", "gpt-6"},
		{"claude-mystery-x", "mystery-x"},
		{"claude-3-5-sonnet-20241022", "3-5-sonnet-20241022"},
		{"claude-opus-5-5-1234567", "opus-5-5-1234567"},
		{"claude-opus-5-10", "Opus 5.10"},
		{"claude-opus-5-100", "opus-5-100"},
		{"us.anthropic.claude-haiku-4-5-20251001-v1:0", "Haiku 4.5"},
		{"claude-sonnet-4-5@20250929", "Sonnet 4.5"},
		{"", ""},
	} {
		if got := modelLabel(tc.model); got != tc.want {
			t.Errorf("modelLabel(%q) = %q, want %q", tc.model, got, tc.want)
		}
	}
}
