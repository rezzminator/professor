package doctor

import (
	"bytes"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestHerdrIndicatorsDoctor(t *testing.T) {
	for _, name := range []string{"stale", "current", "absent", "foreign", "another install", "unreadable table", "unreadable pid", "unreadable image", "stale beside unreadable"} {
		t.Run(name, func(t *testing.T) {
			runtime, fresh, old := newMCPServeDoctorFixture(t)
			const pid = 5000001
			table := &mcpServeIdentityTable{
				mcpServeProcTable: mcpServeProcTable{
					pids:     []int{pid},
					cmdlines: map[int][]string{pid: {"pfm", "internal", "herdr-indicators"}},
					images:   map[int]gather.FileID{pid: old},
					environs: map[int]map[string]string{
						pid: {paths.EnvInstallMark: paths.InstallMark(runtime.Paths.Home)},
					},
				},
				identities:   map[int]gather.ProcessIdentity{pid: {EffectiveUID: uint32(os.Geteuid())}},
				commandReads: map[int]int{},
			}
			wantWarnings, wantFailures, want := 0, 0, "ok process enumeration confirms no stale painter"
			wantAlso := ""
			switch name {
			case "stale":
				wantWarnings, want = 1, "WARN stale executable pids=[5000001] — fix: pfm install --yes"
			case "current":
				table.images[pid] = fresh
			case "absent":
				table.pids = nil
			case "foreign":
				table.identities[pid] = gather.ProcessIdentity{EffectiveUID: uint32(os.Geteuid()) + 1}
			case "another install":
				table.environs[pid][paths.EnvInstallMark] = "another-install-mark"
			case "unreadable table":
				table.pidsErr = fs.ErrPermission
				wantFailures, want = 1, "ERROR herdr indicator process check: list processes"
			case "unreadable pid":
				table.cmdlineErr = map[int]error{pid: fs.ErrPermission}
				wantFailures, want = 1, "ERROR herdr indicator process check: 1 process(es)"
			case "unreadable image":
				table.imageErr = map[int]error{pid: fs.ErrPermission}
				wantFailures, want = 1, "ERROR herdr indicator pid 5000001 executable identity"
			case "stale beside unreadable":
				const unreadable = 5000002
				table.pids = append(table.pids, unreadable)
				table.cmdlines[unreadable] = []string{"pfm", "internal", "herdr-indicators"}
				table.environs[unreadable] = map[string]string{
					paths.EnvInstallMark: paths.InstallMark(runtime.Paths.Home),
				}
				table.identities[unreadable] = gather.ProcessIdentity{EffectiveUID: uint32(os.Geteuid())}
				table.imageErr = map[int]error{unreadable: fs.ErrPermission}
				wantWarnings, wantFailures = 1, 1
				want = "doctor: herdr-indicators: WARN stale executable pids=[5000001] — fix: pfm install --yes\n"
				wantAlso = "doctor: herdr-indicators: ERROR herdr indicator pid 5000002 executable identity"
			}
			var output bytes.Buffer
			warnings, failures := printHerdrIndicatorsDoctor(&output, runtime.Paths.Home, table)
			if warnings != wantWarnings || failures != wantFailures || !strings.Contains(output.String(), want) ||
				!strings.Contains(output.String(), wantAlso) {
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
