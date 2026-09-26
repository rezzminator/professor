package hookentry

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/fleetdb"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// accountEnv is a hook environment homed at lab.root with seat as its
// CLAUDE_CONFIG_DIR when set.
func (lab *callmeterLab) accountEnv(seat string) paths.Env {
	lab.t.Helper()
	values := map[string]string{paths.EnvHome: lab.root}
	if seat != "" {
		values["CLAUDE_CONFIG_DIR"] = seat
	}
	env := &paths.MapEnv{HomeDir: lab.t.TempDir(), Values: values}
	lab.storePath = callmeter.DefaultPath(lab.root)
	return env
}

// feedEntry runs each payload through the hook's own entry under env.
func (lab *callmeterLab) feedEntry(env paths.Env, payloads ...string) {
	lab.t.Helper()
	for _, payload := range payloads {
		var stderr bytes.Buffer
		if code := Callmeter(strings.NewReader(payload), &stderr, env); code != 0 {
			lab.t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
		}
	}
}

// feedChat runs the scripted chat and one sub-agent batch through the entry:
// calls, a request and a sub-agent.
func (lab *callmeterLab) feedChat(env paths.Env) {
	lab.t.Helper()
	scripted := lab.payloads("scripted.jsonl")
	batch := fmt.Sprintf(
		`{"session_id":%q,"transcript_path":%q,"cwd":%q,"agent_id":%q,"agent_type":"general-purpose",`+
			`"hook_event_name":"PostToolBatch","tool_calls":[{"tool_name":"Read","tool_input":{},`+
			`"tool_use_id":"toolu_01V8dv8UQGCZREurDdztPdMd","tool_response":"abc"}]}`,
		cmSessionA, lab.transcript(cmSessionA), lab.proj, cmSubagent)
	lab.feedEntry(env, scripted[:7]...)
	lab.feedEntry(env, batch)
	lab.feedEntry(env, scripted[7:]...)
}

// expectSeat asserts every call, request and agent row carries account (nil
// for NULL) and seat.
func (lab *callmeterLab) expectSeat(account any, seat string) {
	lab.t.Helper()
	for _, table := range []string{"calls", "requests", "agents"} {
		if n := lab.count("SELECT COUNT(*) FROM " + table); n == 0 {
			lab.t.Fatalf("%s holds no row: nothing to check", table)
		}
		if n := lab.count(
			"SELECT COUNT(*) FROM "+table+" WHERE NOT (account IS ? AND seat_dir IS ?)",
			account,
			seat,
		); n != 0 {
			lab.t.Errorf(
				"%d %s rows lack account %v and seat_dir %s: %v",
				n,
				table,
				account,
				seat,
				lab.row(
					"SELECT account, seat_dir FROM "+table+" WHERE NOT (account IS ? AND seat_dir IS ?)",
					account,
					seat,
				),
			)
		}
	}
}

func (lab *callmeterLab) accountFaults() int {
	lab.t.Helper()
	return lab.count("SELECT COUNT(*) FROM faults WHERE stage = ?", callmeter.StageAccount)
}

func recordCallmeterLaunch(t *testing.T, home, session string, account int) {
	t.Helper()
	values := paths.Values{StateDB: paths.DefaultStateDB(home)}
	if err := fleetdb.RecordLaunch(context.Background(), values, fleetdb.Launch{
		SessionID: session, Engine: pfmengine.Claude, Account: account,
	}, 1); err != nil {
		t.Fatal(err)
	}
}

func TestCallmeterLaunchAccountAcrossRows(t *testing.T) {
	lab := newCallmeterLab(t)
	seat := filepath.Join(lab.root, "seat-2")
	recordCallmeterLaunch(t, lab.root, cmSessionA, 2)
	lab.feedChat(lab.accountEnv(seat))
	lab.expectSeat(2, seat)
	for _, table := range []string{"calls", "requests", "agents"} {
		if n := lab.count(
			"SELECT COUNT(*) FROM "+table+" WHERE config_dir IS NOT ?",
			filepath.Join(lab.root, ".claude"),
		); n != 0 {
			t.Errorf("%s rows with wrong shared config_dir = %d", table, n)
		}
	}
	if n := lab.accountFaults(); n != 0 {
		t.Errorf("account faults = %d", n)
	}
}

func TestCallmeterNoLaunchLeavesAccountNull(t *testing.T) {
	lab := newCallmeterLab(t)
	seat := filepath.Join(lab.root, "seat")
	lab.feedEntry(lab.accountEnv(seat), lab.payloads("scripted.jsonl")[0])
	if n := lab.count(
		"SELECT COUNT(*) FROM calls WHERE account IS NULL AND seat_dir = ? AND config_dir = ?",
		seat,
		filepath.Join(lab.root, ".claude"),
	); n != 1 {
		t.Errorf("NULL-account call with seat and shared store = %d, want 1", n)
	}
	if n := lab.accountFaults(); n != 0 {
		t.Errorf("account faults = %d", n)
	}
}

func TestCallmeterLaunchReadErrorIsOneAccountFault(t *testing.T) {
	lab := newCallmeterLab(t)
	seat := filepath.Join(lab.root, "seat")
	state := paths.DefaultStateDB(lab.root)
	if err := os.MkdirAll(filepath.Dir(state), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(state, []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	lab.feedEntry(lab.accountEnv(seat), lab.payloads("scripted.jsonl")[0])
	if n := lab.count("SELECT COUNT(*) FROM calls WHERE account IS NULL AND seat_dir = ?", seat); n != 1 {
		t.Errorf("NULL-account call = %d, want 1", n)
	}
	if n := lab.accountFaults(); n != 1 {
		t.Errorf("account faults = %d, want 1", n)
	}
	assertLogged(t, lab.rec, callmeter.StageAccount)
}
