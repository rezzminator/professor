package action

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestSynthesizeNewOpenCodeLaunchesFreshConfiguredSeat(t *testing.T) {
	machine := pfmconfig.Config{
		Version:          pfmconfig.Version,
		OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 1, Home: "/opencode"}},
		OpenCode:         pfmconfig.OpenCode{Binary: "/opt/opencode stable"},
		Sources:          map[string]pfmconfig.Source{"opencode.binary": pfmconfig.SourceFile},
	}
	plan, err := Synthesize(Request{
		Row:            compose.Row{Kind: compose.NewOpenCode, CWD: "/work/nuts"},
		PrimaryAccount: 1,
		FreshSocket:    "ox-1-2-4",
		Config:         machine,
	})
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if plan.Route != NewOpenCode {
		t.Fatalf("route = %v, want %v", plan.Route, NewOpenCode)
	}
	if !strings.Contains(plan.Run, Quote(machine.OpenCode.Binary)) || strings.Contains(plan.Run, "--session") {
		t.Fatalf("fresh OpenCode run = %q", plan.Run)
	}
	if !strings.HasPrefix(plan.Run, opencodeHygiene+" ") || !strings.Contains(plan.Run, " -u CLAUDE_CODE_SSE_PORT ") {
		t.Fatalf("fresh OpenCode run = %q, want the OpenCode hygiene prefix", plan.Run)
	}
	if plan.Line != "TMUX= tmux -L 'ox-1-2-4' attach -t 'ox-1-2-4'" ||
		plan.ChatServer == nil || plan.ChatServer.CWD != "/work/nuts" || plan.ChatServer.Window != "OpenCode" {
		t.Fatalf("fresh OpenCode line = %q server = %#v", plan.Line, plan.ChatServer)
	}
}

func TestSynthesizeNewOpenCodeRequiresSeatAndLaunchContext(t *testing.T) {
	machine := pfmconfig.Config{
		Version:          pfmconfig.Version,
		OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 1, Home: "/opencode"}},
		OpenCode:         pfmconfig.OpenCode{Binary: "opencode"},
	}
	for _, test := range []struct {
		name    string
		request Request
		want    string
	}{
		{
			name: "account must be configured",
			request: Request{
				Row:            compose.Row{Kind: compose.NewOpenCode, CWD: "/work"},
				PrimaryAccount: 9,
				FreshSocket:    "ox-1",
				Config:         machine,
			},
			want: "account 9 is not in the configured roster",
		},
		{
			name: "cwd is required",
			request: Request{
				Row:            compose.Row{Kind: compose.NewOpenCode},
				PrimaryAccount: 1,
				FreshSocket:    "ox-1",
				Config:         machine,
			},
			want: "requires cwd and fresh socket",
		},
		{
			name: "fresh socket is required",
			request: Request{
				Row:            compose.Row{Kind: compose.NewOpenCode, CWD: "/work"},
				PrimaryAccount: 1,
				Config:         machine,
			},
			want: "requires cwd and fresh socket",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Synthesize(test.request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestSynthesizeResumeOpenCodeLaunchesTheSession(t *testing.T) {
	plan, err := Synthesize(Request{
		Row: compose.Row{
			Kind: compose.ResumeOpenCode,
			ID:   "ses_abc123",
			CWD:  "/work/nuts",
			Name: "math chat",
		},
		FreshSocket: "ox-1-2-3",
		Bunker:      true,
	})
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if plan.Route != ResumeOpenCode {
		t.Fatalf("route = %v, want %v", plan.Route, ResumeOpenCode)
	}
	if !strings.Contains(plan.Run, "--session") || !strings.Contains(plan.Run, "/work/nuts") {
		t.Errorf("run misses the session resume: %q", plan.Run)
	}
	if plan.Line != "TMUX= exec tmux -L 'ox-1-2-3' attach -t 'ox-1-2-3'" {
		t.Errorf("line is not the bunker attach to the fresh server: %q", plan.Line)
	}
	if plan.ChatServer == nil || plan.ChatServer.Socket != "ox-1-2-3" || plan.ChatServer.CWD != "/work/nuts" {
		t.Errorf("server misses socket or cwd: %#v", plan.ChatServer)
	}
	if strings.Contains(plan.Run, "--dangerously") {
		t.Errorf("OpenCode launch must not carry Claude/Codex autonomy flags: %q", plan.Run)
	}
}

func TestSynthesizeResumeOpenCodeRequiresIdentity(t *testing.T) {
	for _, request := range []Request{
		{Row: compose.Row{Kind: compose.ResumeOpenCode, CWD: "/w"}, FreshSocket: "ox-1"},
		{Row: compose.Row{Kind: compose.ResumeOpenCode, ID: "s"}, FreshSocket: "ox-1"},
		{Row: compose.Row{Kind: compose.ResumeOpenCode, ID: "s", CWD: "/w"}},
	} {
		if _, err := Synthesize(request); err == nil {
			t.Errorf("request %+v: expected an error", request)
		}
	}
}

func TestSynthesizeLiveOpenCodeAttachesItsPane(t *testing.T) {
	plan, err := Synthesize(Request{
		Row: compose.Row{
			Kind: compose.LiveOpenCode, ID: "ses_live",
			Socket: "ox-1-2-4", SessionName: "ox-1-2-4", PaneID: "%0",
		},
		Config: pfmconfig.Config{Version: pfmconfig.Version},
	})
	if err != nil {
		t.Fatalf("synthesize: %v", err)
	}
	if plan.Route != Live {
		t.Fatalf("route = %v, want %v — a running OpenCode seat is attached, not relaunched", plan.Route, Live)
	}
	if plan.Line != "TMUX= tmux -L 'ox-1-2-4' attach -t 'ox-1-2-4'" {
		t.Fatalf("live OpenCode line = %q", plan.Line)
	}
	if plan.Run != "" || plan.ChatServer != nil {
		t.Fatalf("live OpenCode plan spawns something: run=%q server=%#v", plan.Run, plan.ChatServer)
	}
}

// A RUNNING OpenCode seat is attached through the live branch of Open, and a
// live seat whose server has since died demotes to ITS OWN engine's resume
// route — never to Claude's, which is what the untyped else branch used to do
// to anything that was not Codex.
func TestOpenLiveOpenCodeAttachesAndDemotesToItsOwnResume(t *testing.T) {
	jailAction(t)
	tmux := &fakeActionTmux{
		alive: map[string]bool{"ox-100-1-1": true},
		panes: map[string][]ActionPane{
			"ox-100-1-1": {{PaneID: "%0", WindowIndex: 0, CurrentCommand: "opencode"}},
		},
	}
	var stderr bytes.Buffer
	executor, err := New(Dependencies{
		Tmux:      tmux,
		Processes: &fakeProcesses{},
		Gate:      fixedGate(true),
		Runner:    &captureRunner{},
		Stderr:    &stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := Request{
		Row: compose.Row{
			Kind: compose.LiveOpenCode, ID: "ses_live",
			Socket: "ox-100-1-1", SessionName: "ox-100-1-1", PaneID: "%0",
			Name: "live one", CWD: "/work/a",
		},
		PrimaryAccount: 1,
		Home:           "/home/test",
		FreshSocket:    "ox-900-1-1",
		Config: pfmconfig.Config{
			Version:          pfmconfig.Version,
			OpenCodeAccounts: []pfmconfig.OpenCodeAccount{{ID: 1, Home: "/opencode"}},
			OpenCode:         pfmconfig.OpenCode{Binary: "opencode"},
		},
	}
	line, err := executor.Open(context.Background(), request)
	if err != nil {
		t.Fatalf("open live OpenCode: %v", err)
	}
	if line != "TMUX= tmux -L 'ox-100-1-1' attach -t 'ox-100-1-1'" {
		t.Fatalf("live OpenCode line = %q", line)
	}

	request.Row.Socket = "ox-404-1-1"
	line, err = executor.Open(context.Background(), request)
	if err != nil {
		t.Fatalf("open dead OpenCode socket: %v", err)
	}
	if line != "TMUX= tmux -L 'ox-900-1-1' attach -t 'ox-900-1-1'" || len(tmux.created) == 0 {
		t.Fatalf("dead fallback line = %q created = %#v", line, tmux.created)
	}
	born := tmux.created[len(tmux.created)-1]
	if born.Socket != "ox-900-1-1" || !strings.Contains(born.Run, "--session 'ses_live'") {
		t.Fatalf("dead fallback resumed as %#v, want an OpenCode --session resume", born)
	}
}
