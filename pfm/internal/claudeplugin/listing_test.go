package claudeplugin

import (
	"path/filepath"
	"strings"
	"testing"
)

// ListedEnabled returns only names the marketplace both enables and lists.
func TestListedEnabledReturnsOnlyEnabledNamesTheListingHolds(t *testing.T) {
	t.Parallel()
	enabled := `{"enabledPlugins":{"flights@professor":true,"harvester@professor":true,"off@professor":false,` +
		`"secret@professor":true,"flights@other":true,"stranger@other":true}}`
	listing := `{"name":"professor","plugins":[{"name":"flights"},{"name":"harvester"},{"name":"off"}]}`
	for _, test := range []struct {
		name, known, listing, want, wantErr string
	}{
		{name: "enabled and listed", known: "professor", listing: listing, want: "flights,harvester"},
		{name: "marketplace unknown", known: "other", listing: listing},
		{name: "listing missing", known: "professor"},
		{name: "listing unparsable", known: "professor", listing: "{", wantErr: "parse {market}/.claude-plugin/marketplace.json: unexpected end of JSON input"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config, market := t.TempDir(), t.TempDir()
			writeFixture(t, filepath.Join(config, "settings.json"), enabled)
			writeFixture(t, filepath.Join(config, "plugins", "known_marketplaces.json"),
				`{"`+test.known+`":{"source":{"source":"github","repo":"o/r"},"installLocation":"`+market+`"}}`)
			if test.listing != "" {
				writeFixture(t, filepath.Join(market, ".claude-plugin", "marketplace.json"), test.listing)
			}
			got, err := ListedEnabled(config, "professor")
			wantErr := strings.ReplaceAll(test.wantErr, "{market}", market)
			if strings.Join(got, ",") != test.want || (err == nil) != (wantErr == "") ||
				(err != nil && err.Error() != wantErr) {
				t.Fatalf("ListedEnabled = %q, %v; want %q, %q", got, err, test.want, wantErr)
			}
		})
	}
}
