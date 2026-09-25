package engine

import (
	"strings"
	"testing"
)

func TestOpenCodeMCPToolName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		want    string
		wantErr string
	}{
		{name: "shared rule", input: "mcp__professor__harvester_read", want: "professor_harvester_read"},
		{name: "sanitizing", input: "mcp__my.server__do-it", want: "my_server_do-it"},
		{name: "no tool part", input: "mcp__professor", wantErr: `OpenCode MCP tool "mcp__professor" must be mcp__server__tool`},
		{name: "empty server", input: "mcp____read", wantErr: `OpenCode MCP tool "mcp____read" must be mcp__server__tool`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := OpenCodeMCPToolName(tc.input)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("OpenCodeMCPToolName(%q) = %q, %v; want error %q", tc.input, got, err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("OpenCodeMCPToolName(%q) = %q, %v; want %q", tc.input, got, err, tc.want)
			}
		})
	}
}
