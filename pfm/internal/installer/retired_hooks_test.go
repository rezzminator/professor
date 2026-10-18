package installer

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/enginesync"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// retiredHookHost is what an install that shipped the gpt agent Stop hook,
// the call-cap hook and the tracetree cut gate left on a host.
type retiredHookHost struct {
	home, managed, receipt string
	// ownedLink is pfm's own link in the default store; foreignLink sits where
	// pfm once linked in an account the config no longer names, which only the
	// receipt remembers, and the operator has pointed it at their own script.
	ownedLink, foreignLink, foreignTarget string
	stateDirs                             map[string]string
}

func seedRetiredHookHost(t *testing.T, receiptBody string) retiredHookHost {
	t.Helper()
	home := t.TempDir()
	root := managedRootForHome(home)
	host := retiredHookHost{
		home:          home,
		managed:       filepath.Join(root, "gpt-agent-stop.py"),
		receipt:       filepath.Join(root, "gpt-hook-ownership.json"),
		ownedLink:     filepath.Join(home, ".claude", "hooks", "gpt-agent-stop.py"),
		foreignLink:   filepath.Join(home, "account-a", "hooks", "gpt-agent-stop.py"),
		foreignTarget: filepath.Join(home, "operator-stop.py"),
	}
	writeFixture(t, host.managed, "retired stop hook")
	writeFixture(t, host.foreignTarget, "operator bytes")
	for link, target := range map[string]string{host.ownedLink: host.managed, host.foreignLink: host.foreignTarget} {
		if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	host.ownedLink, host.foreignLink = physicalHookPath(host.ownedLink), physicalHookPath(host.foreignLink)
	if receiptBody == "" {
		receiptBody = `{"version":1,"targets":["` + host.foreignLink + `","` + host.ownedLink + `"]}`
	}
	writeFixture(t, host.receipt, receiptBody)
	state := filepath.Join(home, ".local", "state", "pfm")
	host.stateDirs = map[string]string{
		"retired gpt hook log directory":             filepath.Join(state, "gpt-agent-stop"),
		"retired call-cap tally directory":           filepath.Join(state, "call-cap"),
		"retired tracetree cut-gate stamp directory": filepath.Join(state, "cutgate"),
	}
	writeFixture(t, filepath.Join(state, "gpt-agent-stop", "flights-foreman.jsonl"), "{}\n")
	writeFixture(t, filepath.Join(state, "call-cap", "0f3a.tally"), "12\n")
	writeFixture(t, filepath.Join(state, "cutgate", "segment-0f3a"), "stamp\n")
	writeFixture(t, filepath.Join(state, "prices-checked"), "live")
	return host
}

// physicalHookPath names a hook link by its physical parent, the form the
// install prints and the receipt records.
func physicalHookPath(link string) string {
	return filepath.Join(paths.PhysicalPath(filepath.Dir(link)), filepath.Base(link))
}

func runRetiredHookInstall(t *testing.T, home string, mode Mode) (string, error) {
	t.Helper()
	state := filepath.Join(home, ".local", "state", "pfm")
	sourceRepo := filepath.Join(home, "source-repo")
	if err := os.MkdirAll(sourceRepo, 0o700); err != nil {
		t.Fatal(err)
	}
	recordFixtureSourceRepo(t, home, sourceRepo)
	configPath := filepath.Join(home, "pfm.config.json")
	writeFixture(t, configPath, `{"version":2}`)
	var output bytes.Buffer
	_, err := Run(t.Context(), Options{
		Mode: mode, Home: home, SourceRepo: sourceRepo, MCPConfigPath: configPath,
		CodexHomes: []string{}, Runner: &fakeRunner{nameSyncIdle: true},
		Env: &paths.MapEnv{HomeDir: home, Values: map[string]string{
			paths.EnvSIDDir:   filepath.Join(home, "sid"),
			paths.EnvStateDB:  filepath.Join(state, "pfm.db"),
			paths.EnvCacheDB:  filepath.Join(state, "pfm-cache.db"),
			paths.EnvProcRoot: filepath.Join(home, "proc"),
			enginesync.EnvBin: testjail.EngineSyncBin(),
		}},
		Now:    func() time.Time { return time.Date(2031, 2, 3, 4, 5, 6, 0, time.Local) },
		Stdout: &output,
	})
	return output.String(), err
}

func assertGone(t *testing.T, what string, targets ...string) {
	t.Helper()
	for _, path := range targets {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s %s remains (lstat err=%v)", what, path, err)
		}
	}
}

func assertOutput(t *testing.T, output string, lines ...string) {
	t.Helper()
	for _, line := range lines {
		if !strings.Contains(output, line) {
			t.Errorf("install output misses %q:\n%s", line, output)
		}
	}
}

// assertForeignLinkKept: the operator's link at a retired hook path survives
// with its own target.
func assertForeignLinkKept(t *testing.T, host retiredHookHost) {
	t.Helper()
	if link, err := os.Readlink(host.foreignLink); err != nil || link != host.foreignTarget {
		t.Errorf("operator link %s = %q (%v), want it kept pointing at %s",
			host.foreignLink, link, err, host.foreignTarget)
	}
}

// TestInstallRetiresTheGptHookAndRetiredStateDirs runs pfm install over a host
// an earlier pfm left the gpt agent Stop hook on: its link in the default
// store goes, an operator's link where an old account had pfm's stays and is
// named, then the managed copy, the receipt, the hook's log directory and the
// call-cap and cut-gate state directories go. A second install finds nothing.
func TestInstallRetiresTheGptHookAndRetiredStateDirs(t *testing.T) {
	host := seedRetiredHookHost(t, "")
	output, err := runRetiredHookInstall(t, host.home, ModeApply)
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, output)
	}
	assertGone(t, "retired gpt hook", host.ownedLink, host.managed, host.receipt)
	assertForeignLinkKept(t, host)
	lines := []string{
		"  change  remove " + host.ownedLink + "\n",
		"  skip    preserve unowned retired gpt hook asset " + host.foreignLink + "\n",
		"  change  retire " + host.managed + " (retired gpt hook asset)\n",
		"  change  remove " + host.receipt + "\n",
	}
	for what, dir := range host.stateDirs {
		assertGone(t, what, dir)
		lines = append(lines, "  change  remove "+what+" "+dir+" (freed ")
	}
	assertOutput(t, output, lines...)
	if got := readFixture(t, filepath.Join(host.home, ".local", "state", "pfm", "prices-checked")); got != "live" {
		t.Errorf("live sibling state changed: %q", got)
	}

	second, err := runRetiredHookInstall(t, host.home, ModeApply)
	if err != nil {
		t.Fatalf("second install failed: %v\n%s", err, second)
	}
	for _, path := range []string{host.ownedLink, host.foreignLink, host.managed, host.receipt, "retired "} {
		if strings.Contains(second, path) {
			t.Errorf("second install still acts on %q:\n%s", path, second)
		}
	}
	assertForeignLinkKept(t, host)
}

// TestInstallPreviewNamesEveryRetiredHookRemoval: a dry run lists each removal
// and the kept operator link, and removes nothing.
func TestInstallPreviewNamesEveryRetiredHookRemoval(t *testing.T) {
	host := seedRetiredHookHost(t, "")
	output, err := runRetiredHookInstall(t, host.home, ModeDryRun)
	if err != nil {
		t.Fatalf("preview failed: %v\n%s", err, output)
	}
	lines := []string{
		"  change  remove " + host.ownedLink + "\n",
		"  skip    preserve unowned retired gpt hook asset " + host.foreignLink + "\n",
		"  change  retire " + host.managed + " (retired gpt hook asset)\n",
		"  change  remove " + host.receipt + "\n",
	}
	kept := []string{host.ownedLink, host.managed, host.receipt}
	for what, dir := range host.stateDirs {
		lines = append(lines, "  change  remove "+what+" "+dir+" (frees ")
		kept = append(kept, dir)
	}
	assertOutput(t, output, lines...)
	for _, path := range kept {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("preview removed %s: %v", path, err)
		}
	}
	assertForeignLinkKept(t, host)
}

// TestInstallReportsAnUnreadableGptHookReceipt: a receipt that cannot be read
// is a warn line naming its path and error, and the install still runs; the
// default store's owned link and the call-cap and cut-gate state go, and the
// managed copy, its log directory and the receipt stay, so a link only the
// receipt knew of never dangles unnamed.
func TestInstallReportsAnUnreadableGptHookReceipt(t *testing.T) {
	host := seedRetiredHookHost(t, "{not json")
	output, err := runRetiredHookInstall(t, host.home, ModeApply)
	if err != nil {
		t.Fatalf("an unreadable retired receipt failed the install: %v\n%s", err, output)
	}
	assertOutput(t, output, "  warn    decode gpt hook ownership "+host.receipt+
		": invalid character 'n' looking for beginning of object key string; delete it to finish the retirement\n")
	assertGone(t, "retired gpt hook link", host.ownedLink)
	logDir := host.stateDirs["retired gpt hook log directory"]
	for what, dir := range host.stateDirs {
		if dir != logDir {
			assertGone(t, what, dir)
		}
	}
	for _, path := range []string{host.managed, logDir, host.receipt} {
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("%s removed beside an unreadable receipt: %v", path, err)
		}
	}
	assertForeignLinkKept(t, host)
}

// TestUninstallRetiresTheGptHook: uninstall takes the same retirement, so
// no link to a removed managed copy survives it.
func TestUninstallRetiresTheGptHook(t *testing.T) {
	host := seedRetiredHookHost(t, "")
	installer := &engine{
		options:     Options{Home: host.home, Stdout: &bytes.Buffer{}},
		managedRoot: managedRootForHome(host.home), apply: true,
	}
	assets, err := assetFiles()
	if err != nil {
		t.Fatal(err)
	}
	if err := installer.removeManagedAssets(assets); err != nil {
		t.Fatal(err)
	}
	assertGone(t, "retired gpt hook", host.ownedLink, host.managed, host.receipt)
	assertForeignLinkKept(t, host)
}
