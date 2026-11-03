"""Refused-shape spellings and the three planted forms for each source language."""

import ast
import re

from core import Unavailable
from source_analysis import normalized

# These are source spellings, not parsers: Go and shell syntax is parsed separately.
SHAPES = {
    "fail-open-verdict": ("verdict", r"\|\|\s*(?:true|:)|continue-on-error:\s*true|except[^:]*:\s*pass|if (?:err :=[^;]+;\s*)?err != nil\s*\{\s*return nil", [
        "check || true", "check || :", "continue-on-error: true"], [
        "if err != nil { return nil }", "if err != nil {\n return nil\n}", "if err := check(); err != nil {\n return nil\n}"]),
    "weak-oracle": ("test", r"assert\.(?:True|False|Len|NotNil|NotEmpty)\(|grep\s+-q\s|\[\s+-[ens]\s|assert\s+(?:exists|status|exit-code)\b", [
        "grep -q ready output", '[ -n "$actual" ]', "assert exists result"], [
        "assert.True(t, ready)", "assert.Len(t, rows, 3)", "assert.NotNil(t, row)"]),
    "tautological-oracle": ("test", r"(?:expected|want)\s*[:=]+\s*(?:\$\(?pfm|\$actual|product\.)|UPDATE_(?:GOLDENS|SNAPSHOTS)=1", [
        "expected=$(pfm chat ls)", "want=$actual", "UPDATE_GOLDENS=1 check"], [
        "want := product.Render()", "expected = product.Load()", "want = product.Format(input)"]),
    "unscoped-read": ("shared", r"SELECT\s+(?:COUNT\([^)]*\)|\*)\s+FROM\s+[a-z_]+\s*(?:[\"';]|$)|SELECT[^\n]*FROM[^\n]*LIMIT 1", [
        'sqlite3 db "SELECT * FROM chats"', 'sqlite3 db "SELECT COUNT(*) FROM chats"', 'sqlite3 db "SELECT id FROM chats LIMIT 1"'], [
        'db.Query("SELECT * FROM chats")', 'db.Query("SELECT COUNT(*) FROM chats")', 'db.Query("SELECT id FROM chats LIMIT 1")']),
    "wall-clock-wait": ("test-harness", r"\bsleep\s+[0-9]|time\.(?:Sleep|After|NewTimer)\(", [
        "sleep 1", "sleep 0.1", "command sleep 2"], [
        "time.Sleep(time.Second)", "time.After(5*time.Second)", "time.NewTimer(time.Minute)"]),
    "consuming-probe": ("harness", r"(?:queue|stream)\.(?:Receive|Pop)\(|(?:read-log|consume-queue)\b|tail[^\n]*(?:activity|events)\.log", [
        "consume-queue inbox", "read-log activity.log", "tail -n 1 activity.log"], [
        "queue.Receive(ctx)", "stream.Pop()", "queue.Pop(ctx)"]),
    "toolchain-as-product": ("test", r'exec\.Command\("(?:go|npm|golangci-lint|gofmt)"|(?:^|[;& ]|do:)(?:go build|go vet|npm install|npm ci|gofmt|golangci-lint)\b|(?:go\.mod|package-lock\.json|\.github/workflows/)', [
        "go build ./cmd/pfm", "npm ci", "golangci-lint run"], [
        'exec.Command("go", "build")', 'os.ReadFile("go.mod")', 'os.ReadFile("package-lock.json")']),
    "shipped-migration-content": ("test", r'(?:migrations/[0-9]|migrate-to-version|(?:Restore|Load)Snapshot\(|(?:seed|fixture)[^\n]*migration)', [
        "cat migrations/001.sql", "pfm migrate-to-version 3", "fixture migration.sql"], [
        'os.ReadFile("migrations/001.sql")', "RestoreSnapshot(path)", "LoadSnapshot(path)"]),
    "lint-as-test": ("test", r'(?:ReadFile|ReadString|cat|grep)[^\n]*(?:pfm/internal/[^\n]*\.go|pfm/cmd/[^\n]*\.go)|runtime\.(?:Caller|FuncForPC)\(', [
        "cat pfm/internal/chat/open.go", "grep private pfm/cmd/pfm/main.go", "cat pfm/internal/store/store.go"], [
        'os.ReadFile("pfm/internal/chat/open.go")', "runtime.Caller(0)", "runtime.FuncForPC(ptr)"]),
    "tombstone": ("test", r'assert[^\n]*(?:not-exists|absent|removed)|(?:NotExist|NotContains)\(|(?:test|t\.Run)[^\n]*(?:stays.absent|removed.function)', [
        "assert not-exists old-file", "assert absent old-key", "test removed-function"], [
        "assert.NotContains(t, schema, oldKey)", "os.IsNotExist(err)", 't.Run("stays-absent", check)']),
    "self-skip": ("test", r't\.Skip(?:f|Now)?\(|\bpytest\.skip\(|\b(?:skip|pending|xfail)\s|\[.*(?:PFM_TEST|TEST_MODE)', [
        "skip unavailable", "pending future", "xfail optional"], [
        't.Skip("unavailable")', 't.Skipf("missing %s", name)', "t.SkipNow()"]),
    "double-in-production": ("production-test", r'(?:func|type|class)\s+(?:[A-Za-z_]*(?:Fake|Mock|Stub)[A-Za-z_]*)|(?:PFM_TEST|TEST_MODE|USE_MOCK)|mock\.(?:Patch|Replace)\(', [
        "PFM_TEST=1 pfm", "USE_MOCK=yes pfm", "TEST_MODE=on pfm"], [
        "func FakeClock() {}", "type MockEngine struct {}", 'os.Getenv("USE_MOCK")']),
    "test-cited-by-production": ("production", r'(?:infra/fence/(?:journey|lanes)/|_test\.go|testdata/|(?:chat-birth|chat-mesh)/[a-z-]+@)', [
        "source infra/fence/journey/lib.sh", "read pfm/internal/store/store_test.go", "load testdata/result"], [
        'const path = "infra/fence/journey/lib.sh"', 'const path = "store_test.go"', 'const path = "testdata/result"']),
    "second-logging-path": ("logging", r'fmt\.(?:Println|Printf|Fprintf)\(|log\.(?:Print|Printf|Println|New)\(|(?:echo|printf)[^\n]*>&2', [
        "echo broken >&2", "printf error >&2", 'echo "$error" >&2'], [
        "fmt.Println(err)", "log.Printf(\"error: %s\", err)", "fmt.Fprintf(os.Stderr, \"error\")"]),
    "line-anchored-exemption": ("allowlist", r'\.(?:go|sh|py|js):[0-9]+', [
        "open.go:12", "entry.sh:24", "scanner.py:9"], [
        '"open.go:12"', '"entry.sh:24"', '"scanner.py:9"']),
    "machine-in-fixture": ("fixture", r'/(?:home|Users)/[A-Za-z]|(?:"pid"|PID)\s*[:=]\s*[0-9]+|"(?:ts|timestamp)"\s*:', [
        'fixture=' + '/' + 'home/owner/work/atlas', 'fixture=' + '/' + 'Users/owner/work/atlas', 'PID=1234'], [
        'const dir = "' + '/' + 'home/owner/work/atlas"', 'const row = `{"pid":1234}`', 'const row = `{"ts":"now"}`']),
    "internal-entry-as-action": ("lane", r'pfm\s+(?:internal|__|hook)\b|"pfm"\s*,\s*"(?:internal|__|hook)"|PFM_INTERNAL|herdr-native\.c', [
        "pfm internal launch", 'act "pfm internal reload"', 'pane_input pane "pfm internal kill"'], [
        'exec.Command("pfm", "internal", "launch")', 'exec.Command("pfm", "hook", "stop")', 'const env = "PFM_INTERNAL"']),
}

COMPILED = {shape: re.compile(rule, re.I | re.M) for shape, (_, rule, _, _) in SHAPES.items()}


def in_scope(scope, path):
    test = path.endswith("_test.go") or path.endswith(".txtar") or "/journey/lanes/" in path
    harness = path.startswith("infra/fence/journey/") and path.endswith((".sh", ".py"))
    production = path.startswith(("pfm/cmd/", "pfm/internal/")) and path.endswith(".go") and not path.endswith("_test.go")
    if scope == "test":
        return test
    if scope == "verdict":
        return harness or (path.endswith((".go", ".sh", ".py", ".yml", ".yaml", ".js", ".ts"))
                           and (path.startswith(("scripts/", ".github/workflows/")) or "lintrules/" in path))
    if scope == "shared":
        return harness
    if scope == "test-harness":
        return test or harness
    if scope == "harness":
        return harness and not path.endswith("/lib.sh")
    if scope == "production-test":
        return production or test
    if scope == "production":
        return production
    if scope == "logging":
        return production and not path.startswith(("pfm/cmd/", "pfm/internal/obs/"))
    if scope == "allowlist":
        return any(word in path for word in ("allowlist", "exemptions", ".baseline"))
    if scope == "fixture":
        return "/fixtures/" in path or "/testdata/" in path
    if scope == "lane":
        return "/journey/lanes/" in path
    return False


def line_shapes(path, text):
    findings = []
    for shape, (scope, _, _, _) in SHAPES.items():
        if not in_scope(scope, path):
            continue
        if shape == "fail-open-verdict" and path.endswith(".py"):
            try:
                tree = ast.parse(text)
            except SyntaxError as error:
                raise Unavailable(f"could not parse {path}: {error}") from error
            for node in ast.walk(tree):
                if isinstance(node, ast.ExceptHandler) and len(node.body) == 1:
                    body = node.body[0]
                    empty_return = isinstance(body, ast.Return) and (body.value is None or isinstance(body.value, ast.Constant) and body.value.value is None)
                    if isinstance(body, ast.Pass) or empty_return:
                        findings.append((node.lineno, shape, normalized(ast.unparse(node))))
            continue
        for match in COMPILED[shape].finditer(text):
            number = text.count("\n", 0, match.start()) + 1
            line = text.splitlines()[number - 1]
            if line.lstrip().startswith(("#", "//")):
                continue
            if shape == "line-anchored-exemption" and "\t" in line and not COMPILED[shape].search(line.split("\t", 1)[0]):
                continue
            start = text.rfind("\n", 0, match.start()) + 1
            end = text.find("\n", match.end())
            statement = text[start:end if end >= 0 else len(text)]
            findings.append((number, shape, normalized(statement)))
    return findings
