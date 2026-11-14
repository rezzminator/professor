package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	"github.com/rezzminator/professor/pfm/internal/installer"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/stale"
	"github.com/rezzminator/professor/pfm/internal/store"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRRDirEntryUsesInjectedHome(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(root, "unmanaged")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "injected-home")
	var stdout, stderr bytes.Buffer
	if code := runRRDirEntry(bytes.NewReader(payload), &stdout, &stderr, &paths.MapEnv{HomeDir: home}); code != 0 {
		t.Fatalf("runRRDirEntry code = %d, want fail-open 0; stderr = %q", code, stderr.String())
	}
	want := filepath.Join(home, ".professor", ".professor", "RR")
	if got := stdout.String(); !strings.Contains(got, want) {
		t.Fatalf("runRRDirEntry stdout = %q, want injected fallback %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("runRRDirEntry stderr = %q, want empty", stderr.String())
	}
}

func TestRRDirEntryReportsHomeErrorAndContinues(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]string{"cwd": root})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := runRRDirEntry(
		bytes.NewReader(payload),
		&stdout,
		&stderr,
		&paths.MapEnv{HomeErr: errors.New("home unavailable")},
	); code != 0 {
		t.Fatalf("runRRDirEntry code = %d, want fail-open 0; stderr = %q", code, stderr.String())
	}
	if got := stderr.String(); !strings.Contains(
		got,
		"pfm internal rr-dir: resolve home directory: home unavailable\n",
	) {
		t.Fatalf("runRRDirEntry stderr = %q, want visible home error", got)
	}
	if got := stdout.String(); !strings.Contains(got, "RR-DIR-ERROR:") || !strings.Contains(got, "no home directory") {
		t.Fatalf("runRRDirEntry stdout = %q, want fail-open hook response for empty home", got)
	}
}

func TestVersion(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(version) code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "pfm dev\n"; got != want {
		t.Fatalf("run(version) stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("run(version) stderr = %q, want empty", stderr.String())
	}
}

func TestSettledRootInterface(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--version) code = %d; stderr = %q", code, stderr.String())
	}
	if stdout.String() != "pfm dev\n" {
		t.Fatalf("run(--version) stdout = %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"--help"}, &stdout, &stderr); code != 0 {
		t.Fatalf("run(--help) code = %d; stderr = %q", code, stderr.String())
	}
	help := stdout.String()
	for _, want := range []string{
		"operator commands:", "  chat", "  install", "  update", "  init", "wiring commands:",
		"  statusline", "  usage-hook", "tokens    audit transcripts: where did the tokens go (--codex, --opencode, --tree SESSION|AGENT)",
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("root help missing %q:\n%s", want, help)
		}
	}
}

func TestChatShimWhoamiCompatibilityRoute(t *testing.T) {
	jailTest(t)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	t.Setenv("CODEX_THREAD_ID", "")
	var stdout, stderr bytes.Buffer
	runtime, err := pfmconfig.LoadRuntime("")
	if err != nil {
		t.Fatal(err)
	}
	code := runChatWithRuntime([]string{"whoami"}, strings.NewReader(""), &stdout, &stderr,
		runtime, context.Background())
	if code != 1 {
		t.Fatalf("chat whoami code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if strings.Contains(stderr.String(), "unknown command") ||
		!strings.Contains(stderr.String(), "not inside a pfm chat") {
		t.Fatalf("chat whoami did not reach the root identity command: %q", stderr.String())
	}
}

func TestLSKilledAbsorbsTheOldKilledListing(t *testing.T) {
	jailTest(t)
	const id = "88888888-8888-4888-8888-888888888888"
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Kill(context.Background(), store.Killed{
		ID: id, Engine: "cc", KilledAt: 42,
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"ls", "--killed"},
		{"ls", "--killed", "--tsv"},
		{"ls", "--tsv", "--killed"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("%v code=%d stderr=%q", args, code, stderr.String())
		}
		if stdout.String() != id+"\t\t42\n" {
			t.Fatalf("%v stdout=%q", args, stdout.String())
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"no-such-command"}, &stdout, &stderr); code != 2 {
		t.Fatalf("run(unknown) code = %d, want 2", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("run(unknown) stdout = %q, want empty", stdout.String())
	}
	if got := stderr.String(); !strings.Contains(got, `unknown command "no-such-command"`) {
		t.Fatalf("run(unknown) stderr = %q, want unknown-command message", got)
	}
}

func TestChatUnknownVerbIsAUsageError(t *testing.T) {
	jailTest(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "no-such-verb"}, &stdout, &stderr); code != 2 {
		t.Fatalf("chat unknown verb code=%d stdout=%q stderr=%q, want 2", code, stdout.String(), stderr.String())
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), `pfm chat: unknown command "no-such-verb"`) ||
		!strings.Contains(stderr.String(), "usage: pfm chat") || !strings.Contains(stderr.String(), "  call ") {
		t.Fatalf("chat unknown verb stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestKillKilledUnkillCLI(t *testing.T) {
	root := jailTest(t)
	const id = "99999999-9999-4999-8999-999999999999"
	transcriptPath := filepath.Join(root, "claude", "project", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		transcriptPath,
		[]byte(`{"type":"user","cwd":"/work/example","message":{"content":"kill fixture"}}`+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	if err := database.UpsertTranscript(
		context.Background(),
		store.Transcript{
			UUID:        id,
			Path:        transcriptPath,
			PromptCount: 12,
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"chat", "kill", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("kill code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	// The kill names its own mechanism (chat.KillOutcome): this fixture has no
	// live pane, so it de-lists and says so.
	if stdout.String() != "killed "+id+"\tde-listed only, no live pane closed\n" || stderr.Len() != 0 {
		t.Fatalf("kill stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ls", "--killed"}, &stdout, &stderr); code != 0 {
		t.Fatalf("killed code=%d stderr=%q", code, stderr.String())
	}
	fields := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\t")
	if len(fields) != 3 || fields[0] != id || fields[1] != "cc" {
		t.Fatalf("killed stdout=%q, want id/engine/killed_at only", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"chat", "unkill", id}, &stdout, &stderr); code != 0 {
		t.Fatalf("unkill code=%d stderr=%q", code, stderr.String())
	}
	if stdout.String() != "unkilled "+id+"\n" {
		t.Fatalf("unkill stdout=%q", stdout.String())
	}
}

func TestKilledPruneOrphansCLI(t *testing.T) {
	jailTest(t)
	const (
		live    = "11111111-1111-4111-8111-111111111111"
		orphan  = "22222222-2222-4222-8222-222222222222"
		orphan2 = "33333333-3333-4333-8333-333333333333"
	)
	database, err := store.Open()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := database.UpsertTranscript(ctx, store.Transcript{
		UUID: live,
		Path: "/jailed/" + live + ".jsonl",
	}); err != nil {
		t.Fatal(err)
	}
	for _, killed := range []store.Killed{
		{ID: live, Engine: "cc", KilledAt: 10},
		{ID: orphan, Engine: "cc", KilledAt: 20},
		{ID: orphan2, Engine: "cx", KilledAt: 30},
	} {
		if err := database.Kill(ctx, killed); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"archive", "--prune-orphans"}, &stdout, &stderr); code != 0 {
		t.Fatalf("prune dry run code=%d stderr=%q", code, stderr.String())
	}
	// The engine column is empty for every orphan, and that IS the report: an
	// engine is derived from whichever index table claims the id, and an orphan
	// is precisely a kill no index table claims any more.
	dryRun := stdout.String()
	if !strings.Contains(dryRun, "would prune\t"+orphan+"\t\t20\n") ||
		!strings.Contains(dryRun, "would prune\t"+orphan2+"\t\t30\n") ||
		!strings.Contains(dryRun, "2 orphaned kill(s); re-run with --yes to delete\n") {
		t.Fatalf("prune dry run stdout=%q", dryRun)
	}
	if strings.Contains(dryRun, live) {
		t.Fatalf("prune dry run named the live kill: stdout=%q", dryRun)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ls", "--killed"}, &stdout, &stderr); code != 0 {
		t.Fatalf("killed after dry run code=%d stderr=%q", code, stderr.String())
	}
	if lines := strings.Count(stdout.String(), "\n"); lines != 3 {
		t.Fatalf("dry run deleted rows: killed stdout=%q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"archive", "--prune-orphans", "--yes"}, &stdout, &stderr); code != 0 {
		t.Fatalf("prune code=%d stderr=%q", code, stderr.String())
	}
	pruned := stdout.String()
	if !strings.Contains(pruned, "pruned\t"+orphan+"\t\t20\n") ||
		!strings.Contains(pruned, "pruned 2 orphaned kill(s)\n") {
		t.Fatalf("prune stdout=%q", pruned)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ls", "--killed"}, &stdout, &stderr); code != 0 {
		t.Fatalf("killed after prune code=%d stderr=%q", code, stderr.String())
	}
	if got := stdout.String(); !strings.HasPrefix(got, live+"\tcc\t10\n") ||
		strings.Count(got, "\n") != 1 {
		t.Fatalf("killed after prune stdout=%q, want only the live kill", got)
	}
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"ls", "--killed", "--yes"}, &stdout, &stderr); code != 2 {
		t.Fatalf("killed --yes without --prune-orphans code=%d, want 2", code)
	}
}

func TestDoctorReportsDamagedDatabaseWithoutPanic(t *testing.T) {
	jailTest(t)
	dbPath := os.Getenv(paths.EnvCacheDB)
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"doctor"}, &stdout, &stderr); code != 3 {
		t.Fatalf("doctor code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "doctor: config path=") ||
		!strings.Contains(stdout.String(), "doctor: unhealthy database:") {
		t.Fatalf("doctor stdout=%q", stdout.String())
	}
}

func TestUsageErrors(t *testing.T) {
	jailTest(t)
	for _, args := range [][]string{
		{"ls", "--plain", "--tsv"},
		{"ls", "--killed", "--all"},
		{"chat", "open"},
		{"index", "--unknown"},
		{"doctor", "extra"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("run(%q) code=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
		}
		if stderr.Len() == 0 {
			t.Fatalf("run(%q) emitted no usage/error", args)
		}
	}
}

func jailTest(t *testing.T) string {
	t.Helper()
	root := testjail.InstalledHome(t)
	dirs, files := storeLayout()
	testjail.StageAccountLinks(t, filepath.Join(root, "home"), filepath.Join(root, "home", ".cc", "1"), dirs, files)
	stageStorePlugins(t, filepath.Join(root, "home"))
	testjail.StageHerdrClaudeHook(t, filepath.Join(root, "home", ".cc", "1"))
	// The jailed home reaches no user service manager: macOS always has
	// launchctl, whose real gui domain holds no plist in this home.
	previous := doctor.ScheduleTimerProbeOverride
	doctor.ScheduleTimerProbeOverride = func(context.Context, deps.Runner) doctor.ServiceManagerReport {
		return doctor.ServiceManagerReport{}
	}
	t.Cleanup(func() { doctor.ScheduleTimerProbeOverride = previous })
	return root
}

func jailPaths(t *testing.T) paths.Values {
	t.Helper()
	resolved, err := paths.Resolve()
	if err != nil {
		t.Fatalf("resolve jail paths: %v", err)
	}
	return resolved
}

// Read args[0] comparisons from the dispatcher, including its != kill-exit case.
func argsZeroStringLiterals(body *ast.BlockStmt) map[string]bool {
	literals := map[string]bool{}
	isArgsZero := func(expr ast.Expr) bool {
		index, ok := expr.(*ast.IndexExpr)
		if !ok {
			return false
		}
		ident, ok := index.X.(*ast.Ident)
		return ok && ident.Name == "args"
	}
	ast.Inspect(body, func(n ast.Node) bool {
		binary, ok := n.(*ast.BinaryExpr)
		if !ok || (binary.Op != token.EQL && binary.Op != token.NEQ) {
			return true
		}
		var literal *ast.BasicLit
		switch {
		case isArgsZero(binary.X):
			literal, _ = binary.Y.(*ast.BasicLit)
		case isArgsZero(binary.Y):
			literal, _ = binary.X.(*ast.BasicLit)
		}
		if literal == nil || literal.Kind != token.STRING {
			return true
		}
		value, err := strconv.Unquote(literal.Value)
		if err == nil {
			literals[value] = true
		}
		return true
	})
	return literals
}

// switchCaseStringLiterals collects every string literal a top-level
// "switch args[0]" case clause names.
func switchCaseStringLiterals(body *ast.BlockStmt) map[string]bool {
	literals := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok {
			return true
		}
		for _, expr := range clause.List {
			if literal, ok := expr.(*ast.BasicLit); ok && literal.Kind == token.STRING {
				if value, err := strconv.Unquote(literal.Value); err == nil {
					literals[value] = true
				}
			}
		}
		return true
	})
	return literals
}

// findFuncDecl parses main.go once and returns the *ast.FuncDecl body for
// name — "run" or "runInternal", the two functions topLevelSubcommands and
// internalSubcommands must stay in lockstep with.
func findFuncDecl(t *testing.T, name string) *ast.BlockStmt {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Name.Name == name {
			return fn.Body
		}
	}
	t.Fatalf("main.go declares no func %s", name)
	return nil
}

// Every advertised top-level subcommand must reach its dispatcher handler.
func TestTopLevelSubcommandsReachTheirHandler(t *testing.T) {
	body := findFuncDecl(t, "run")
	literals := switchCaseStringLiterals(body)
	for _, name := range topLevelSubcommands {
		if literals[name] {
			continue
		}
		t.Fatalf(
			"topLevelSubcommands names %q, but run's switch has no matching case — it falls through to the default \"unknown command\" arm",
			name,
		)
	}
}

// Advertised internal subcommands must reach their dispatcher handlers.
func TestInternalSubcommandsReachTheirHandler(t *testing.T) {
	body := findFuncDecl(t, "runInternal")
	comparisons := argsZeroStringLiterals(body)
	for _, name := range internalSubcommands {
		if !comparisons[name] {
			t.Fatalf(
				"internalSubcommands names %q, but runInternal's if-chain never compares args[0] against it — it falls through to \"pfm internal: unknown subcommand\"",
				name,
			)
		}
	}
}

// brokenConfigJail is a jailed installed home whose default config does not
// parse; it returns the config's path.
func brokenConfigJail(t *testing.T) string {
	t.Helper()
	root := jailTest(t)
	t.Setenv(paths.EnvConfig, "")
	clone := filepath.Join(root, "clone")
	if err := os.MkdirAll(clone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := paths.WriteSourceRepoMarker(jailPaths(t).Home, clone); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(clone, pfmconfig.FileName)
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Version answers on a default config that does not parse.
func TestVersionAnswersOnABrokenConfig(t *testing.T) {
	brokenConfigJail(t)
	for _, arg := range []string{"--version", versionCommand} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, &stdout, &stderr); code != 0 {
			t.Fatalf("%s code=%d stdout=%q stderr=%q, want 0", arg, code, stdout.String(), stderr.String())
		}
		if !strings.HasPrefix(stdout.String(), "pfm ") {
			t.Fatalf("%s stdout=%q, want the pfm version line", arg, stdout.String())
		}
	}
}

// Stale needs no config during binary replacement, a broken one included.
func TestInternalStaleAnswersOnABrokenConfig(t *testing.T) {
	brokenConfigJail(t)
	bin := filepath.Join(jailPaths(t).Home, ".local", "bin", "pfm")
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := testjail.WriteExecutableNeverRun(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	args := []string{"stale", "--binary", bin}
	var stdout, stderr bytes.Buffer
	code := run(append([]string{internalCommand}, args...), &stdout, &stderr)
	if strings.Contains(stderr.String(), "pfm: config:") {
		t.Fatalf("internal stale stderr=%q, want no config refusal", stderr.String())
	}
	var wantOut, wantErr bytes.Buffer
	wantCode := stale.Run(args[1:], &wantOut, &wantErr)
	if code != wantCode || stdout.String() != wantOut.String() || stderr.String() != wantErr.String() {
		t.Fatalf("internal stale code=%d stdout=%q stderr=%q, want stale.Run's code=%d stdout=%q stderr=%q",
			code, stdout.String(), stderr.String(), wantCode, wantOut.String(), wantErr.String())
	}
}

// A hook or launcher runs on defaults over a broken config: a refusal there
// would keep Claude from starting and fail every guard open.
func TestInternalCommandsRunOnDefaultsOnABrokenConfig(t *testing.T) {
	path := brokenConfigJail(t)
	var stdout, stderr bytes.Buffer
	if code := run([]string{internalCommand, "primary-get"}, &stdout, &stderr); code != 0 {
		t.Fatalf("internal primary-get code=%d stdout=%q stderr=%q, want 0", code, stdout.String(), stderr.String())
	}
	if stdout.String() != "1\n" {
		t.Fatalf("internal primary-get stdout=%q, want the default account %q", stdout.String(), "1\n")
	}
	want := "pfm: config: parse config " + path +
		" at byte 1: unexpected EOF; running on defaults until it is fixed (pfm doctor)\n"
	if stderr.String() != want {
		t.Fatalf("internal primary-get stderr=%q, want %q", stderr.String(), want)
	}
}

func TestCommandsRefuseOnABrokenConfig(t *testing.T) {
	path := brokenConfigJail(t)
	for _, args := range [][]string{{"ls"}, {"chat", "ls"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 1 {
			t.Fatalf("ls code=%d stdout=%q stderr=%q, want 1", code, stdout.String(), stderr.String())
		}
		want := "pfm: config: parse config " + path + " at byte 1: unexpected EOF"
		if !strings.HasPrefix(stderr.String(), want) {
			t.Fatalf("ls stderr=%q, want it to begin %q", stderr.String(), want)
		}
	}
}

func TestInstallWithMissingNamedConfig(t *testing.T) {
	broken := brokenConfigJail(t)
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(filepath.Dir(broken), "missing", pfmconfig.FileName)
	account := pfmconfig.DefaultAccountDir(jailPaths(t).Home, 42)
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvClaudeRoots, filepath.Join(account, "projects"))
	previous := runInstaller
	t.Cleanup(func() { runInstaller = previous })
	runInstaller = func(context.Context, installer.Options) (installer.Report, error) { return installer.Report{}, nil }
	for _, scenario := range []struct {
		args   []string
		code   int
		prefix string
	}{
		{[]string{"install", "--offline"}, 0, "  skip    "},
		{[]string{"install", "--yes", "--offline"}, 1, "pfm install: "},
		{[]string{"install", "--check", "--offline"}, 1, "pfm install: "},
	} {
		var stdout, stderr bytes.Buffer
		args := append([]string{"--config", missing}, scenario.args...)
		code := run(args, &stdout, &stderr)
		want := scenario.prefix + "--config " + missing + " does not exist; refusing to converge host wiring on defaults (a missing explicit config would disable every MCP service it names)\n"
		output := stderr.String()
		if scenario.code == 0 {
			output = strings.SplitAfterN(stdout.String(), "\n", 2)[0]
		}
		if code != scenario.code || output != want {
			t.Fatalf("code=%d want=%d output=%q expected=%q", code, scenario.code, output, want)
		}
	}
}

func storeLayout() ([]string, map[string]string) {
	var names []string
	files := make(map[string]string)
	for _, entry := range installer.StoreEntries {
		if entry.Dir {
			names = append(names, entry.Name)
		} else {
			files[entry.Name] = entry.Seed
		}
	}
	return names, files
}

func TestInstallerManagedCleanupOptions(t *testing.T) {
	var output bytes.Buffer
	home := t.TempDir()
	runtime := pfmconfig.Runtime{
		Paths:  paths.Values{Home: home, ManagedSettingsDir: filepath.Join(home, "managed")},
		Config: pfmconfig.Config{Claude: pfmconfig.Claude{CleanupPeriodDays: 123, RequireManagedCleanup: true}},
	}
	options := newInstallerOptions(installer.ModeDryRun, "", true, &output, &output, runtime)
	if options.ManagedSettingsDir != runtime.Paths.ManagedSettingsDir || options.CleanupPeriodDays != 123 ||
		!options.RequireManagedCleanup {
		t.Fatalf("cleanup options=%+v", options)
	}
}

func stageStorePlugins(t *testing.T, home string) {
	t.Helper()
	storeDir := installer.ClaudeStore(home)
	targets := installer.ClaudePluginTargets(installer.ClaudePluginBuild{Plugins: pfmconfig.DefaultClaudePlugins()})
	ids, err := installer.ClaudePluginsNotInstalled(t.TempDir(), targets)
	if err != nil {
		t.Fatal(err)
	}
	testjail.StageClaudePlugins(t, storeDir, ids)
	testjail.StageTracetreeKit(t, storeDir, ids)
}

// rooted writes the run-unique temp root as {root}, so output naming it compares exactly.
func rooted(s, root string) string { return strings.ReplaceAll(s, root, "{root}") }

// scrub normalises what varies per run: the temp root, session ids, socket names and clock stamps.
func scrub(s, root string) string {
	// The host goes first: a macOS host name can hold a UUID the {uuid} rule would rewrite.
	if host, err := os.Hostname(); err == nil {
		s = strings.ReplaceAll(s, "host "+host+" ", "host {host} ")
	}
	for _, r := range []struct{ pattern, token string }{
		{`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`, "{uuid}"},
		{`\b(cc|cx|oc)-\d+-\d+-\d+\b`, "{socket}"},
		{`\d{4}-\d\d-\d\d[T ]\d\d:\d\d(:\d\d(\.\d+)?)?Z?`, "{time}"},
		{`\d{4}-\d\d-\d\dT\d\dh\b`, "{hour}"},
		{`\b[0-9a-f]{32}\b`, "{hash}"},
	} {
		s = regexp.MustCompile(r.pattern).ReplaceAllString(s, r.token)
	}
	if root == "" {
		return s
	}
	return rooted(s, root)
}
