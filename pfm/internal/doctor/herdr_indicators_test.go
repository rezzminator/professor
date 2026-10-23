package doctor

import (
	"bytes"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
)

func TestHerdrIndicatorsDoctor(t *testing.T) {
	for _, name := range []string{"stale", "current", "absent", "foreign", "unreadable table", "unreadable pid", "unreadable image"} {
		t.Run(name, func(t *testing.T) {
			runtime, fresh, old := newMCPServeDoctorFixture(t)
			const pid = 5000001
			table := &mcpServeIdentityTable{
				mcpServeProcTable: mcpServeProcTable{
					pids:     []int{pid},
					cmdlines: map[int][]string{pid: {"pfm", "internal", "herdr-indicators"}},
					images:   map[int]gather.FileID{pid: old},
				},
				identities:   map[int]gather.ProcessIdentity{pid: {EffectiveUID: uint32(os.Geteuid())}},
				commandReads: map[int]int{},
			}
			wantWarnings, wantFailures, want := 0, 0, "ok process enumeration confirms no stale painter"
			switch name {
			case "stale":
				wantWarnings, want = 1, "WARN stale executable pids=[5000001] — fix: pfm install --yes"
			case "current":
				table.images[pid] = fresh
			case "absent":
				table.pids = nil
			case "foreign":
				table.identities[pid] = gather.ProcessIdentity{EffectiveUID: uint32(os.Geteuid()) + 1}
			case "unreadable table":
				table.pidsErr = fs.ErrPermission
				wantFailures, want = 1, "ERROR herdr indicator process check: list processes"
			case "unreadable pid":
				table.cmdlineErr = map[int]error{pid: fs.ErrPermission}
				wantFailures, want = 1, "ERROR herdr indicator process check: 1 process(es)"
			case "unreadable image":
				table.imageErr = map[int]error{pid: fs.ErrPermission}
				wantFailures, want = 1, "ERROR herdr indicator pid 5000001 executable identity"
			}
			var output bytes.Buffer
			warnings, failures := printHerdrIndicatorsDoctor(&output, runtime.Paths.Home, table)
			if warnings != wantWarnings || failures != wantFailures || !strings.Contains(output.String(), want) {
				t.Fatalf(
					"counts=%d,%d output=%s; want=%d,%d %s",
					warnings,
					failures,
					output.String(),
					wantWarnings,
					wantFailures,
					want,
				)
			}
		})
	}
}
