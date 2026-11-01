package gather

import (
	"context"
	"strings"
	"testing"
)

// TestHerdrTerminalListedNeverReadsABadListingAsAbsent: a terminal is listed
// or not only on a well-formed pane.list; a malformed or duplicate listing is
// an error, never "not listed".
func TestHerdrTerminalListedNeverReadsABadListingAsAbsent(t *testing.T) {
	const pane = `{"pane_id":"w1:p1","workspace_id":"w1","terminal_id":"term-1"}`
	cases := []struct {
		name    string
		list    string
		want    bool
		wantErr string
	}{
		{"listed", `{"result":{"type":"pane_list","panes":[` + pane + `]}}`, true, ""},
		{"absent", `{"result":{"type":"pane_list","panes":[]}}`, false, ""},
		{"duplicate", `{"result":{"type":"pane_list","panes":[` + pane + `,` + pane + `]}}`, false, "duplicate"},
		{"envelope", `{"result":{"type":"pane_info"}}`, false, "want pane_list"},
		{"refused", `{"error":{"code":"internal","message":"down"}}`, false, "list Herdr panes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			socket, _ := herdrRefusalSocket(t, tc.list, "")
			got, err := HerdrTerminalListed(context.Background(), socket, "term-1")
			if got != tc.want {
				t.Fatalf("HerdrTerminalListed() = %v %v, want %v", got, err, tc.want)
			}
			if tc.wantErr == "" && err != nil ||
				tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("HerdrTerminalListed() error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}
