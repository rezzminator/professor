package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
)

// LabelSoleTab is the one tab-label rule: a tab one pane sits in alone takes
// the label, a tab already labelled is left alone, a shared tab is left alone
// as the named ErrTabNotSole skip, an empty tab labels nothing, and a tab
// Herdr will not read or relabel, or a tab.get answering for another tab, is
// an error.
func TestLabelSoleTab(t *testing.T) {
	for _, test := range []struct {
		name, tab, label string
		panes            int
		refuse           string
		wantMethods      []string
		wantErr          string
		answer           string
		wantIs           error
	}{
		{"a sole-pane tab takes the label", "w1:t1", "1", 1, "", []string{"tab.get", "tab.rename:Fix It"}, "", "", nil},
		{"a tab already labelled is left alone", "w1:t1", "Fix It", 1, "", []string{"tab.get"}, "", "", nil},
		{"a shared tab keeps its label", "w1:t1", "1", 2, "", []string{"tab.get"}, "not alone", "", ErrTabNotSole},
		{
			"a tab.get answering another tab is an error", "w1:t1", "1", 1, "",
			[]string{"tab.get"},
			"tab.get for w1:t1 returned tab w1:t9", "w1:t9", nil,
		},
		{"no tab labels nothing", "", "1", 1, "", nil, "", "", nil},
		{
			"an unreadable tab is an error", "w1:t1", "1", 1, "tab.get",
			[]string{"tab.get"},
			"read: herdr refused tab.get", "", nil,
		},
		{
			"a refused relabel is an error", "w1:t1", "1", 1, "tab.rename",
			[]string{"tab.get", "tab.rename:Fix It"},
			`relabel "Fix It": herdr refused tab.rename`, "", nil,
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
					answer := test.tab
					if test.answer != "" {
						answer = test.answer
					}
					return fmt.Sprintf(
						`{"id":%q,"ok":true,"result":{"type":"tab_info",`+
							`"tab":{"tab_id":%q,"label":%q,"pane_count":%d}}}`,
						req.ID, answer, test.label, test.panes,
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
			if test.wantIs != nil && !errors.Is(err, test.wantIs) {
				t.Fatalf("err = %v, want errors.Is %v", err, test.wantIs)
			}
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(methods, test.wantMethods) {
				t.Fatalf("requests = %v, want %v", methods, test.wantMethods)
			}
		})
	}
}
