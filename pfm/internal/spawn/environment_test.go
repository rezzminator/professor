package spawn

import (
	"context"
	"reflect"
	"testing"
)

func TestBackendEnvironmentDoesNotUseDaemonEnvironment(t *testing.T) {
	daemon := []string{"HERDR_ENV=1", "HERDR_SOCKET_PATH=/daemon/herdr.sock", "HERDR_PANE_ID=w9:p9"}
	for _, tc := range []struct {
		name    string
		bind    bool
		environ []string
		want    []string
	}{
		{name: "direct CLI", want: daemon},
		{name: "MCP without ambient identity", bind: true, want: []string{}},
		{name: "explicit request environment", bind: true, environ: []string{"TMUX=/caller/tmux"}, want: []string{"TMUX=/caller/tmux"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			if tc.bind {
				ctx = WithBackendEnvironment(ctx, tc.environ)
			}
			got := BackendEnvironment(ctx, daemon)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("backend environment=%v want=%v", got, tc.want)
			}
		})
	}
}
