package config

import "testing"

func TestPricesPathIsSiblingOfConfigFile(t *testing.T) {
	cases := []struct{ name, configPath, want string }{
		{"absolute", "/x/pfm.config.json", "/x/pfm.prices.json"},
		{"nested", "/tmp/demo-proj/.config/pfm/pfm.config.json", "/tmp/demo-proj/.config/pfm/pfm.prices.json"},
		{"bare file name", "pfm.config.json", "pfm.prices.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PricesPath(tc.configPath); got != tc.want {
				t.Fatalf("PricesPath(%q) = %q, want %q", tc.configPath, got, tc.want)
			}
		})
	}
	if PricesFileName != "pfm.prices.json" {
		t.Fatalf("PricesFileName = %q, want pfm.prices.json", PricesFileName)
	}
}
