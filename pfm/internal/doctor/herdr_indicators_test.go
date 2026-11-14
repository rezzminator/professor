package doctor

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestHerdrIndicatorsDoctor(t *testing.T) {
	wants := map[string]string{
		"stale":                   "1 0 doctor: herdr-indicators: WARN stale executable pids=[5000001] — fix: pfm install --yes\n",
		"current":                 "0 0 doctor: herdr-indicators: ok process enumeration confirms no stale painter\n",
		"absent":                  "0 0 doctor: herdr-indicators: ok process enumeration confirms no stale painter\n",
		"foreign":                 "0 0 doctor: herdr-indicators: ok process enumeration confirms no stale painter\n",
		"another install":         "0 0 doctor: herdr-indicators: ok process enumeration confirms no stale painter\n",
		"unreadable table":        "0 1 doctor: herdr-indicators: ERROR herdr indicator process check: list processes: permission denied — fix: repair process-table access, then rerun pfm doctor\n",
		"unreadable pid":          "0 1 doctor: herdr-indicators: ERROR herdr indicator process check: 1 process(es) of this user could not be read, so they may run unstopped: pid 5000001: permission denied — fix: repair process-table access, then rerun pfm doctor\n",
		"unreadable image":        "0 1 doctor: herdr-indicators: ERROR herdr indicator pid 5000001 executable identity: permission denied — fix: repair process-table access, then rerun pfm doctor\n",
		"stale beside unreadable": "1 1 doctor: herdr-indicators: WARN stale executable pids=[5000001] — fix: pfm install --yes\ndoctor: herdr-indicators: ERROR herdr indicator pid 5000002 executable identity: permission denied — fix: repair process-table access, then rerun pfm doctor\n",
	}
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
			switch name {
			case "stale":
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
			case "unreadable pid":
				table.cmdlineErr = map[int]error{pid: fs.ErrPermission}
			case "unreadable image":
				table.imageErr = map[int]error{pid: fs.ErrPermission}
			case "stale beside unreadable":
				const unreadable = 5000002
				table.pids = append(table.pids, unreadable)
				table.cmdlines[unreadable] = []string{"pfm", "internal", "herdr-indicators"}
				table.environs[unreadable] = map[string]string{
					paths.EnvInstallMark: paths.InstallMark(runtime.Paths.Home),
				}
				table.identities[unreadable] = gather.ProcessIdentity{EffectiveUID: uint32(os.Geteuid())}
				table.imageErr = map[int]error{unreadable: fs.ErrPermission}
			}
			var output bytes.Buffer
			warnings, failures := printHerdrIndicatorsDoctor(&output, runtime.Paths.Home, table)
			if got := fmt.Sprintf("%d %d %s", warnings, failures, output.String()); got != wants[name] {
				t.Errorf("counts and output = %q, want %q", got, wants[name])
			}
		})
	}
}
