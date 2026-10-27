package herdr

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestStoredDecodesTheStateALiveChatServerHolds pins the JSON keys of the
// @pfm_herdr option: a chat server started by an older pfm keeps this exact
// shape, and pfm chat ls reads it beside the reporter.
func TestStoredDecodesTheStateALiveChatServerHolds(t *testing.T) {
	raw := `{"engine":"claude","session_id":"s-1","state":"working","message":"","seq":7,"name":"ok-reply",` +
		`"clients":{"86015":{"pane":"wD:p1","terminal":"term_65deadb27f72b27","socket":"/h.sock"}}}`
	var stored Stored
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	want := Stored{
		Engine: "claude", SessionID: "s-1", State: "working", Seq: 7, Name: "ok-reply",
		Clients: map[string]Client{"86015": {Pane: "wD:p1", Terminal: "term_65deadb27f72b27", Socket: "/h.sock"}},
	}
	if !reflect.DeepEqual(stored, want) {
		t.Fatalf("Stored = %+v, want %+v", stored, want)
	}
	if StoredOption != "@pfm_herdr" {
		t.Fatalf("StoredOption = %q, want @pfm_herdr (the option live chat servers hold)", StoredOption)
	}
}
