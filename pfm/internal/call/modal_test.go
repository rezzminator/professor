package call

import (
	"context"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

func TestDenyModalNativeOrderedKeys(t *testing.T) {
	for _, down := range []int{0, 1, 2} {
		t.Run(string(rune('0'+down)), func(t *testing.T) {
			socket := newNativeSocket(t, func(nativeRequest) (any, string) { return map[string]any{"type": "ok"}, "" })
			target := Target{Backend: HerdrBackend, SocketPath: socket.path, Pane: "w1:p2", Engine: "cc"}
			seedNativeOwner(t, target)
			if err := DenyModal(context.Background(), target, down, clock.Real); err != nil {
				t.Fatal(err)
			}
			calls := socket.calls()
			want := make([]string, down+1)
			for i := 0; i < down; i++ {
				want[i] = "Down"
			}
			want[down] = "Enter"
			if len(calls) != len(want) {
				t.Fatalf("requests=%+v want keys=%v", calls, want)
			}
			for i, call := range calls {
				if call.Method != "pane.send_keys" || call.Params["pane_id"] != "w1:p2" ||
					!reflect.DeepEqual(call.Params["keys"], []any{want[i]}) {
					t.Fatalf("request=%+v want=%s", call, want[i])
				}
			}
		})
	}
}
