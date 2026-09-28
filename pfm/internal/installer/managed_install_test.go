package installer

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestInstallManagedArgsWriteSpacedDestination runs this kernel's argv with
// its real tools (no sudo) into a destination holding a space and a missing
// parent: GNU install -D on Linux, mkdir -p then BSD-portable install on macOS.
func TestInstallManagedArgsWriteSpacedDestination(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source.json")
	want := "{\"cleanupPeriodDays\":36500}\n"
	if err := os.WriteFile(source, []byte(want), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "Application Support", "ClaudeCode", "managed-settings.d", "pfm.json")
	for _, args := range managedInstallArgs(source, destination) {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%q: %v\n%s", args, err, out)
		}
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(destination); err != nil || string(got) != want || info.Mode().Perm() != 0o644 {
		t.Fatalf("installed %q mode %v (err %v), want %q mode 0644", got, info.Mode().Perm(), err, want)
	}
}

// TestLayoutManagedCommandsQuoteSpacedPath: the macOS managed dir holds a
// space, so the echoed sudo line and the set-it-by-hand advisory must each
// keep the path one shell word — run through sh, the advisory writes the
// drop-in at the path the classifier reads.
func TestLayoutManagedCommandsQuoteSpacedPath(t *testing.T) {
	env := layoutFixture(t)
	env.ManagedDir = filepath.Join(env.Home, "Application Support", "ClaudeCode", "managed-settings.d")
	managed := filepath.Join(env.ManagedDir, "pfm.json")
	env.writeManaged = func(string, []byte) error { return os.ErrPermission }
	env.runner = &layoutTestRunner{failSudo: true}
	var output bytes.Buffer
	if _, err := ApplyLayout(
		context.Background(),
		env,
		NewJournal(context.Background(), env),
		true,
		&output,
	); err != nil {
		t.Fatal(err)
	}
	var echoed, advisory string
	for _, line := range strings.Split(output.String(), "\n") {
		if echoed == "" && strings.HasPrefix(line, "sudo -n ") {
			echoed = line
		}
		if _, command, found := strings.Cut(line, "sudo -n needs cached credentials; run: "); found {
			advisory = command
		}
	}
	if echoed == "" || advisory == "" {
		t.Fatalf("sudo echo or advisory missing:\n%s", output.String())
	}
	words := parseShellWords(t, echoed)
	if last := words[len(words)-1]; last != managed && last != env.ManagedDir {
		t.Fatalf("echoed sudo line split the managed path: last word %q of %q", last, words)
	}
	run := exec.Command("sh", "-c", strings.ReplaceAll(advisory, "sudo ", ""))
	run.Dir = t.TempDir()
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("advisory %q failed: %v\n%s", advisory, err, out)
	}
	want := fmt.Sprintf("{\"cleanupPeriodDays\":%d}\n", env.Config.Claude.CleanupPeriodDays)
	if got, err := os.ReadFile(managed); err != nil || string(got) != want {
		t.Fatalf("advisory wrote %q (err %v), want %q at %s", got, err, want, managed)
	}
	if finding := layoutFindingByPath(
		ClassifyLayout(env),
		layoutRowManagedCleanup,
		managed,
	); finding.Verdict != VerdictOK {
		t.Fatalf("managed row after advisory=%+v", finding)
	}
}

// parseShellWords returns the words a POSIX shell parses from line.
func parseShellWords(t *testing.T, line string) []string {
	t.Helper()
	out, err := exec.Command("sh", "-c", "set -- "+line+"\nprintf '%s\\n' \"$@\"").Output()
	if err != nil {
		t.Fatalf("sh could not parse %q: %v", line, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}
