package themesync

import "testing"

func TestEveryBuiltinThemeCarriesEveryDerivedTokenAsAParsableColour(t *testing.T) {
	if len(builtinThemes) != 6 {
		t.Fatalf("builtinThemes = %d themes, want Claude's 6", len(builtinThemes))
	}
	for name, tokens := range builtinThemes {
		for _, token := range deriveTokens {
			raw, ok := tokens[token]
			if !ok {
				t.Errorf("%s lacks %s", name, token)
				continue
			}
			if _, err := parseColor(raw); err != nil {
				t.Errorf("%s.%s = %q: %v", name, token, raw, err)
			}
		}
		if _, err := deriveVariant(name, tokens); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
