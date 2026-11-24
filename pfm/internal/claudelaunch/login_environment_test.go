package claudelaunch

import (
	"slices"
	"testing"
)

func TestLoginEnvironmentKeepsLoginVariablesAndHome(t *testing.T) {
	environ := []string{
		"HOME=/h/me",
		"PATH=/pfm/bin",
		"LC_ALL=C",
		"XDG_DATA_HOME=/h/me/.local",
		"PFM_X=1",
		"SHELL=/bin/bash",
	}
	got := loginEnvironment(environ, "/bin/zsh")
	want := []string{
		"HOME=/h/me",
		"LC_ALL=C",
		"XDG_DATA_HOME=/h/me/.local",
		"SHELL=/bin/zsh",
		"PATH=/usr/bin:/bin:/usr/sbin:/sbin",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("loginEnvironment = %q, want %q", got, want)
	}
	for _, test := range []struct {
		name string
		env  []string
		want string
	}{
		{"home set", got, "/h/me"},
		{"home empty", []string{"HOME=", "SHELL=/bin/zsh"}, ""},
		{"home absent", []string{"SHELL=/bin/zsh"}, ""},
		{"home not a prefix match", []string{"HOMEDIR=/x"}, ""},
	} {
		if home := loginHome(test.env); home != test.want {
			t.Fatalf("%s: loginHome(%q) = %q, want %q", test.name, test.env, home, test.want)
		}
	}
}
