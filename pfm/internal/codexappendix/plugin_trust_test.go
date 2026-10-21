package codexappendix

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestPluginHookTrustScope(t *testing.T) {
	const id = "ponytail@ponytail"
	for _, tc := range []struct {
		name, source, key, hash string
		enabled                 bool
		wantErr                 string
	}{
		{"managed", "plugin", id + ":hooks/h.json:session_start:0:0", trustHash, true, ""},
		{"foreign", "plugin", "other@other:hooks/h.json:session_start:0:0", trustHash, true, "unavailable"},
		{"user spoof", "user", id + ":hooks/h.json:session_start:0:0", trustHash, true, "unavailable"},
		{"prefix collision", "plugin", id + "-other:hooks/h.json:session_start:0:0", trustHash, true, "unavailable"},
		{"missing hash", "plugin", id + ":hooks/h.json:session_start:0:0", "", true, "missing hash"},
		{"disabled", "plugin", id + ":hooks/h.json:session_start:0:0", trustHash, false, "not enabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			account, binary, log := stageTrustAccount(t, func(_ string) []listedHook {
				return []listedHook{
					{Key: tc.key, Source: tc.source, CurrentHash: tc.hash, Enabled: true},
					{
						Key:         "foreign@foreign:hooks/h.json:session_start:0:0",
						Source:      "plugin",
						CurrentHash: trustHash,
						Enabled:     true,
					},
				}
			})
			content := "[plugins.\"" + id + "\"]\nenabled = false\n"
			if tc.enabled {
				content = strings.Replace(content, "false", "true", 1)
			}
			if err := os.WriteFile(filepath.Join(account, "config.toml"), []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			err := RegisterPluginHookTrusts(context.Background(), binary, account, id)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want %s, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			writes := 0
			for _, line := range strings.Split(string(raw), "\n") {
				if strings.Contains(line, `"method":"config/value/write"`) {
					writes++
					if strings.Contains(line, "foreign@foreign") || !strings.Contains(line, `hooks.state.`) ||
						!strings.Contains(line, `"trusted_hash":"`+trustHash+`"`) {
						t.Fatalf("wrong trust write: %s", line)
					}
				}
			}
			if writes != 1 {
				t.Fatalf("trust writes=%d want 1: %s", writes, raw)
			}
		})
	}
}

func TestPluginHookTrustPreservesDisabledHooks(t *testing.T) {
	const id = "ponytail@ponytail"
	account, binary, log := stageTrustAccount(t, func(_ string) []listedHook {
		return []listedHook{
			{Key: id + ":disabled", Source: "plugin", CurrentHash: trustHash, Enabled: false},
			{Key: id + ":active", Source: "plugin", CurrentHash: trustHash, Enabled: true},
		}
	})
	if err := os.WriteFile(
		filepath.Join(account, "config.toml"),
		[]byte("[plugins.\""+id+"\"]\nenabled=true\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := RegisterPluginHookTrusts(context.Background(), binary, account, id); err != nil {
		t.Fatal(err)
	}
	writes := writeRequests(t, log)
	if len(writes) != 1 || strings.Contains(writes[0], ":disabled") || !strings.Contains(writes[0], ":active") {
		t.Fatalf("operator-disabled hook changed: %v", writes)
	}
}

func TestPluginHookTrustRejectsDiscoveryDiagnostics(t *testing.T) {
	const id = "ponytail@ponytail"
	for _, diagnostic := range []string{"errors", "warnings"} {
		t.Run(diagnostic, func(t *testing.T) {
			account, binary, log := stageTrustAccount(t, func(_ string) []listedHook {
				return []listedHook{
					{
						Key:         id + ":hook",
						Source:      "plugin",
						CurrentHash: trustHash,
						Enabled:     true,
						TrustStatus: trustedState,
					},
				}
			})
			if err := os.WriteFile(
				filepath.Join(account, "config.toml"),
				[]byte("[plugins.\""+id+"\"]\nenabled=true\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			broken := strings.Replace(
				string(raw),
				`"hooks":[`,
				`"`+diagnostic+`":["fixture discovery failed"],"hooks":[`,
				1,
			)
			if err := testjail.WriteExecutable(binary, []byte(broken), 0o700); err != nil {
				t.Fatal(err)
			}
			for _, probe := range []func(context.Context, string, string, string) error{RegisterPluginHookTrusts, PluginHookTrustState} {
				if err := probe(
					context.Background(),
					binary,
					account,
					id,
				); err == nil ||
					!strings.Contains(err.Error(), "diagnostics") {
					t.Fatalf("discovery %s rendered healthy: %v", diagnostic, err)
				}
			}
			if writes := writeRequests(t, log); len(writes) != 0 {
				t.Fatalf("incomplete discovery wrote trust: %v", writes)
			}
		})
	}
}
