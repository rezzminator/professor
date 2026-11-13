package installer

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallManagedCommandsQuoteSpacedPath: the macOS managed dir holds a
// space, so the echoed sudo line and the set-it-by-hand advisory must each
// keep the path one shell word — run through sh, the advisory writes the
// drop-in at the path the classifier reads, every key the drop-in already
// held (a quote in its value included) kept.
func TestInstallManagedCommandsQuoteSpacedPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "Application Support", "ClaudeCode", "managed-settings.d")
	managed := filepath.Join(dir, "pfm.json")
	writeFixture(t, managed, `{"cleanupPeriodDays":30,"companyAnnouncements":["it's pfm's"]}`)
	var output bytes.Buffer
	installer := &engine{
		apply: true,
		options: Options{
			ManagedSettingsDir:    dir,
			CleanupPeriodDays:     36500,
			RequireManagedCleanup: true,
			Runner:                stateRunner{err: os.ErrPermission},
			Stdout:                &output,
			writeManaged:          func(string, []byte) error { return os.ErrPermission },
		},
	}
	if err := installer.installManagedCleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	var echoed, advisory string
	for _, line := range strings.Split(output.String(), "\n") {
		if echoed == "" && strings.HasPrefix(line, "sudo -n ") {
			echoed = line
		}
		if _, command, found := strings.Cut(line, "; run: "); found {
			advisory = command
		}
	}
	if echoed == "" || advisory == "" {
		t.Fatalf("sudo echo or advisory missing:\n%s", output.String())
	}
	words := parseShellWords(t, echoed)
	if last := words[len(words)-1]; last != managed && last != dir {
		t.Fatalf("echoed sudo line split the managed path: last word %q of %q", last, words)
	}
	run := exec.Command("sh", "-c", strings.ReplaceAll(advisory, "sudo ", ""))
	run.Dir = t.TempDir()
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("advisory %q failed: %v\n%s", advisory, err, out)
	}
	want := `{"cleanupPeriodDays":36500,"companyAnnouncements":["it's pfm's"],"prependPlugins":[]}` + "\n"
	if got, err := os.ReadFile(managed); err != nil || string(got) != want {
		t.Fatalf("advisory wrote %q (err %v), want %q at %s", got, err, want, managed)
	}
	if status := InspectManagedCleanup(dir, true, 36500); status.State != "ok" {
		t.Fatalf("managed after advisory=%+v", status)
	}
}

func parseShellWords(t *testing.T, line string) []string {
	t.Helper()
	out, err := exec.Command("sh", "-c", "set -- "+line+"\nprintf '%s\\n' \"$@\"").Output()
	if err != nil {
		t.Fatalf("sh could not parse %q: %v", line, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}
