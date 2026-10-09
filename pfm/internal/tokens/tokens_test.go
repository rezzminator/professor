package tokens

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// goldenManifest is one recorded run of the Node engine: testdata/golden/a1
// holds every mode over the fixtures (A1), testdata/golden/suite every run the
// Node suite made.
type goldenManifest struct {
	Test    string            `json:"test"`
	Now     string            `json:"now"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	Exit    int               `json:"exit"`
	Outputs map[string]string `json:"outputs"`
	// Dirs names the empty input directories, which git cannot carry under in/.
	Dirs []string `json:"dirs"`
}

// nodeError is a Node fs error message; Go states the same error as
// "{op} {path}: {message}", realpath's failure as lstat's and readdir's as open's.
var nodeError = regexp.MustCompile(`[A-Z]+: ([a-z][a-z ]*), (open|lstat|stat|scandir|realpath) '([^']*)'`)

var nodeOps = map[string]string{
	"open":     "open",
	"lstat":    "lstat",
	"stat":     "stat",
	"scandir":  "open",
	"realpath": "lstat",
}

// usageRefusals are the Node exit-2 messages that stay usage errors; every
// other Node exit 2 is a runtime failure, exit 1 in pfm (R2).
var usageRefusals = []string{
	"--since wants", "--timeline reads whole files", "--timeline wants a transcript path", "matched none of the",
	"is not a directory",
}

// intended rewrites a Node golden into what pfm prints: the R3 deltas, each
// named in the flight's run.md.
func intended(text string) string {
	text = strings.ReplaceAll(text, "token-audit: ", "pfm tokens: ")
	text = strings.ReplaceAll(text, "TOKEN_AUDIT_PRICES ", "--prices ")
	return nodeError.ReplaceAllStringFunc(text, func(match string) string {
		parts := nodeError.FindStringSubmatch(match)
		return nodeOps[parts[2]] + " " + parts[3] + ": " + parts[1]
	})
}

func intendedExit(code int, stderr string) int {
	if code != 2 {
		return code
	}
	for _, refusal := range usageRefusals {
		if strings.Contains(stderr, refusal) {
			return 2
		}
	}
	return 1
}

func TestAuditReplaysTheNodeGoldens(t *testing.T) {
	dirs := goldenDirs(t)
	// 18 A1 modes and 78 suite runs: fewer means the enumerator lost some.
	if len(dirs) != 96 {
		t.Fatalf("found %d golden runs, want 96", len(dirs))
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(filepath.Dir(dir))+"/"+filepath.Base(dir), func(t *testing.T) {
			replayGolden(t, openScenario(t, dir))
		})
	}
}

// testZone is the zone the goldens were recorded in (TZ=Etc/GMT-3): a fixed,
// nonzero offset, never the host's.
var testZone = time.FixedZone("UTC+3", 3*60*60)

// goldenNow is the instant every hand-built test audits at.
var goldenNow = time.Date(2026, 9, 21, 6, 0, 0, 0, time.UTC)

func goldenDirs(t *testing.T) []string {
	t.Helper()
	dirs, err := filepath.Glob("testdata/golden/*/*")
	if err != nil {
		t.Fatalf("list the goldens: %v", err)
	}
	return dirs
}

func realPath(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err == nil {
		absolute, err = filepath.EvalSymlinks(absolute)
	}
	if err != nil {
		t.Fatalf("resolve %s: %v", path, err)
	}
	return absolute
}

// fixture is a path under testdata/fixtures.
func fixture(t *testing.T, relative string) string {
	t.Helper()
	return filepath.Join(realPath(t, "testdata/fixtures"), filepath.FromSlash(relative))
}

// auditWith runs Audit over args at goldenNow in testZone on host test-host,
// unless opts says otherwise, and returns the exit code and both streams.
func auditWith(t *testing.T, opts Options, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	opts.Args, opts.Stdout, opts.Stderr = args, &stdout, &stderr
	if opts.Clock == nil {
		opts.Clock = clock.NewFake(goldenNow)
	}
	if opts.Env == nil {
		opts.Env = &paths.MapEnv{HostnameValue: "test-host"}
	}
	if opts.Location == nil {
		opts.Location = testZone
	}
	code := Audit(context.Background(), opts)
	return code, stdout.String(), stderr.String()
}

// scenario is one recorded run, its inputs materialized in scratch: opts
// carry its arguments (--prices first) and the roots Node discovered.
type scenario struct {
	dir      string
	manifest goldenManifest
	scratch  string
	expand   func(string) string
	mask     func(string) string
	opts     Options
}

func openScenario(t *testing.T, dir string) scenario {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("read the manifest: %v", err)
	}
	var manifest goldenManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatalf("decode the manifest: %v", err)
	}
	now, err := time.Parse(time.RFC3339Nano, manifest.Now)
	if err != nil {
		t.Fatalf("parse now %q: %v", manifest.Now, err)
	}
	fixtures := realPath(t, "testdata/fixtures")
	scratch := realPath(t, t.TempDir())
	expand := strings.NewReplacer("{TMP}", scratch, "{FIX}", fixtures).Replace
	mask := strings.NewReplacer(scratch, "{TMP}", fixtures, "{FIX}").Replace
	materialize(t, filepath.Join(dir, "in"), scratch, expand)
	for _, empty := range manifest.Dirs {
		if err := os.MkdirAll(filepath.Join(scratch, empty), 0o755); err != nil {
			t.Fatalf("create the empty input %s: %v", empty, err)
		}
	}
	prices, ok := manifest.Env["TOKEN_AUDIT_PRICES"]
	if !ok {
		t.Fatalf("%s names no saved price table: the replay never fetches", manifest.Test)
	}
	// first, so a trailing variadic flag (--timeline) never swallows it
	args := []string{"--prices", expand(prices)}
	for _, arg := range manifest.Args {
		args = append(args, expand(arg))
	}
	opts := Options{
		Args:     args,
		Clock:    clock.NewFake(now),
		Env:      &paths.MapEnv{HostnameValue: "golden-host"},
		Location: testZone,
	}
	if home, ok := manifest.Env["HOME"]; ok {
		// the roots Node discovered under this HOME, as pfm resolves them on such a host
		home = expand(home)
		opts.ClaudeRoots = append(opts.ClaudeRoots, filepath.Join(home, ".claude", "projects"))
		accounts, _ := os.ReadDir(filepath.Join(home, ".cc"))
		for _, account := range accounts {
			opts.ClaudeRoots = append(opts.ClaudeRoots, filepath.Join(home, ".cc", account.Name(), "projects"))
		}
		opts.CodexHomes = []string{filepath.Join(home, ".codex")}
	}
	return scenario{dir: dir, manifest: manifest, scratch: scratch, expand: expand, mask: mask, opts: opts}
}

// run audits the scenario with args in place of its recorded ones (nil: the
// recorded ones), --prices kept first.
func (s scenario) run(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	opts := s.opts
	if args != nil {
		opts.Args = append(append([]string(nil), s.opts.Args[:2]...), args...)
	}
	return auditWith(t, opts, opts.Args...)
}

func replayGolden(t *testing.T, s scenario) {
	code, stdout, stderr := s.run(t)
	want := func(name string) string {
		golden, err := os.ReadFile(filepath.Join(s.dir, name))
		if err != nil {
			t.Fatalf("read the golden %s: %v", name, err)
		}
		return intended(string(golden))
	}
	test := s.manifest.Test
	wantStderr := want("stderr")
	if wantCode := intendedExit(s.manifest.Exit, wantStderr); code != wantCode {
		t.Errorf("%s: exit %d, want %d (stderr %q)", test, code, wantCode, stderr)
	}
	compareText(t, test+": stdout", want("stdout"), s.mask(stdout))
	compareText(t, test+": stderr", wantStderr, s.mask(stderr))
	for flag, path := range s.manifest.Outputs {
		written, err := os.ReadFile(s.expand(path))
		if err != nil {
			t.Errorf("%s: %s %s was not written: %v", test, flag, path, err)
			continue
		}
		compareText(
			t,
			test+": "+flag,
			want(filepath.Join("out", strings.TrimPrefix(flag, "--"))),
			s.mask(string(written)),
		)
	}
}

// materialize copies a golden's input tree into scratch, its placeholders
// expanded in every file and link target.
func materialize(t *testing.T, from, scratch string, expand func(string) string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if os.IsNotExist(walkErr) && path == from {
				return filepath.SkipDir
			}
			return walkErr
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(scratch, relative)
		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(expand(link), target)
		case entry.IsDir():
			return os.MkdirAll(target, 0o755)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, []byte(expand(string(content))), 0o644)
	})
	if err != nil {
		t.Fatalf("materialize %s: %v", from, err)
	}
}

// compareText fails at the first differing byte, with its line number and
// 80 bytes of context on each side: a one-line JSON document differs mid-line.
func compareText(t *testing.T, what, want, got string) {
	t.Helper()
	if want == got {
		return
	}
	at := 0
	for at < len(want) && at < len(got) && want[at] == got[at] {
		at++
	}
	window := func(text string) string { return text[max(0, at-80):min(len(text), at+80)] }
	t.Errorf("%s differs at byte %d (line %d):\nwant %q\n got %q", what, at, strings.Count(want[:at], "\n")+1,
		window(want), window(got))
}

func TestIntendedRewritesTheNodeDeltas(t *testing.T) {
	cases := []struct{ node, pfm string }{
		{"token-audit: --since wants", "pfm tokens: --since wants"},
		{
			"token-audit: TOKEN_AUDIT_PRICES /t/x.json: ENOENT: no such file or directory, open '/t/x.json'",
			"pfm tokens: --prices /t/x.json: open /t/x.json: no such file or directory",
		},
		{
			"--root /r is not readable: ENOENT: no such file or directory, lstat '/r'",
			"--root /r is not readable: lstat /r: no such file or directory",
		},
		{"/d: EACCES: permission denied, scandir '/d'", "/d: open /d: permission denied"},
	}
	for _, c := range cases {
		if got := intended(c.node); got != c.pfm {
			t.Errorf("intended(%q) = %q, want %q", c.node, got, c.pfm)
		}
	}
	codes := []struct {
		code   int
		stderr string
		want   int
	}{
		{2, "token-audit: --since wants a number", 2},
		{2, "token-audit: --session x matched none of the 5 transcripts", 2},
		{2, "token-audit: --root /r is not readable: lstat /r", 1},
		{2, "token-audit: --session x matched 1 transcripts in this window but read no call from them", 1},
		{0, "", 0},
	}
	for _, c := range codes {
		if got := intendedExit(c.code, c.stderr); got != c.want {
			t.Errorf("intendedExit(%d, %q) = %d, want %d", c.code, c.stderr, got, c.want)
		}
	}
}

// testAuditor is a run over args at goldenNow in testZone, priced from the
// fixture document, its streams returned.
func testAuditor(t *testing.T, args ...string) (*auditor, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	parsed, err := readFlags(args, "test-host")
	if err != nil {
		t.Fatalf("readFlags(%v) = %v", args, err)
	}
	var stdout, stderr bytes.Buffer
	a := newAuditor(Options{Stdout: &stdout, Stderr: &stderr, Clock: clock.NewFake(goldenNow), Location: testZone},
		parsed, "test-host")
	if a.prices, err = loadSaved(fixture(t, "model-cost.json")); err != nil {
		t.Fatal(err)
	}
	return a, &stdout, &stderr
}

// assistant is one Claude assistant record: message id, model, instant, and
// input, output, cache-read and 5-minute cache-write tokens.
func assistant(id, model, at string, input, output, read, write5m int) string {
	return fmt.Sprintf(`{"type":"assistant","timestamp":%q,"cwd":"/w/demo","sessionId":"s1","message":{"id":%q,`+
		`"model":%q,"content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":%d,"output_tokens":%d,`+
		`"cache_read_input_tokens":%d,"cache_creation_input_tokens":%d,"cache_creation":`+
		`{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":0}}}}`,
		at, id, model, input, output, read, write5m, write5m)
}

// writeTranscript writes lines, one per line, at path under dir and returns path.
func writeTranscript(t *testing.T, path string, lines ...string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}
