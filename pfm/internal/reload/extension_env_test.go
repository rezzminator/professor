package reload

import (
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

func TestCodexReloadExtensionEnv(t *testing.T) {
	machine := pfmconfig.Config{
		Extensions: map[string]pfmconfig.Extension{
			"ponytail": {
				Enabled: true,
				Engines: []string{"codex"},
				Env:     map[string]string{"PONYTAIL_SUBAGENT_MATCHER": "^worker$"},
			},
		},
	}
	got, err := codexRun(Request{Machine: machine})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "PONYTAIL_SUBAGENT_MATCHER='^worker$'") {
		t.Fatalf("extension env lost at reload: %s", got)
	}
}

func TestCodexReloadExtensionOwnership(t *testing.T) {
	t.Setenv("PONYTAIL_USER_EXPORT", "user")
	t.Setenv("PONYTAIL_SUBAGENT_MATCHER", "inherited")
	for _, mode := range []string{"on", "off", "dropped"} {
		t.Run(mode, func(t *testing.T) {
			ext := pfmconfig.Extension{
				Enabled: mode != "off",
				Engines: []string{"codex"},
				Env:     map[string]string{"PONYTAIL_SUBAGENT_MATCHER": "configured"},
			}
			if mode == "dropped" {
				ext.Engines = nil
			}
			got, err := codexRun(
				Request{Machine: pfmconfig.Config{Extensions: map[string]pfmconfig.Extension{"ponytail": ext}}},
			)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(got, "-u PONYTAIL_USER_EXPORT") {
				t.Errorf("reload strips unowned export: %s", got)
			}
			if mode == "on" {
				if !strings.Contains(got, "PONYTAIL_SUBAGENT_MATCHER='configured'") {
					t.Errorf("reload configured key not applied: %s", got)
				}
			} else if strings.Contains(got, "-u PONYTAIL_SUBAGENT_MATCHER") || strings.Contains(got, "PONYTAIL_SUBAGENT_MATCHER=") {
				t.Errorf("inactive reload changes inherited key: %s", got)
			}
		})
	}
}
