package herdr

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
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

// A corrupt tab-label record never blocks the tab's label: it is named in the
// reporter log doctor reads, read as empty, and replaced by the next write.
func TestACorruptTabLabelRecordIsNamedAndReplaced(t *testing.T) {
	dir := t.TempDir()
	socket, tab := "/fake/herdr.sock", "w1:t1"
	path := filepath.Join(dir, fmt.Sprintf("tab-%x", sha256.Sum256([]byte(socket+"\x00"+tab)))) + ".json"
	if err := os.WriteFile(path, []byte("{torn"), 0o600); err != nil {
		t.Fatal(err)
	}
	var renamed []string
	call := func(_ context.Context, _ string, lines ...[]byte) ([]json.RawMessage, error) {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Label string `json:"label"`
			} `json:"params"`
		}
		if err := json.Unmarshal(lines[0], &req); err != nil {
			return nil, err
		}
		if req.Method == "tab.rename" {
			renamed = append(renamed, req.Params.Label)
			return []json.RawMessage{json.RawMessage(`{"type":"ok"}`)}, nil
		}
		info := `{"type":"tab_info","tab":{"tab_id":"w1:t1","label":"1","pane_count":1}}`
		return []json.RawMessage{json.RawMessage(info)}, nil
	}
	var seq uint64
	if err := writeSoleTabLabel(
		context.Background(), socket, dir, tab, "Fix It", true, func() uint64 { seq++; return seq }, call,
	); err != nil {
		t.Fatalf("label over a corrupt record = %v", err)
	}
	if !slices.Equal(renamed, []string{"Fix It"}) {
		t.Fatalf("renames = %q, want [Fix It]", renamed)
	}
	if record, err := readTabLabel(path); err != nil || record.Base != "Fix It" {
		t.Fatalf("record %s after the label = %+v, %v; want base Fix It", path, record, err)
	}
	records, err := RecentErrors(dir, time.Time{})
	want := "decode tab label " + path + ": invalid character 't' looking for beginning of object key string; read as empty"
	if err != nil || len(records) != 1 || records[0].Session != tabLabelLogSession || records[0].Message != want {
		t.Fatalf("reporter log = %+v, %v; want one %q record %q", records, err, tabLabelLogSession, want)
	}
}
