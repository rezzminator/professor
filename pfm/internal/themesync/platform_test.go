package themesync

import (
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/servicemanager"
)

// Each profile serves its platform's VS Code settings, servers and process
// markers and its follower's service manager; a zero Env serves this host's.
func TestPlatformProfiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("VSCODE_PORTABLE", "")
	cases := []struct {
		name     string
		profile  *platform
		settings []string
		roots    []string
		markers  []string
		follower servicemanager.Kind
	}{
		{
			name:    "darwin",
			profile: darwinPlatform,
			settings: []string{
				"/h/Library/Application Support/Code/User/settings.json",
				"/h/Library/Application Support/Code - Insiders/User/settings.json",
				"/h/Library/Application Support/VSCodium/User/settings.json",
				"/h/Library/Application Support/Cursor/User/settings.json",
			},
			roots:    nil,
			markers:  vscodeAppMarkers,
			follower: servicemanager.KindLaunchd,
		},
		{
			name:    "linux",
			profile: linuxPlatform,
			settings: []string{
				"/h/.config/Code/User/settings.json",
				"/h/.config/Code - Insiders/User/settings.json",
				"/h/.config/VSCodium/User/settings.json",
				"/h/.vscode-server/data/Machine/settings.json",
				"/h/.vscode-server-insiders/data/Machine/settings.json",
			},
			roots:    []string{"/h/.vscode-server", "/h/.vscode-server-insiders"},
			markers:  vscodeLinuxMarkers,
			follower: servicemanager.KindSystemd,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &session{env: Env{Home: "/h", Platform: tc.profile}}
			if got := s.vscodeSettings(); !reflect.DeepEqual(got, tc.settings) {
				t.Errorf("settings = %q, want %q", got, tc.settings)
			}
			if got := s.vscodeServerRoots(); !reflect.DeepEqual(got, tc.roots) {
				t.Errorf("server roots = %q, want %q", got, tc.roots)
			}
			if got := s.env.platform().vscodeMarkers; !reflect.DeepEqual(got, tc.markers) {
				t.Errorf("markers = %q, want %q", got, tc.markers)
			}
			if got := s.env.platform().follower; got != tc.follower {
				t.Errorf("follower = %q, want %q", got, tc.follower)
			}
		})
	}
	if got := (Env{}).platform(); got != hostPlatform {
		t.Errorf("Env{} serves %+v, want hostPlatform %+v", got, hostPlatform)
	}
}
