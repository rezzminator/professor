package hookentry

import "testing"

func TestHerdrInFlightMessage(t *testing.T) {
	tests := []struct {
		name   string
		agents []string // open turns the lifecycle hooks counted
		tasks  []string // the Stop payload's background task types
		want   string
	}{
		{"nothing in flight", nil, nil, ""},
		{"one of a kind", nil, []string{"shell"}, "1 shell running"},
		{
			"every kind, in the message's order",
			nil,
			[]string{"MCP task", "workflow", "monitor", "monitor", "shell", "subagent"},
			"1 agent, 1 shell, 2 monitors, 1 workflow, 1 MCP task running",
		},
		{
			"open turns outnumber listed agents",
			[]string{"a", "b"},
			[]string{"subagent", "workflow"},
			"2 agents, 1 workflow running",
		},
		{"listed agents outnumber open turns", []string{"a"}, []string{"subagent", "subagent"}, "2 agents running"},
		{"agents the hooks counted alone", []string{"a"}, nil, "1 agent running"},
		{"tasks that never wake the chat", nil, []string{"dream", "auto-mode scan", "cloud session"}, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stored := herdrStored{}
			for _, agent := range test.agents {
				if stored.Agents == nil {
					stored.Agents = map[string]bool{}
				}
				stored.Agents[agent] = true
			}
			var tasks []herdrBackgroundTask
			for _, kind := range test.tasks {
				tasks = append(tasks, herdrBackgroundTask{Type: kind})
			}
			stored.Background = herdrBackgroundCounts(tasks)
			if got := herdrInFlightMessage(stored); got != test.want {
				t.Fatalf("message = %q; want %q (background %v)", got, test.want, stored.Background)
			}
			if got, want := herdrInFlight(stored), test.want != ""; got != want {
				t.Fatalf("in flight = %v; want %v (background %v)", got, want, stored.Background)
			}
		})
	}
}
