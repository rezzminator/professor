package testjail

import "testing"

func TestSeedConfigWithSetsExtraTopLevelKeys(t *testing.T) {
	for _, row := range []struct {
		name  string
		extra map[string]any
		want  string
	}{
		{"no extra keeps the seed", nil, `{"claude":{"systemPrompt":"production"},"version":2}`},
		{
			"an account joins the seed",
			map[string]any{"accounts": []map[string]any{{"id": 1, "configDir": "/jail/home/.cc/1"}}},
			`{"accounts":[{"configDir":"/jail/home/.cc/1","id":1}],"claude":{"systemPrompt":"production"},"version":2}`,
		},
		{"an extra key replaces the seed's", map[string]any{"version": 3}, `{"claude":{"systemPrompt":"production"},"version":3}`},
	} {
		t.Run(row.name, func(t *testing.T) {
			got, err := seedConfigWith(row.extra)
			if err != nil || string(got) != row.want {
				t.Fatalf("seedConfigWith(%v) = %s, %v; want %s", row.extra, got, err, row.want)
			}
		})
	}
}
