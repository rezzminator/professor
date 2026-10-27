package doctor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/config"
)

func TestExtensionsDoctorUnreadableStateIsFailureWithRepair(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "seat")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte("broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	machine := config.Config{
		Extensions: map[string]config.Extension{"ponytail": {Enabled: true, Engines: []string{"claude"}}},
	}
	var out bytes.Buffer
	tally := &doctorTally{}
	printExtensionsDoctor(context.Background(), &out, home, dir, machine, tally)
	for _, aspect := range []string{"installed", "enabled", "options", "env"} {
		if !strings.Contains(out.String(), "ponytail/claude "+aspect+" FAIL state unreadable:") {
			t.Fatalf("unreadable %s rendered absent/clean: %s", aspect, out.String())
		}
	}
	if tally.failures < 4 || !strings.Contains(out.String(), "run pfm install --yes") {
		t.Fatalf("repair/failures missing: %s %+v", out.String(), tally)
	}
}
