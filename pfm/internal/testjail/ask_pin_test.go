package testjail

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
)

func TestPinClaudeAsk(t *testing.T) {
	for _, test := range []struct {
		name      string
		installed bool
		content   string
		wantAsk   map[string]any
	}{
		{
			name:      "installed_home",
			installed: true,
		},
		{
			name:    "other_ask_keys",
			content: `{"version":2,"ask":{"claude":{"model":"claude-haiku-4-5"}}}`,
			wantAsk: map[string]any{
				"engine": "claude",
				"claude": map[string]any{"model": "claude-haiku-4-5"},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var home string
			if test.installed {
				home = filepath.Join(InstalledHome(t), "home")
			} else {
				home = t.TempDir()
				if err := os.WriteFile(
					filepath.Join(home, "pfm.config.json"),
					[]byte(test.content),
					0o600,
				); err != nil {
					t.Fatal(err)
				}
			}

			PinClaudeAsk(t, home)
			if test.installed {
				runtime, err := config.LoadRuntime("")
				if err != nil {
					t.Fatal(err)
				}
				if runtime.Config.Ask.Engine != pfmengine.Claude ||
					runtime.Config.Source("ask.engine") != config.SourceFile ||
					len(runtime.Config.Accounts) != 1 || runtime.Config.Accounts[0].ID != 1 {
					t.Fatalf("ask engine=%s source=%s accounts=%+v",
						runtime.Config.Ask.Engine, runtime.Config.Source("ask.engine"), runtime.Config.Accounts)
				}
				return
			}

			raw, err := os.ReadFile(filepath.Join(home, "pfm.config.json"))
			if err != nil {
				t.Fatal(err)
			}
			var document struct {
				Version int            `json:"version"`
				Ask     map[string]any `json:"ask"`
			}
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			if document.Version != 2 || !reflect.DeepEqual(document.Ask, test.wantAsk) {
				t.Fatalf("version=%d ask=%v, want version=2 ask=%v", document.Version, document.Ask, test.wantAsk)
			}
		})
	}
}
