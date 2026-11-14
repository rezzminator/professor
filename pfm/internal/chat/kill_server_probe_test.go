package chat

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	pfmtmux "github.com/rezzminator/professor/pfm/internal/tmux"
)

func hungValues(t *testing.T) paths.Values {
	t.Helper()
	root := testjail.ShortRoot(t)
	values := paths.Values{
		TmuxDir: filepath.Join(root, "t"),
		SIDDir:  filepath.Join(root, "s"),
		StateDB: filepath.Join(root, "pfm.db"),
	}
	for _, dir := range []string{values.TmuxDir, values.SIDDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return values
}

// TestKillServerKeepsAnUnresponsiveServer: a kill-server that failed against a
// server that then does not answer has not ended it; reading the silence as
// "dead" removed the socket of a server that may still hold the chat.
func TestKillServerKeepsAnUnresponsiveServer(t *testing.T) {
	ctx := pfmtmux.WithClientRunner(context.Background(), testjail.HungTmuxRunner(1))
	values := hungValues(t)
	socket := "cc-hung-1-1"
	socketPath := filepath.Join(values.TmuxDir, socket)
	if err := os.WriteFile(socketPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := KillServer(ctx, values, socket, "%0")
	if err == nil || !strings.Contains(err.Error(), "unresponsive") {
		t.Fatalf("KillServer() over an unresponsive server = %v, want an unresponsive error", err)
	}
	if _, statErr := os.Stat(socketPath); statErr != nil {
		t.Fatalf("KillServer() removed the socket of an unresponsive server: %v", statErr)
	}
}

// TestEndNowRefusesAnUnresponsiveServer: EndNow returns nil only once tmux says
// the server is gone; a server that stops answering is not proof of an end.
func TestEndNowRefusesAnUnresponsiveServer(t *testing.T) {
	ctx := pfmtmux.WithClientRunner(context.Background(), testjail.HungTmuxRunner(0))
	values := hungValues(t)
	chat := headless.Chat{ID: "sid-hung", Socket: "cc-hung-1-1", Pane: "%0"}
	err := EndNow(ctx, chat, values)
	if err == nil || !strings.Contains(err.Error(), "unresponsive") {
		t.Fatalf("EndNow() over an unresponsive server = %v, want an unresponsive error", err)
	}
}

// TestEndResolvedRoutesATmuxEndThroughTheOneProbe: the CLI's end door ends a
// tmux chat through endTmux, so a server that stops answering after its
// kill-server is an unconfirmed end, never "ended".
func TestEndResolvedRoutesATmuxEndThroughTheOneProbe(t *testing.T) {
	ctx := pfmtmux.WithClientRunner(context.Background(), testjail.HungTmuxRunner(0))
	values := hungValues(t)
	chat := headless.Chat{ID: "sid-hung", Socket: "cc-hung-1-1", Pane: "%0"}
	var stdout, stderr strings.Builder
	code := EndResolved(ctx, chat, &stdout, &stderr, &config.Runtime{Paths: values})
	if code == 0 || !strings.Contains(stderr.String(), "unresponsive") || strings.Contains(stdout.String(), "ended") {
		t.Fatalf("EndResolved() over an unresponsive server = %d, stdout %q, stderr %q; want an unresponsive failure",
			code, stdout.String(), stderr.String())
	}
}
