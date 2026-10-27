package paths

import (
	"strings"
	"testing"
)

func TestVSCodeSettingsCandidatesPerPlatform(t *testing.T) {
	cases := []struct {
		name   string
		darwin bool
		values map[string]string
		want   []string
	}{
		{name: "macos", darwin: true, want: []string{
			"/h/Library/Application Support/Code/User/settings.json",
			"/h/Library/Application Support/Code - Insiders/User/settings.json",
			"/h/Library/Application Support/VSCodium/User/settings.json",
		}},
		{name: "linux desktop and server", want: []string{
			"/h/.config/Code/User/settings.json",
			"/h/.config/Code - Insiders/User/settings.json",
			"/h/.config/VSCodium/User/settings.json",
			"/h/.vscode-server/data/Machine/settings.json",
			"/h/.vscode-server-insiders/data/Machine/settings.json",
		}},
		{
			name:   "linux xdg and portable",
			values: map[string]string{"XDG_CONFIG_HOME": "/x", "VSCODE_PORTABLE": "/p"},
			want: []string{
				"/x/Code/User/settings.json",
				"/x/Code - Insiders/User/settings.json",
				"/x/VSCodium/User/settings.json",
				"/h/.vscode-server/data/Machine/settings.json",
				"/h/.vscode-server-insiders/data/Machine/settings.json",
				"/p/user-data/User/settings.json",
			},
		},
		{
			name:   "relative xdg and portable are ignored",
			values: map[string]string{"XDG_CONFIG_HOME": "x", "VSCODE_PORTABLE": "p"},
			want: []string{
				"/h/.config/Code/User/settings.json",
				"/h/.config/Code - Insiders/User/settings.json",
				"/h/.config/VSCodium/User/settings.json",
				"/h/.vscode-server/data/Machine/settings.json",
				"/h/.vscode-server-insiders/data/Machine/settings.json",
			},
		},
	}
	for _, tc := range cases {
		got := VSCodeSettingsCandidates("/h", tc.darwin, &MapEnv{Values: tc.values})
		if strings.Join(got, "\n") != strings.Join(tc.want, "\n") {
			t.Errorf("%s: candidates =\n%s\nwant\n%s", tc.name, strings.Join(got, "\n"), strings.Join(tc.want, "\n"))
		}
	}
}
