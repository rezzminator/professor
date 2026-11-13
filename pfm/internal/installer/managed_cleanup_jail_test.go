package installer

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/obs"
)

func TestInstallManagedCleanupDeclinedSudo(t *testing.T) {
	_, recorder := obs.Test(t)
	dir := filepath.Join(t.TempDir(), "Application Support", "managed-settings.d")
	path := filepath.Join(dir, "pfm.json")
	var output bytes.Buffer
	installer := &engine{
		apply: true,
		options: Options{
			Mode:                  ModeApply,
			ManagedSettingsDir:    dir,
			RequireManagedCleanup: true,
			CleanupPeriodDays:     36500,
			Runner:                stateRunner{err: os.ErrPermission},
			Stdout:                &output,
			writeManaged:          func(string, []byte) error { return os.ErrPermission },
		},
	}
	if err := installer.installManagedCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "sudo -n ") {
		t.Fatalf("output=%q want two lines starting with sudo -n", output.String())
	}
	words := parseShellWords(t, lines[0])
	args := append([]string{"sudo", "-n"}, managedInstallArgs(words[len(words)-2], path)[0]...)
	if !reflect.DeepEqual(words, args) {
		t.Fatalf("sudo argv=%q want=%q", words, args)
	}
	want := "  warn    managed-cleanup " + path + " not written: permission denied; " + lines[0] + ": permission denied; run: sudo mkdir -p " + shellCommandLine(
		dir,
	) + " && printf '%s\\n' '{\"cleanupPeriodDays\":36500,\"prependPlugins\":[]}' | sudo tee " + shellCommandLine(
		path,
	) + " >/dev/null"
	if lines[1] != want || installer.report.Changed != 0 {
		t.Fatalf("output=%q want warning=%q", output.String(), want)
	}
	records := recorder.Records()
	if len(records) != 1 || records[0].Message != "installer.step" {
		t.Fatalf("records=%s", recorder.Raw())
	}
	for key, want := range map[string]any{
		obs.FieldComp: "installer", "kind": "apply", "decision": "warn",
		"step": "managed-cleanup " + path + " not written", obs.FieldErr: "permission denied\npermission denied",
	} {
		if got, _ := records[0].Field(key); got != want {
			t.Fatalf("record %s=%v want=%v", key, got, want)
		}
	}
}
