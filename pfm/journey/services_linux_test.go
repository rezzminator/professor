//go:build journey

package journey

import (
	"strings"

	"github.com/rezzminator/professor/pfm/journey/harness"
)

// installedServices is what pfm's install leaves running: its path and timer units, active.
const installedServices = "pfm-name-sync.path active\npfm-name-sync.timer active\npfm-schedule.timer active"

// services is pfm's units as the owner's systemd user manager lists them, "{unit} {active state}" a line;
// the services a path or timer starts on demand are left out, as their state is only a moment's.
func services(c *harness.Ctx) string {
	out := read(c, "systemctl", "--user", "list-units", "--all", "--no-legend", "--plain", "pfm*")
	var units []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && !strings.HasSuffix(f[0], ".service") {
			units = append(units, f[0]+" "+f[2])
		}
	}
	return strings.Join(units, "\n")
}
