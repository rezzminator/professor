package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestTokensCLIEntry(t *testing.T) {
	jailTest(t)
	var out, errs bytes.Buffer
	if code := run([]string{"tokens", "--help"}, &out, &errs); code != 0 ||
		!strings.Contains(out.String(), "--codex") || !strings.Contains(out.String(), "--timeline") {
		t.Fatalf("tokens --help code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
	out.Reset()
	errs.Reset()
	// The jail sets PFM_PRICES_OFFLINE=1: the served table loads without a fetch.
	if code := run([]string{"tokens", "--root", t.TempDir()}, &out, &errs); code != 0 ||
		!strings.HasPrefix(out.String(), "TOKEN AUDIT · last 24h · ") {
		t.Fatalf("tokens over an empty root code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
	out.Reset()
	// the cost ledger directory is the one the sink writes: PFM_COST_LEDGER_DIR names it
	const session = "00000000-0000-4000-8000-0000000000c3"
	root, ledgerDir := t.TempDir(), t.TempDir()
	at := time.Now().UTC().Format(time.RFC3339)
	chat := fmt.Sprintf(
		`{"type":"assistant","timestamp":%q,"cwd":"/w/demo","requestId":"req_cli","message":{"id":"msg_cli",`+
			`"model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":2,"output_tokens":10}}}`,
		at,
	)
	if err := os.MkdirAll(filepath.Join(root, "-w-demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "-w-demo", session+".jsonl"), []byte(chat+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	row := fmt.Sprintf(`{"request_id":"req_cli","ts":%q,"session_id":%q,"agent_id":"","parent_agent_id":"",`+
		`"query_source":"repl_main_thread","model":"claude-sonnet-5","input_tokens":2,"output_tokens":10,`+
		`"cache_read_tokens":0,"cache_creation_tokens":0,"cost_usd_micros":2500000,"speed":"","effort":"","join":"log"}`, at, session)
	if err := os.WriteFile(filepath.Join(ledgerDir, session+".jsonl"), []byte(row+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvCostLedgerDir, ledgerDir)
	if code := run([]string{"tokens", "--root", root}, &out, &errs); code != 0 ||
		!strings.Contains(
			out.String(),
			"chat "+session,
		) || !strings.Contains(out.String(), "session total $2.50 (1 call)") {
		t.Fatalf("tokens over a chat with a cost ledger code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
	out.Reset()
	if code := run([]string{"tokens", "--since", "3x"}, &out, &errs); code != 2 ||
		!strings.Contains(errs.String(), "pfm tokens: --since wants a number plus h or d") {
		t.Fatalf("tokens --since 3x code=%d stderr=%q", code, errs.String())
	}
	out.Reset()
	if code := run([]string{"help"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "  tokens ") {
		t.Fatalf("help code=%d lacks tokens: %s", code, out.String())
	}
}
