package doctor

import (
	"fmt"
	"io"

	"github.com/rezzminator/professor/pfm/internal/installer"
)

// printHostOverlayDoctor treats every missing, displaced, or unknown overlay
// symlink as a failure: a misdirected or absent overlay is invisible from the
// prompt itself (issue #14 F1).
func printHostOverlayDoctor(stdout io.Writer, home string) (failures int) {
	for _, overlay := range installer.InspectHostOverlays(home) {
		switch overlay.State {
		case installer.HostOverlayOK:
			fmt.Fprintf(stdout, "doctor: host_overlay %s ok\n", overlay.Name)
		case installer.HostOverlayMissing:
			failures++
			fmt.Fprintf(stdout, "doctor: host_overlay %s missing — run pfm install --yes\n", overlay.Name)
		case installer.HostOverlayDisplaced:
			failures++
			fmt.Fprintf(
				stdout,
				"doctor: host_overlay %s DISPLACED by %s — run pfm install --yes\n",
				overlay.Name,
				overlay.Target,
			)
		default:
			failures++
			fmt.Fprintf(
				stdout,
				"doctor: host_overlay %s unknown state=%s — run pfm install --yes\n",
				overlay.Name,
				overlay.State,
			)
		}
	}
	return failures
}
