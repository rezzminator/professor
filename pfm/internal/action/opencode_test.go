package action

import (
	"strings"
	"testing"

	"hostops/pfm/internal/compose"
	pfmconfig "hostops/pfm/internal/config"
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
