package herdr

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

// LabelSoleTab is the one tab-label rule: a tab one pane sits in alone takes
// the label, a shared tab and a tab already labelled are left alone, an empty
// tab labels nothing, and a tab Herdr will not read or relabel is an error.
func TestLabelSoleTab(t *testing.T) {
	for _, test := range []struct {
		name, tab, label string
		panes            int
		refuse           string
		wantMethods      []string
		wantErr          string
	}{
		{"a sole-pane tab takes the label", "w1:t1", "1", 1, "", []string{"tab.get", "tab.rename:Fix It"}, ""},
		{"a tab already labelled is left alone", "w1:t1", "Fix It", 1, "", []string{"tab.get"}, ""},
		{"a shared tab keeps its label", "w1:t1", "1", 2, "", []string{"tab.get"}, ""},
		{"no tab labels nothing", "", "1", 1, "", nil, ""},
		{
			"an unreadable tab is an error", "w1:t1", "1", 1, "tab.get",
			[]string{"tab.get"},
			"read: herdr refused tab.get",
		},
		{
			"a refused relabel is an error", "w1:t1", "1", 1, "tab.rename",
			[]string{"tab.get", "tab.rename:Fix It"},
			`relabel "Fix It": herdr refused tab.rename`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var mu sync.Mutex
			var methods []string
			socket := serve(t, func(line string) string {
				var req struct {
					ID     string `json:"id"`
					Method string `json:"method"`
					Params struct {
						Tab   string `json:"tab_id"`
						Label string `json:"label"`
					} `json:"params"`
				}
				if err := json.Unmarshal([]byte(line), &req); err != nil {
					return `{"ok":false,"error":{"code":"bad_request","message":"decode"}}`
				}
				mu.Lock()
				if req.Method == "tab.rename" {
					methods = append(methods, req.Method+":"+req.Params.Label)
				} else {
					methods = append(methods, req.Method)
				}
				mu.Unlock()
				if req.Params.Tab != test.tab {
					return fmt.Sprintf(`{"id":%q,"ok":false,"error":{"code":"tab_not_found","message":"x"}}`, req.ID)
				}
				if req.Method == test.refuse {
					return fmt.Sprintf(`{"id":%q,"ok":false,"error":{"code":"invalid_params","message":"no"}}`, req.ID)
				}
				if req.Method == "tab.get" {
					return fmt.Sprintf(
						`{"id":%q,"ok":true,"result":{"type":"tab_info",`+
							`"tab":{"tab_id":%q,"label":%q,"pane_count":%d}}}`,
						req.ID, test.tab, test.label, test.panes,
					)
				}
				return fmt.Sprintf(`{"id":%q,"ok":true,"result":{"type":"ok"}}`, req.ID)
			})
			var seq uint64
			err := LabelSoleTab(context.Background(), socket, test.tab, "Fix It", func() uint64 { seq++; return seq })
			if test.wantErr == "" && err != nil ||
				test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("err = %v, want %q", err, test.wantErr)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(methods, test.wantMethods) {
				t.Fatalf("requests = %v, want %v", methods, test.wantMethods)
			}
		})
	}
}
