package chat

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/reload"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// The one reload-request builder both the picker's reboot key and the open
// door's account offer run: the account asked for lands in the request, 0 falls
// back to the fleet primary, a Codex row carries its account's home, and an
// unconfigured Codex account refuses before any reload.
func TestRebootNativeBuildsTheReloadRequest(t *testing.T) {
	machine := config.Config{
		Accounts:      []config.Account{{ID: 1, ConfigDir: "/fixture/cc/1"}, {ID: 2, ConfigDir: "/fixture/cc/2"}},
		CodexAccounts: []config.CodexAccount{{ID: 1, Home: "/fixture/cx/1"}},
	}
	claude := compose.Row{
		Kind:   compose.LiveClaude,
		ID:     "sid",
		Name:   "NAME",
		CWD:    "/fixture/cwd",
		Socket: "/fixture/herdr.sock",
		PaneID: "w6:p3",
	}
	codex := claude
	codex.Kind = compose.LiveCodex
	for _, test := range []struct {
		name        string
		row         compose.Row
		account     int
		cache1H     bool
		reloadErr   error
		want        reload.Request
		wantRefused bool
		wantErr     string
	}{
		{
			name: "claude onto the asked account", row: claude, account: 2, cache1H: true,
			want: reload.Request{Engine: pfmengine.Claude, Account: 2, AccountIDs: []int{1, 2}, Cache1H: true},
		},
		{
			name: "no account takes the fleet primary", row: claude,
			want: reload.Request{Engine: pfmengine.Claude, Account: 1, AccountIDs: []int{1, 2}},
		},
		{
			name: "codex carries its account home", row: codex, account: 1,
			want: reload.Request{Engine: pfmengine.Codex, Account: 1, AccountIDs: []int{1}, CodexHome: "/fixture/cx/1"},
		},
		{name: "unconfigured codex account refuses", row: codex, account: 5, wantRefused: true, wantErr: "not configured"},
		{
			name: "reload failure is returned", row: claude, account: 2, reloadErr: errors.New("boom"),
			want: reload.Request{Engine: pfmengine.Claude, Account: 2, AccountIDs: []int{1, 2}}, wantErr: "native reboot: boom",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			testjail.Fleet(t)
			resolved, err := paths.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			var got []reload.Request
			previous := runHerdrReload
			t.Cleanup(func() { runHerdrReload = previous })
			runHerdrReload = func(
				_ context.Context, request reload.Request, _ reload.Options, _ io.Writer,
			) (reload.Result, error) {
				got = append(got, request)
				return reload.Result{}, test.reloadErr
			}
			err = RebootNative(
				context.Background(), resolved, test.row, test.account, test.cache1H, &bytes.Buffer{},
				config.Runtime{Paths: resolved, Config: machine},
			)
			if test.wantErr == "" && err != nil {
				t.Fatalf("RebootNative: %v", err)
			}
			if test.wantErr != "" && (err == nil || !bytes.Contains([]byte(err.Error()), []byte(test.wantErr))) {
				t.Fatalf("RebootNative error=%v want %q", err, test.wantErr)
			}
			if test.wantRefused {
				if len(got) != 0 {
					t.Fatalf("refused reboot still reloaded: %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("reload requests=%+v want one", got)
			}
			request := got[0]
			if request.Engine != test.want.Engine || request.Account != test.want.Account ||
				request.Cache1H != test.want.Cache1H || request.CodexHome != test.want.CodexHome ||
				len(request.AccountIDs) != len(test.want.AccountIDs) ||
				request.SocketPath != test.row.Socket || request.Pane != test.row.PaneID ||
				request.SessionID != test.row.ID || request.CWD != test.row.CWD || request.Name != test.row.Name ||
				request.Home != resolved.Home {
				t.Fatalf("reload request=%+v want %+v for row %+v", request, test.want, test.row)
			}
		})
	}
}
