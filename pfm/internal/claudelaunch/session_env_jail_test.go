//go:build jail

package claudelaunch

import (
	"context"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/deps"
)

// A pane shell running the pane command's PATH words puts its own PATH first and
// the login folders after it; an empty pane PATH gives the login folders alone,
// with no empty entry to search the working directory, and an unset one /bin/sh's
// own default PATH (a shell given no PATH searches its default) ahead of them.
func TestLaunchPaneEnvPATHWordsInARealShell(t *testing.T) {
	_, words := Launch{LoginPATH: []string{"/shims", "/opt/x y"}}.PaneEnv()
	runner := deps.RealRunner{}
	var tools []string
	for _, name := range []string{"bash", "env", "printenv"} {
		path, err := runner.LookPath(name)
		if err != nil {
			t.Fatalf("no %s for the pane shell: %v", name, err)
		}
		tools = append(tools, path)
	}
	bash, env, printenv := tools[0], tools[1], tools[2]
	unset := []string{"LANG=C"}
	defaults, err := runner.Run(context.Background(),
		[]string{"/bin/sh", "-c", `printf %s "$PATH"`}, deps.RunOptions{Env: unset})
	if err != nil || len(defaults.Stdout) == 0 {
		t.Fatalf("/bin/sh's default PATH = %q (err %v), want a non-empty default", defaults.Stdout, err)
	}
	command := env + words + " " + printenv + " PATH"
	for _, shell := range []string{"/bin/sh", bash} {
		for _, test := range []struct {
			name    string
			environ []string
			want    string
		}{
			{"pane PATH set", []string{"PATH=/pane/bin:/usr/bin"}, "/pane/bin:/usr/bin:/shims:/opt/x y\n"},
			{"pane PATH empty", []string{"PATH="}, "/shims:/opt/x y\n"},
			{"pane PATH unset", unset, string(defaults.Stdout) + ":/shims:/opt/x y\n"},
		} {
			t.Run(shell+" "+test.name, func(t *testing.T) {
				argv := []string{shell, "-c", command}
				result, err := runner.Run(context.Background(), argv, deps.RunOptions{Env: test.environ})
				if err != nil || string(result.Stdout) != test.want {
					t.Fatalf("%q under %q printed PATH %q (stderr %q, err %v), want %q",
						argv, test.environ, result.Stdout, result.Stderr, err, test.want)
				}
			})
		}
	}
}
