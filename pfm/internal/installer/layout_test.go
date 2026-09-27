package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func layoutFixture(t *testing.T) LayoutEnv {
	t.Helper()
	home := t.TempDir()
	store := filepath.Join(home, ".claude")
	account := filepath.Join(home, ".cc", "2")
	for _, dir := range []string{store, account, filepath.Join(home, "proc"), filepath.Join(home, "clone"), filepath.Join(home, "managed"), filepath.Join(home, ".local", "state", "pfm")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range SessionPaths {
		path := filepath.Join(store, entry)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(path, filepath.Join(account, entry)); err != nil {
			t.Fatal(err)
		}
	}
	env := LayoutEnv{
		Home: home, Clone: filepath.Join(home, "clone"),
		Config: pfmconfig.Config{
			Claude: pfmconfig.Claude{CleanupPeriodDays: 36500, RequireManagedCleanup: true},
			Accounts: []pfmconfig.Account{
				{ID: 1, ConfigDir: store, Implicit: true},
				{ID: 2, ConfigDir: account},
			},
		},
		ConfigPath: filepath.Join(home, "clone", pfmconfig.FileName),
		StateDB:    filepath.Join(home, ".local", "state", "pfm", "pfm.db"),
		CacheDB:    filepath.Join(home, ".local", "state", "pfm", "pfm-cache.db"),
		ManagedDir: filepath.Join(home, "managed"), ProcRoot: filepath.Join(home, "proc"),
		ManagedRoot:     filepath.Join(home, ".local", "share", "pfm", "install"),
		LegacyConfigDir: filepath.Join(home, ".config", "pfm"),
		runner:          &layoutTestRunner{},
	}
	layoutWrite(t, env.ConfigPath, `{"version":2}`)
	layoutWrite(t, env.StateDB, "state")
	layoutWrite(t, env.CacheDB, "cache")
	layoutWrite(t, filepath.Join(env.ManagedDir, "pfm.json"), `{"cleanupPeriodDays":36500}`)
	layoutWrite(
		t,
		filepath.Join(home, ".zshrc"),
		sourceLine(filepath.Join(env.Clone, "pfm", "internal", "installer", "assets", "shim", "pfm.zsh"))+"\n",
	)
	return env
}

func layoutWrite(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
}

func layoutFinding(t *testing.T, findings []LayoutFinding, row, path string) LayoutFinding {
	t.Helper()
	for _, finding := range findings {
		if finding.Row == row && finding.Path == path {
			return finding
		}
	}
	t.Fatalf("no finding row=%s path=%s in %+v", row, path, findings)
	return LayoutFinding{}
}

func requireLayoutVerdict(
	t *testing.T,
	findings []LayoutFinding,
	row, path string,
	verdict LayoutVerdict,
) LayoutFinding {
	t.Helper()
	finding := layoutFinding(t, findings, row, path)
	if finding.Verdict != verdict || finding.Err != nil {
		t.Fatalf("%s %s = %+v, want %s without error", row, path, finding, verdict)
	}
	return finding
}

func TestLayoutTargetHostEveryRowOK(t *testing.T) {
	env := layoutFixture(t)
	findings := ClassifyLayout(env)
	if len(findings) < len(HostLayout) {
		t.Fatalf("%d findings for %d layout rows", len(findings), len(HostLayout))
	}
	seen := map[string]bool{}
	for _, finding := range findings {
		seen[finding.Row] = true
		if finding.Err != nil || finding.Verdict != VerdictOK {
			t.Errorf("target finding %+v", finding)
		}
	}
	for _, row := range HostLayout {
		if !seen[row.Row] {
			t.Errorf("row %s has no finding", row.Row)
		}
	}
}

func TestLayoutLegacyHostClassifiesEveryKind(t *testing.T) {
	env := layoutFixture(t)
	account := env.Config.Accounts[1].ConfigDir
	for _, path := range []string{env.ConfigPath, env.StateDB, env.CacheDB} {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	layoutWrite(t, filepath.Join(env.LegacyConfigDir, pfmconfig.FileName), `{"version":2}`)
	layoutWrite(t, filepath.Join(env.LegacyConfigDir, "harvester.config.json"), `{}`)
	layoutWrite(t, paths.LegacyStateDB(env.Home), "state")
	layoutWrite(t, paths.LegacyCacheDB(env.Home), "cache")
	if err := os.Remove(filepath.Join(account, "file-history")); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, filepath.Join(account, "file-history", "session", "checkpoint"), "checkpoint")
	other := filepath.Join(env.Home, ".cc", "3")
	if err := os.MkdirAll(other, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(account, "projects")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(other, "projects"), filepath.Join(account, "projects")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(env.Home, ".claude", "projects"), filepath.Join(other, "projects")); err != nil {
		t.Fatal(err)
	}
	env.Config.Accounts = append(env.Config.Accounts, pfmconfig.Account{ID: 3, ConfigDir: other})
	for _, entry := range SessionPaths[1:] {
		if err := os.Symlink(filepath.Join(env.Home, ".claude", entry), filepath.Join(other, entry)); err != nil {
			t.Fatal(err)
		}
	}
	layoutWrite(
		t,
		filepath.Join(account, "settings.json"),
		`{"statusLine":{"type":"command","command":"`+env.Home+`/.local/bin/pfm statusline"}}`,
	)
	registry := filepath.Join(account, ".claude.json")
	layoutWrite(t, registry, `{"mcpServers":{"chat":{"command":"pfm"}}}`)
	ledger := map[string]any{
		"registrations": map[string]any{
			physicalSettingsPath(registry): map[string]any{"chat": map[string]any{"command": "pfm"}},
		},
	}
	encoded, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, filepath.Join(env.ManagedRoot, mcpOwnershipName), string(encoded))
	layoutWrite(
		t,
		filepath.Join(account, "scripts", "cc-memory-wire.sh"),
		string(oldMemoryHelperFixture(t, "memory-wire.sh", "test-vault")),
	)
	layoutWrite(
		t,
		filepath.Join(env.Home, ".zshrc"),
		sourceLine(filepath.Join(env.ManagedRoot, "shim", "pfm.zsh"))+"\n",
	)
	if err := os.MkdirAll(filepath.Join(env.ManagedRoot, "harness-prompts"), 0o700); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, filepath.Join(env.Home, ".local", "state", "pfm", "shared.db"), "")
	if err := os.Mkdir(filepath.Join(env.Home, ".cc", ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	findings := ClassifyLayout(env)
	for _, check := range []struct {
		row, path string
		want      LayoutVerdict
	}{
		{"config", env.ConfigPath, VerdictMove},
		{"harvester-config", filepath.Join(env.Clone, "harvester.config.json"), VerdictMove},
		{"state-db", env.StateDB, VerdictMove},
		{"cache-db", env.CacheDB, VerdictMove},
		{"session-store", filepath.Join(account, "projects"), VerdictRepoint},
		{"session-store", filepath.Join(account, "file-history"), VerdictMerge},
		{"memory-helpers", account, VerdictMove},
		{"account-settings", filepath.Join(account, "settings.json"), VerdictStrip},
		{"account-mcp", registry, VerdictStrip},
		{"zshrc", filepath.Join(env.Home, ".zshrc"), VerdictRepoint},
		{"staged-prompts", filepath.Join(env.ManagedRoot, "harness-prompts"), VerdictRemove},
		{"shared-db", filepath.Join(env.Home, ".local", "state", "pfm", "shared.db"), VerdictRemove},
		{"stray-dir", filepath.Join(env.Home, ".cc", ".git"), VerdictRemove},
	} {
		requireLayoutVerdict(t, findings, check.row, check.path, check.want)
	}
}

func TestLayoutImplicitAccountLinkHasNoSessionRows(t *testing.T) {
	env := layoutFixture(t)
	link := filepath.Join(env.Home, ".cc", "1")
	if err := os.Symlink(filepath.Join(env.Home, ".claude"), link); err != nil {
		t.Fatal(err)
	}
	env.Config.Accounts = append(env.Config.Accounts, pfmconfig.Account{ID: 3, ConfigDir: link})
	accountRows := 0
	for _, finding := range ClassifyLayout(env) {
		if finding.Row == "session-store" && strings.HasPrefix(finding.Path, link+string(filepath.Separator)) {
			t.Fatalf("implicit whole-store link produced session row: %+v", finding)
		}
		if finding.Row == "session-store" &&
			strings.HasPrefix(finding.Path, env.Config.Accounts[1].ConfigDir+string(filepath.Separator)) {
			accountRows++
		}
	}
	if accountRows != len(SessionPaths) {
		t.Fatalf("other account has %d session rows, want %d", accountRows, len(SessionPaths))
	}
}

func TestLayoutForeignSessionLinkRefuses(t *testing.T) {
	env := layoutFixture(t)
	path := filepath.Join(env.Config.Accounts[1].ConfigDir, "projects")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(env.Home, "foreign", "projects")
	if err := os.Symlink(foreign, path); err != nil {
		t.Fatal(err)
	}
	finding := requireLayoutVerdict(t, ClassifyLayout(env), "session-store", path, VerdictRefuse)
	if !strings.Contains(finding.Detail, foreign) {
		t.Fatalf("foreign link detail=%q", finding.Detail)
	}
}

func TestLayoutNonEmptyLeftoversRefuse(t *testing.T) {
	env := layoutFixture(t)
	shared := filepath.Join(env.Home, ".local", "state", "pfm", "shared.db")
	layoutWrite(t, shared, "bytes")
	stray := filepath.Join(env.Home, ".cc", ".agents")
	layoutWrite(t, filepath.Join(stray, "owned"), "bytes")
	findings := ClassifyLayout(env)
	for _, check := range []struct{ row, path string }{{"shared-db", shared}, {"stray-dir", stray}} {
		finding := requireLayoutVerdict(t, findings, check.row, check.path, VerdictRefuse)
		if finding.Detail == "" {
			t.Errorf("%s has no reason", check.row)
		}
	}
}

func TestLayoutManagedCleanupStates(t *testing.T) {
	for _, testCase := range []struct {
		name, body string
		required   bool
		want       LayoutVerdict
		wantError  bool
	}{
		{"absent", "", true, VerdictCreate, false},
		{"wrong", `{"cleanupPeriodDays":30}`, true, VerdictRepoint, false},
		{"right", `{"cleanupPeriodDays":36500}`, true, VerdictOK, false},
		{"off", "", false, VerdictOK, false},
		{"unreadable", "{bad", true, VerdictOK, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := layoutFixture(t)
			path := filepath.Join(env.ManagedDir, "pfm.json")
			if testCase.body == "" {
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			} else {
				layoutWrite(t, path, testCase.body)
			}
			env.Config.Claude.RequireManagedCleanup = testCase.required
			finding := layoutFinding(t, ClassifyLayout(env), "managed-cleanup", path)
			if finding.Verdict != testCase.want || (finding.Err != nil) != testCase.wantError {
				t.Fatalf("finding=%+v, want verdict=%s error=%t", finding, testCase.want, testCase.wantError)
			}
		})
	}
}

func TestLayoutAccountFilesAndOwnershipErrors(t *testing.T) {
	env := layoutFixture(t)
	account := env.Config.Accounts[1].ConfigDir
	settings := filepath.Join(account, "settings.json")
	layoutWrite(t, settings, `{"statusLine":{"type":"command","command":"`+env.Home+`/.local/bin/pfm statusline"}}`)
	registry := filepath.Join(account, ".claude.json")
	layoutWrite(t, registry, `{"mcpServers":{"chat":{}}}`)
	ledger := map[string]any{
		"registrations": map[string]any{physicalSettingsPath(registry): map[string]any{"chat": map[string]any{}}},
	}
	encoded, err := json.Marshal(ledger)
	if err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, filepath.Join(env.ManagedRoot, mcpOwnershipName), string(encoded))
	help := filepath.Join(account, "scripts", "cc-memory-wire.sh")
	layoutWrite(t, help, string(oldMemoryHelperFixture(t, "memory-wire.sh", "vault")))
	findings := ClassifyLayout(env)
	requireLayoutVerdict(t, findings, "account-settings", settings, VerdictStrip)
	requireLayoutVerdict(t, findings, "account-mcp", registry, VerdictStrip)
	requireLayoutVerdict(t, findings, "memory-helpers", account, VerdictMove)
	layoutWrite(t, settings, "{bad")
	if finding := layoutFinding(t, ClassifyLayout(env), "account-settings", settings); finding.Err == nil {
		t.Fatal("malformed settings looked clean")
	}
	layoutWrite(t, filepath.Join(env.ManagedRoot, settingsHookOwnershipName), "{bad")
	if finding := layoutFinding(
		t,
		ClassifyLayout(env),
		"account-settings",
		settings,
	); finding.Err == nil ||
		finding.Detail != "ownership ledger" {
		t.Fatalf("ownership ledger finding=%+v", finding)
	}
}

func TestLayoutLegacyStatePath(t *testing.T) {
	env := layoutFixture(t)
	legacy := paths.LegacyStateDB(env.Home)
	layoutWrite(t, legacy, "state")
	finding := requireLayoutVerdict(t, ClassifyLayout(env), "state-db", env.StateDB, VerdictRefuse)
	if finding.Source != legacy {
		t.Fatalf("legacy source=%q", finding.Source)
	}
}

func TestLayoutClassifierPureAndStable(t *testing.T) {
	env := layoutFixture(t)
	before := layoutTreeSnapshot(t, env.Home)
	first := ClassifyLayout(env)
	second := ClassifyLayout(env)
	if len(first) < len(HostLayout) || !reflect.DeepEqual(first, second) {
		t.Fatalf("unstable findings: first=%+v second=%+v", first, second)
	}
	if after := layoutTreeSnapshot(t, env.Home); !reflect.DeepEqual(before, after) {
		t.Fatalf("classification changed host tree: before=%v after=%v", before, after)
	}
}

func layoutTreeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(path string, _ os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		state := info.Mode().String() + ":" + strconv.FormatInt(info.ModTime().UnixNano(), 10)
		if info.Mode().IsRegular() {
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			state += ":" + string(body)
		} else if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			state += ":" + target
		}
		result[path] = state
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLayoutEnvLoadsLegacyConfigWhenTargetMissing(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, "clone")
	if err := os.Mkdir(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".config", "pfm", pfmconfig.FileName)
	layoutWrite(t, legacy, `{"version":2}`)
	target := filepath.Join(clone, pfmconfig.FileName)
	runtime := pfmconfig.Runtime{
		Config: pfmconfig.Defaults(home, nil, ""),
		Paths: paths.Values{
			Home:               home,
			StateDB:            filepath.Join(home, "state.db"),
			CacheDB:            filepath.Join(home, "cache.db"),
			ManagedSettingsDir: filepath.Join(home, "managed"),
			ProcRoot:           filepath.Join(home, "proc"),
		},
	}
	runtime.Config.Path = target
	env, err := NewLayoutEnv(
		runtime,
		&paths.MapEnv{Values: map[string]string{"XDG_CONFIG_HOME": filepath.Join(home, ".config")}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if env.ConfigPath != target || env.Clone != clone || env.Config.Path != legacy || !env.Config.Exists {
		t.Fatalf("legacy env = %+v", env)
	}
	finding := layoutFinding(t, ClassifyLayout(env), "config", target)
	if finding.Verdict != VerdictMove || finding.Source != legacy {
		t.Fatalf("config finding=%+v", finding)
	}
}

func TestLayoutEnvTargetsCloneWhenRuntimeLoadedLegacyConfig(t *testing.T) {
	home := t.TempDir()
	clone := filepath.Join(home, "clone")
	if err := os.Mkdir(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(home, clone); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(home, ".config", "pfm", pfmconfig.FileName)
	layoutWrite(t, legacy, `{"version":2}`)
	loaded, err := pfmconfig.Load(legacy, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtime := pfmconfig.Runtime{Config: loaded, Paths: paths.Values{Home: home}}
	env, err := NewLayoutEnv(runtime, &paths.MapEnv{})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(clone, pfmconfig.FileName)
	if env.ConfigPath != want || env.Config.Path != legacy {
		t.Fatalf("target=%q loaded=%q want %q / %q", env.ConfigPath, env.Config.Path, want, legacy)
	}
}

const layoutTestMCPPort = 18765

func layoutMCPReplacer(env LayoutEnv, registry string) *strings.Replacer {
	return strings.NewReplacer(
		"{bin}", filepath.Join(env.Home, ".local", "bin", "pfm"),
		"{port}", strconv.Itoa(layoutTestMCPPort),
		"{registry}", physicalSettingsPath(registry),
	)
}

func TestLayoutMCPRowsJudgeLedgerAndShape(t *testing.T) {
	const (
		professor = `"professor":{"type":"stdio","command":"{bin}","args":["mcp","serve","--stdio"]}`
		chat      = `"chat":{"type":"stdio","command":"{bin}","args":["mcp","chat","serve"]}`
		harvester = `"harvester":{"type":"http","url":"http://127.0.0.1:{port}/mcp/harvester"}`
		operator  = `"operator":{"type":"stdio","command":"{bin}","args":["mcp","serve","--stdio"]}`
	)
	type want struct {
		verdict LayoutVerdict
		detail  string
		err     bool
	}
	ok := want{verdict: VerdictOK}
	for _, testCase := range []struct {
		name, registry, home, ledger string
		account, homeMCP             want
	}{
		{
			name:     "ledger-owned entry",
			registry: `{"mcpServers":{"chat":{"command":"pfm"}}}`,
			ledger:   `{"registrations":{"{registry}":{"chat":{}}}}`,
			account:  want{verdict: VerdictStrip, detail: "mcpServers.chat"},
			homeMCP:  ok,
		},
		{
			name:     "shape-only professor",
			registry: `{"counter":9007199254740993,"mcpServers":{` + professor + `}}`,
			account:  want{verdict: VerdictStrip, detail: "mcpServers.professor"},
			homeMCP:  ok,
		},
		{
			name:     "shape-only legacy",
			registry: `{"mcpServers":{` + chat + `,` + harvester + `}}`,
			account:  want{verdict: VerdictStrip, detail: "mcpServers.chat,mcpServers.harvester"},
			homeMCP:  ok,
		},
		{
			name: "near miss",
			registry: `{"mcpServers":{"professor":{"type":"stdio","command":"{bin}","args":["mcp","serve","--stdio"],` +
				`"cwd":"/srv"},"chat":{"type":"stdio","command":"pfm","args":["mcp","chat","serve"]},` + operator + `}}`,
			home:    `{"mcpServers":{` + operator + `,"harvester":{"type":"http","url":"http://127.0.0.1:1/mcp/harvester"}}}`,
			account: ok,
			homeMCP: ok,
		},
		{
			name:    "home file stripped",
			home:    `{"mcpServers":{` + harvester + `,"operator":{"command":"own"}}}`,
			ledger:  `{"clients":["harvester"]}`,
			account: ok,
			homeMCP: want{verdict: VerdictStrip, detail: "mcpServers.harvester,clients"},
		},
		{
			name:    "home shape without ledger",
			home:    `{"mcpServers":{` + professor + `}}`,
			account: ok,
			homeMCP: want{verdict: VerdictStrip, detail: "mcpServers.professor"},
		},
		{
			name:    "clients only",
			ledger:  `{"clients":["chat"]}`,
			account: ok,
			homeMCP: want{verdict: VerdictStrip, detail: "clients"},
		},
		{
			name:    "clean home",
			home:    `{"mcpServers":{"operator":{"command":"own"}}}`,
			ledger:  `{"registrations":{}}`,
			account: ok,
			homeMCP: ok,
		},
		{name: "home not JSON", home: `{bad`, account: ok, homeMCP: want{err: true}},
		{name: "home mcpServers not object", home: `{"mcpServers":[]}`, account: ok, homeMCP: want{err: true}},
		{
			name:     "unreadable ledger",
			registry: `{"mcpServers":{` + professor + `}}`,
			home:     `{"mcpServers":{` + professor + `}}`,
			ledger:   `{bad`,
			account:  want{err: true, detail: "ownership ledger"},
			homeMCP:  want{err: true, detail: "ownership ledger"},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			env := layoutFixture(t)
			env.Config.MCP.HTTP.Port = layoutTestMCPPort
			registry := filepath.Join(env.Config.Accounts[1].ConfigDir, ".claude.json")
			home := filepath.Join(env.Home, ".mcp.json")
			ledger := filepath.Join(env.ManagedRoot, mcpOwnershipName)
			replace := layoutMCPReplacer(env, registry)
			for path, raw := range map[string]string{registry: testCase.registry, home: testCase.home, ledger: testCase.ledger} {
				if raw != "" {
					layoutWrite(t, path, replace.Replace(raw))
				}
			}
			findings := ClassifyLayout(env)
			for _, check := range []struct {
				row, path string
				want      want
			}{{"account-mcp", registry, testCase.account}, {"home-mcp", home, testCase.homeMCP}} {
				finding := layoutFinding(t, findings, check.row, check.path)
				if (finding.Err != nil) != check.want.err || !check.want.err && finding.Verdict != check.want.verdict ||
					finding.Detail != check.want.detail {
					t.Errorf("%s = %+v, want %+v", check.row, finding, check.want)
				}
				if check.want.detail == "ownership ledger" && finding.Source != ledger {
					t.Errorf("%s ledger source = %q, want %q", check.row, finding.Source, ledger)
				}
			}
		})
	}
}
