package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckProtocol(t *testing.T) {
	for _, scenario := range []struct {
		name, result, want string
		mismatch           bool
	}{
		{name: "supported", result: `{"type":"pong","version":"0.9.5","protocol":22}`},
		{name: "mismatch", result: `{"type":"pong","version":"0.9.5","protocol":23}`, mismatch: true, want: "herdr protocol 23 DIVERGES from supported 22 — herdr seats disabled"},
		{name: "zero", result: `{"type":"pong","version":"0.9.5","protocol":0}`, mismatch: true, want: "herdr protocol 0 DIVERGES from supported 22 — herdr seats disabled"},
		{name: "null", result: `null`, want: "herdr ping: response has no result"},
		{name: "wrong type", result: `{"type":"other","version":"0.9.5","protocol":22}`, want: "malformed ping result:"},
		{name: "missing protocol", result: `{"type":"pong","version":"0.9.5"}`, want: "malformed ping result:"},
		{name: "string protocol", result: `{"type":"pong","version":"0.9.5","protocol":"22"}`, want: "malformed ping result:"},
		{name: "negative protocol", result: `{"type":"pong","version":"0.9.5","protocol":-1}`, want: "malformed ping result:"},
		{name: "overflow", result: `{"type":"pong","version":"0.9.5","protocol":4294967296}`, want: "malformed ping result:"},
		{name: "missing version", result: `{"type":"pong","protocol":22}`, want: "malformed ping result:"},
		{name: "dead socket", want: "dial for ping"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			socket := filepath.Join(t.TempDir(), "dead.sock")
			if scenario.result != "" {
				socket = serve(t, func(line string) string {
					var request struct {
						Method string `json:"method"`
					}
					if err := json.Unmarshal([]byte(line), &request); err != nil {
						t.Errorf("decode ping request: %v", err)
					}
					if request.Method != "ping" {
						t.Errorf("method=%q, want ping", request.Method)
					}
					return `{"result":` + scenario.result + `}`
				})
			}
			status, err := ProbeProtocol(context.Background(), socket)
			guardError := CheckProtocol(context.Background(), socket)
			if scenario.want == "" {
				if err != nil || guardError != nil || status.Version != "0.9.5" ||
					status.Protocol != SupportedProtocol {
					t.Fatalf("status=%+v probe=%v guard=%v", status, err, guardError)
				}
				return
			}
			if err == nil || guardError == nil || !strings.Contains(err.Error(), scenario.want) ||
				!strings.Contains(guardError.Error(), scenario.want) {
				t.Fatalf("probe=%v guard=%v, want %q", err, guardError, scenario.want)
			}
			var mismatch *ProtocolMismatch
			if errors.As(err, &mismatch) != scenario.mismatch {
				t.Fatalf("mismatch type=%T, want %v", err, scenario.mismatch)
			}
		})
	}
}
