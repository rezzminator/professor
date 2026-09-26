package hookentry

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/callmeter"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// accountEnv is a hook environment homed at lab.root: its pfm config holds
// config when config is not "", and CLAUDE_CONFIG_DIR is seat when seat is not "".
func (lab *callmeterLab) accountEnv(config, seat string) paths.Env {
	lab.t.Helper()
	values := map[string]string{paths.EnvHome: lab.root}
	if seat != "" {
		values["CLAUDE_CONFIG_DIR"] = seat
	}
	env := &paths.MapEnv{HomeDir: lab.t.TempDir(), Values: values}
	if config != "" {
		path := pfmconfig.ResolvePathFrom(env, lab.root)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			lab.t.Fatalf("create config dir: %v", err)
		}
		lab.write(path, []byte(config))
	}
	lab.storePath = callmeter.DefaultPath(lab.root)
	return env
}

// accountConfig is a pfm config registering each id at its dir.
func accountConfig(dirs map[int]string) string {
	var entries []string
	for id, dir := range dirs {
		entries = append(entries, fmt.Sprintf(`{"id":%d,"configDir":%q}`, id, dir))
	}
	return `{"version":2,"accounts":[` + strings.Join(entries, ",") + `]}`
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

func TestCallmeterRecordsTheSeatAccount(t *testing.T) {
	lab := newCallmeterLab(t)
	seat := filepath.Join(lab.root, "seat-3")
	if err := os.MkdirAll(seat, 0o755); err != nil {
		t.Fatal(err)
	}
	lab.feedChat(lab.accountEnv(accountConfig(map[int]string{1: filepath.Join(lab.root, "seat-1"), 3: seat}), seat+"/"))
	lab.expectSeat(3, seat)
	if n := lab.accountFaults(); n != 0 {
		t.Errorf("account faults = %d, want 0", n)
	}
}

func TestCallmeterUnsetSeatIsHomeClaude(t *testing.T) {
	lab := newCallmeterLab(t)
	seat := filepath.Join(lab.root, ".claude")
	lab.feedChat(lab.accountEnv(accountConfig(map[int]string{1: seat}), ""))
	lab.expectSeat(1, seat)
}

// TestCallmeterSymlinkedSeatMatchesItsAccount: ~/.cc/3 links to ~/.claude3;
// the seat is recorded as named, and matches the account by its target.
func TestCallmeterSymlinkedSeatMatchesItsAccount(t *testing.T) {
	lab := newCallmeterLab(t)
	target := filepath.Join(lab.root, "claude3")
	link := filepath.Join(lab.root, "cc-3")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	lab.feedChat(lab.accountEnv(accountConfig(map[int]string{3: target}), link))
	lab.expectSeat(3, link)
}

func TestCallmeterUnregisteredSeatRecordsNoAccountAndNoFault(t *testing.T) {
	lab := newCallmeterLab(t)
	seat := filepath.Join(lab.root, "stray")
	if err := os.MkdirAll(seat, 0o755); err != nil {
		t.Fatal(err)
	}
	lab.feedChat(lab.accountEnv(accountConfig(map[int]string{1: filepath.Join(lab.root, "other")}), seat))
	lab.expectSeat(nil, seat)
	if n := lab.accountFaults(); n != 0 {
		t.Errorf("account faults = %d, want 0 for a seat no account names", n)
	}
}

// TestCallmeterUnloadableConfigIsOneAccountFault: a config pfm cannot load, or
// none at all, still records the seat, leaves the account NULL and says so in
// exactly one account fault for the hook run.
func TestCallmeterUnloadableConfigIsOneAccountFault(t *testing.T) {
	for name, config := range map[string]string{"missing": "", "unreadable": "{not json"} {
		t.Run(name, func(t *testing.T) {
			lab := newCallmeterLab(t)
			seat := filepath.Join(lab.root, "seat")
			lab.feedEntry(lab.accountEnv(config, seat), lab.payloads("scripted.jsonl")[0])
			if n := lab.count("SELECT COUNT(*) FROM calls WHERE account IS NULL AND seat_dir = ?", seat); n != 1 {
				t.Errorf("calls with NULL account and seat %s = %d, want 1", seat, n)
			}
			if n := lab.accountFaults(); n != 1 {
				t.Errorf("account faults = %d, want exactly 1", n)
			}
			assertLogged(t, lab.rec, callmeter.StageAccount)
		})
	}
}
