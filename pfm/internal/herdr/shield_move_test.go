package herdr

import (
	"context"
	"reflect"
	"strings"
	"testing"
)

// nestedListing is shieldListing("running", true, false, "PROTECTED") plus
// holder h2 (pid 400, child 410) bound to w1:p4 too, listed first.
func nestedListing() string {
	listing := shieldListing("running", true, false, "PROTECTED")
	outer := `{"id":"h2","holder_live":true,"state":"running","orphan":false,` +
		`"holder":{"pid":400,"start":40},"child":{"pid":410,"start":41},` +
		`"binding":{"pane":"w1:p4","socket":"` + shieldSocket + `"}},`
	return strings.Replace(listing, `"holders":[`, `"holders":[`+outer, 1)
}

// nestedTable is shieldTable plus h2's processes: its child zsh 410 runs, in
// its foreground group 420, the attach client given by attach.
func nestedTable(attach []string) shieldProc {
	table := shieldTable()
	for pid, process := range map[int]struct {
		stat ProcStat
		argv []string
	}{
		400: {ProcStat{Start: 40, Group: 400, TerminalGroup: -1}, []string{"herdr-shield", "holder"}},
		410: {ProcStat{Start: 41, Group: 410, TerminalGroup: 420}, []string{"-zsh"}},
		420: {ProcStat{Start: 42, Group: 420, TerminalGroup: 420}, attach},
	} {
		table[pid] = process
	}
	return table
}

// `herdr-shield attach h1` run in the held shell of h2, the pane's own holder,
// binds both to the pane: the pane's chat is in h1, the holder h2 attaches.
func TestShieldProcessesFollowsAMovedHolder(t *testing.T) {
	for _, tc := range []struct {
		name, diagnostic string
		attach           []string
	}{
		{name: "holder attached from another held shell", attach: []string{"/usr/bin/herdr-shield", "attach", "h1"}},
		{
			name: "two holders on one pane, neither attaching", attach: []string{"vim"},
			diagnostic: "herdr pane w1:p4: herdr-shield binds holders h2 and h1, neither attaching the other",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(EnvShieldInner, "h1")
			t.Setenv(EnvSocket, shieldRelay)
			runner := shieldRunner(t, nestedListing(), 0)
			got, err := (&Shield{Runner: runner, Proc: nestedTable(tc.attach)}).Processes(
				context.Background(), shieldSocket, processInfo("w1:p4", attachClient),
			)
			if tc.diagnostic != "" {
				if err == nil || err.Error() != tc.diagnostic {
					t.Fatalf("Processes = %+v, %v; want error %q", got, err, tc.diagnostic)
				}
				return
			}
			if err != nil || got.Holder == nil || got.Holder.ID != "h1" {
				t.Fatalf("Processes = %+v, %v; want holder h1", got, err)
			}
			want := PaneProcesses{
				Pane: "w1:p4", Shell: 310,
				Foreground: []Process{
					{
						PID:   320,
						Name:  "pfm",
						Argv0: "/opt/fixture/pfm",
						Argv:  []string{"/opt/fixture/pfm", "chat", "open"},
					},
				},
			}
			got.Holder = nil
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("Processes = %+v, want %+v", got, want)
			}
		})
	}
}
