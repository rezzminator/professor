package action

import (
	"fmt"
	"path/filepath"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func stressRequests(home string) []Request {
	machine := testMachineConfig(home)
	machine.OpenCodeAccounts = []pfmconfig.OpenCodeAccount{{
		ID: 1, Home: filepath.Join(home, ".local", "share", "opencode"),
	}}
	machine.OpenCode.Binary = "opencode"
	rows := []compose.Row{
		{Kind: compose.NewClaude, CWD: "/work/project"},
		{Kind: compose.NewCodex, CWD: "/work/project"},
		{Kind: compose.NewOpenCode, CWD: "/work/project"},
		{
			Kind:        compose.LiveClaude,
			ID:          "11111111-1111-4111-8111-111111111111",
			Socket:      "cc-1700000000-123-456",
			SessionName: "live-session",
			CWD:         "/work/project",
		},
		{
			Kind:      compose.Agent,
			ID:        "22222222-2222-4222-8222-222222222222",
			CWD:       "/work/project",
			ConfigDir: pfmconfig.DefaultAccountDir(home, 2),
		},
		{
			Kind: compose.ResumeClaude,
			ID:   "33333333-3333-4333-8333-333333333333",
			CWD:  "/work/project",
		},
		{
			Kind: compose.ResumeOpenCode,
			ID:   "ses_stress_opencode",
			CWD:  "/work/project",
		},
		{
			Kind: compose.ResumeCodex,
			ID:   "44444444-4444-4444-8444-444444444444",
			CWD:  "/work/project",
		},
	}
	requests := make([]Request, 0, len(rows)*2*3*2)
	for index := range rows {
		row := rows[index]
		for _, bunker := range []bool{false, true} {
			accounts := []int{1, 2, 3}
			if row.Kind == compose.NewOpenCode || row.Kind == compose.ResumeOpenCode {
				accounts = []int{1}
			}
			for _, account := range accounts {
				for _, cache1H := range []bool{false, true} {
					freshPrefix := "cc"
					switch row.Kind {
					case compose.NewCodex, compose.ResumeCodex:
						freshPrefix = "cx"
					case compose.NewOpenCode, compose.ResumeOpenCode:
						freshPrefix = "ox"
					}
					requests = append(requests, Request{
						Row:            row,
						PrimaryAccount: account,
						Cache1H:        cache1H,
						Bunker:         bunker,
						Home:           home,
						Config:         machine,
						FreshSocket: fmt.Sprintf(
							"%s-1700000001-123-456",
							freshPrefix,
						),
					})
				}
			}
		}
	}
	return requests
}
