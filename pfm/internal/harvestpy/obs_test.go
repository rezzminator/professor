package harvestpy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"hostops/pfm/internal/deps"
	"hostops/pfm/internal/obs"
)

// wantField fails unless record carries key with exactly want.
func wantField(t *testing.T, record obs.Record, key string, want any) {
	t.Helper()
	got, found := record.Field(key)
	if !found {
		t.Fatalf("record %s has no %q: %v", record.Message, key, record.Fields)
	}
	if got != want {
		t.Fatalf("record %s %s = %v (%T), want %v (%T)", record.Message, key, got, got, want, want)
	}
}

// harvestpyRecords isolates comp=harvestpy records from whatever else the
// call path also logs (e.g. the runner seam's own comp=runner records when
// the Runner in play is itself wrapped) — this test's scope is the Process
// middleware these items wire, not every door a call happens to cross.
func harvestpyRecords(records []obs.Record) []obs.Record {
	filtered := make([]obs.Record, 0, len(records))
	for _, record := range records {
		if comp, _ := record.Field(obs.FieldComp); comp == "harvestpy" {
			filtered = append(filtered, record)
		}
	}
	return filtered
}

// wantOp fails unless record is the harvestpy op record for comp=harvestpy
// and the given kind.
func wantOp(t *testing.T, record obs.Record, op, kind string) {
	t.Helper()
	if record.Message != "harvestpy."+op {
		t.Fatalf("record = %q, want harvestpy.%s", record.Message, op)
	}
	wantField(t, record, obs.FieldComp, "harvestpy")
	wantField(t, record, "op", op)
	wantField(t, record, "kind", kind)
}

// TestConverterEnsureAndCloseRecordProcessLifecycle pins item 12/13/14: the
// converter worker's start, its one convert request, and stop/kill/exit at
// Close — recorded through obs.NewProcess, never the request body or the
// response line.
func TestConverterEnsureAndCloseRecordProcessLifecycle(t *testing.T) {
	_, recorder := obs.Test(t)
	python := fakePython(t, `
import json,sys
for line in sys.stdin:
    req=json.loads(line)
    print(json.dumps({"ok":True,"markdown":"# converted"}), flush=True)
`)
	converter := testConverter(t, python)
	if _, err := converter.Convert(context.Background(), Request{Path: "one", Kind: "html"}); err != nil {
		t.Fatal(err)
	}
	if err := converter.Close(); err != nil {
		t.Fatal(err)
	}

	records := harvestpyRecords(recorder.Records())
	wantOps := []string{"start", "request", "stop", "kill", "exit"}
	if len(records) != len(wantOps) {
		t.Fatalf("comp=harvestpy records = %d, want %d: %s", len(records), len(wantOps), recorder.Raw())
	}
	wantOp(t, records[0], "start", "converter")
	if pid, _ := records[0].Field(obs.FieldPID); pid == nil || pid.(float64) <= 0 {
		t.Fatalf("start pid = %v, want > 0", pid)
	}
	wantOp(t, records[1], "request", "converter")
	wantField(t, records[1], "subcmd", "convert")
	if bytes, _ := records[1].Field("bytes"); bytes == nil || bytes.(float64) <= 0 {
		t.Fatalf("request bytes = %v, want > 0", bytes)
	}
	if _, found := records[1].Field(obs.FieldDur); !found {
		t.Fatalf("request record carries no dur_ms: %v", records[1].Fields)
	}
	wantOp(t, records[2], "stop", "converter")
	wantField(t, records[2], "cause", "close")
	wantOp(t, records[3], "kill", "converter")
	wantOp(t, records[4], "exit", "converter")
	if strings.Contains(recorder.Raw(), "# converted") {
		t.Fatalf("the response line reached the activity log: %s", recorder.Raw())
	}
}

// TestBrowserWorkerEnsureAndCloseRecordProcessLifecycle pins item 15/16/17
// with a scripted FakeRunner (deterministic pid, no real process race).
func TestBrowserWorkerEnsureAndCloseRecordProcessLifecycle(t *testing.T) {
	_, recorder := obs.Test(t)
	worker := NewBrowserWorker(Runtime{
		Python: "fake-browser", Script: "script",
		Runner: browserTestRunner(t, []string{"https://a.example.test/", "https://b.example.test/"}, "rendered"),
	})
	html, status, err := worker.Fetch(
		context.Background(), "https://a.example.test/", "", true, 45000,
		func(string) error { return nil },
	)
	if err != nil {
		t.Fatal(err)
	}
	if status != 200 || !strings.Contains(html, "rendered") {
		t.Fatalf("fetch result = %q/%d", html, status)
	}
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}

	records := harvestpyRecords(recorder.Records())
	wantOps := []string{"start", "request", "stop", "kill", "exit"}
	if len(records) != len(wantOps) {
		t.Fatalf("comp=harvestpy records = %d, want %d: %s", len(records), len(wantOps), recorder.Raw())
	}
	wantOp(t, records[0], "start", "browser")
	wantField(t, records[0], obs.FieldPID, float64(7001))
	wantOp(t, records[1], "request", "browser")
	wantField(t, records[1], "subcmd", "fetch")
	if bytes, _ := records[1].Field("bytes"); bytes == nil || bytes.(float64) <= 0 {
		t.Fatalf("request bytes = %v, want > 0", bytes)
	}
	wantOp(t, records[2], "stop", "browser")
	wantField(t, records[2], "cause", "close")
	wantOp(t, records[3], "kill", "browser")
	if _, found := records[3].Field(obs.FieldErr); found {
		t.Fatalf("a clean group kill carries an err: %v", records[3].Fields)
	}
	wantOp(t, records[4], "exit", "browser")
	if strings.Contains(recorder.Raw(), "rendered") {
		t.Fatalf("the response html reached the activity log: %s", recorder.Raw())
	}
}

// TestCheckInterpreterRecordsProcessLifecycle pins item 18: a version probe
// is one Process start/request/exit under kind=check.
func TestCheckInterpreterRecordsProcessLifecycle(t *testing.T) {
	ctx, recorder := obs.Test(t)
	runner := &deps.FakeRunner{}
	runner.Script(
		[]string{"python", "--version"},
		deps.RunResult{Stdout: []byte("Python 3.11.15\n"), ExitCode: 0},
		nil,
	)
	if err := checkInterpreter(ctx, runner, "python", "3.11.15+20260610"); err != nil {
		t.Fatal(err)
	}

	records := harvestpyRecords(recorder.Records())
	wantOps := []string{"start", "request", "exit"}
	if len(records) != len(wantOps) {
		t.Fatalf("comp=harvestpy records = %d, want %d: %s", len(records), len(wantOps), recorder.Raw())
	}
	wantOp(t, records[0], "start", "check")
	if _, found := records[0].Field(obs.FieldPID); found {
		t.Fatalf("a runner.Run probe has no pid: %v", records[0].Fields)
	}
	wantOp(t, records[1], "request", "check")
	wantField(t, records[1], "subcmd", "version")
	wantField(t, records[1], "bytes", float64(len("Python 3.11.15\n")))
	wantOp(t, records[2], "exit", "check")
	wantField(t, records[2], obs.FieldExit, float64(0))
	if strings.Contains(recorder.Raw(), "Python 3.11.15") {
		t.Fatalf("the interpreter's stdout reached the activity log: %s", recorder.Raw())
	}
}

// TestRunCommandWithRunnerRecordsProcessLifecycle pins item 19: a
// provisioning command is one Process start/request/exit under kind=provision,
// with every stderr line surfaced as its own WARN record.
func TestRunCommandWithRunnerRecordsProcessLifecycle(t *testing.T) {
	ctx, recorder := obs.Test(t)
	runner := &deps.FakeRunner{}
	runner.Script(
		[]string{"uv", "pip", "list"},
		deps.RunResult{
			Stdout: []byte("fixture==1.0\n"), Stderr: []byte("Using Python 3.11.15 environment at: .venv\n"),
			ExitCode: 0,
		},
		nil,
	)
	output, err := runCommandWithRunner(ctx, runner, "uv", []string{"pip", "list"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "fixture==1.0\n" {
		t.Fatalf("output = %q", output)
	}

	records := harvestpyRecords(recorder.Records())
	wantOps := []string{"start", "stderr", "request", "exit"}
	if len(records) != len(wantOps) {
		t.Fatalf("comp=harvestpy records = %d, want %d: %s", len(records), len(wantOps), recorder.Raw())
	}
	wantOp(t, records[0], "start", "provision")
	if records[1].Message != "harvestpy.stderr" {
		t.Fatalf("record 1 = %q, want harvestpy.stderr", records[1].Message)
	}
	wantField(t, records[1], "line", "Using Python 3.11.15 environment at: .venv")
	wantOp(t, records[2], "request", "provision")
	wantField(t, records[2], "subcmd", "uv")
	wantField(t, records[2], "bytes", float64(len("fixture==1.0\n")))
	wantOp(t, records[3], "exit", "provision")
	wantField(t, records[3], obs.FieldExit, float64(0))
	if strings.Contains(recorder.Raw(), "fixture==1.0") {
		t.Fatalf("stdout reached the activity log: %s", recorder.Raw())
	}
}

// TestDownloadFileWrapsTheClientForHTTPOut pins item 10: downloadFile's
// request goes through obs.WrapClient, never http.DefaultClient directly.
func TestDownloadFileWrapsTheClientForHTTPOut(t *testing.T) {
	ctx, recorder := obs.Test(t)
	body := "the pinned artifact bytes"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(body))
	}))
	defer server.Close()
	path := t.TempDir() + "/artifact"
	if err := downloadFile(ctx, server.URL, path, int64(len(body))); err != nil {
		t.Fatal(err)
	}

	records := recorder.Records()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1: %s", len(records), recorder.Raw())
	}
	record := records[0]
	if record.Message != "http.out.request" {
		t.Fatalf("record = %q, want http.out.request", record.Message)
	}
	wantField(t, record, obs.FieldComp, "http.out")
	wantField(t, record, "method", http.MethodGet)
	wantField(t, record, "status", float64(http.StatusOK))
	if strings.Contains(recorder.Raw(), body) {
		t.Fatalf("the downloaded body reached the activity log: %s", recorder.Raw())
	}
}
