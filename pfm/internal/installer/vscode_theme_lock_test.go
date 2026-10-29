package installer

import (
	"encoding/json"
	"strings"
	"testing"
)

// Lock faults run the real extension, with virtual wait ticks rather than ten wall seconds.
const vscodeThemeLockSetupJS = `
const lockMode = mode.startsWith('lock-');
const applyLock = path.join(stateDir, 'vscode-apply.lock');
const ackLock = path.join(stateDir, 'vscode-ack.lock');
let unlockedAck = false;
let replacement = null;
const originalRead = fs.readFileSync;
if (lockMode) {
  const realTimeout = global.setTimeout;
  global.setTimeout = (fn, ms, ...args) => realTimeout(fn, 0, ...args);
  fs.mkdirSync(applyLock);
  if (mode.startsWith('lock-ack-') || mode.startsWith('lock-history-')) fs.rmSync(applyLock, { recursive: true });
  if (mode === 'lock-ack-busy' || mode.startsWith('lock-ack-exit')) fs.mkdirSync(ackLock);
  if (mode === 'lock-stale') fs.utimesSync(applyLock, new Date(0), new Date(0));
  fs.writeFileSync(path.join(stateDir, 'current.json'), mode === 'lock-broken' ? '{broken' :
    JSON.stringify({ enabled: true, generation: 1, colors: { 'terminal.background': '#111111' } }));
  fs.writeFileSync(path.join(stateDir, 'vscode-ack.json'), JSON.stringify({ generation: 1,
    chrome: { other: { generation: 1, keys: [], error: 'other window failed' } } }));
  if (mode === 'lock-ack-exit-null') fs.writeFileSync(path.join(stateDir, 'current.json'), '{broken');
  if (mode === 'lock-history-malformed' || mode === 'lock-ack-restart-history') fs.writeFileSync(path.join(stateDir, 'vscode-ack.json'), '{unknown history');
  const read = fs.readFileSync;
  if (mode === 'lock-history-unreadable' || mode === 'lock-ack-restart-unreadable') fs.readFileSync = (file, ...args) => {
    if (path.basename(file) === 'vscode-ack.json') { const err = new Error('injected ACK read failure'); err.code = 'EACCES'; throw err; }
    return read(file, ...args);
  };
  const write = fs.writeFileSync;
  fs.writeFileSync = (file, ...args) => {
    if (path.basename(file).startsWith('.vscode-ack.json.') && !fs.existsSync(ackLock)) unlockedAck = true;
    return write(file, ...args);
  };
  if (mode === 'lock-replaced') {
    fs.rmSync(applyLock, { recursive: true });
    const config = vscodeStub.workspace.getConfiguration;
    vscodeStub.workspace.getConfiguration = (section) => {
      const result = config(section);
      if (section !== 'workbench') return result;
      const update = result.update;
      result.update = (...args) => {
        fs.renameSync(applyLock, applyLock + '.original');
        fs.mkdirSync(applyLock);
        replacement = fs.statSync(applyLock).ino;
        return update(...args);
      };
      return result;
    };
  }
}
`

const vscodeThemeLockResultJS = `
if (mode.startsWith('lock-ack-exit') || mode.startsWith('lock-ack-restart')) {
  (async () => {
    const failuresDir = path.join(stateDir, 'vscode-ack-failures');
    const readFailures = () => fs.existsSync(failuresDir) ? Object.fromEntries(fs.readdirSync(failuresDir)
      .filter((name) => name.endsWith('.json')).map((name) => [name, JSON.parse(originalRead(path.join(failuresDir, name), 'utf8'))])) : {};
    const before = readFailures();
    if (mode.startsWith('lock-ack-restart')) {
      const prior = Object.values(before).find((failure) => failure.owner);
      const scope = prior?.owner?.scope;
      const markers = {
        'live.json': { pid: process.pid, scope },
        'foreign.json': { pid: prior?.owner?.pid, scope: 'foreign-boot-namespace' },
        'unknown.json': {},
        'denied.json': { pid: 2147483647, scope },
      };
      for (const [name, owner] of Object.entries(markers)) fs.writeFileSync(path.join(failuresDir, name),
        JSON.stringify({ generation: null, target: 'global', error: name + ' protected', owner }));
      const kill = process.kill;
      process.kill = (pid, signal) => {
        if (pid === 2147483647) { const err = new Error('injected probe permission denial'); err.code = 'EPERM'; throw err; }
        return kill(pid, signal);
      };
    }
    await syncTheme();
    if (mode.startsWith('lock-ack-exit')) fs.rmSync(ackLock, { recursive: true }); // fixture ends; not the extension's release
    process.stdout.write(JSON.stringify({ before, failures: readFailures(), raw: originalRead(path.join(stateDir, 'vscode-ack.json'), 'utf8') }));
  })().catch((err) => { process.stderr.write(String(err.stack || err)); process.exit(1); });
  return;
}
if (mode.startsWith('lock-ack-') || mode.startsWith('lock-history-')) {
  (async () => {
    await syncTheme();
    const ackPath = path.join(stateDir, 'vscode-ack.json');
    // Bypass only the fixture's read fault to inspect the actual disk artifact.
    const raw = originalRead(ackPath, 'utf8');
    const failuresDir = path.join(stateDir, 'vscode-ack-failures');
    const failures = fs.existsSync(failuresDir) ? fs.readdirSync(failuresDir).map((file) => readJSONFile('vscode-ack-failures/' + file)) : [];
    if (mode === 'lock-ack-busy') fs.rmSync(ackLock, { recursive: true });
    fs.readFileSync = originalRead;
    fs.writeFileSync(ackPath, JSON.stringify({ generation: 1, chrome: { other: { generation: 1, error: 'other window failed' } } }));
    // An unrelated owner's diagnostic must survive this window's successful retry.
    fs.mkdirSync(failuresDir, { recursive: true });
    fs.writeFileSync(path.join(failuresDir, 'other-owner.json'), JSON.stringify({ generation: 1, error: 'other owner failed' }));
    await syncTheme();
    const remaining = fs.readdirSync(failuresDir);
    process.stdout.write(JSON.stringify({ raw, failures, remaining, ack: readJSONFile('vscode-ack.json'), unlockedAck,
      ackLockKept: mode !== 'lock-ack-busy' || failures.length > 0 }));
  })().catch((err) => { process.stderr.write(String(err.stack || err)); process.exit(1); });
  return;
}
if (lockMode) {
  syncTheme().then(() => {
    const lockExists = fs.existsSync(applyLock);
    const ack = readJSONFile('vscode-ack.json');
    process.stdout.write(JSON.stringify({ writes, unlockedAck, lockExists, ack,
      replacementKept: replacement !== null && lockExists && fs.statSync(applyLock).ino === replacement }));
  }).catch((err) => { process.stderr.write(String(err.stack || err)); process.exit(1); });
  return;
}
`

func TestVSCodeAckPublicationFailurePreservesHistoryAndRecoversOnlyItsOwner(t *testing.T) {
	for _, mode := range []string{"lock-ack-busy", "lock-history-malformed", "lock-history-unreadable"} {
		t.Run(mode, func(t *testing.T) {
			var got struct {
				Raw      string
				Failures []struct {
					Generation int
					Error      string
				}
				Remaining   []string
				Ack         vscodeThemeAck
				UnlockedAck bool
			}
			if err := json.Unmarshal(runVSCodeThemeDriver(t, mode, t.TempDir(), nil), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Failures) == 0 || got.Failures[0].Generation != 1 || got.Failures[0].Error == "" {
				t.Errorf("current ACK publication failure left stale success: %+v", got)
			}
			if mode == "lock-history-malformed" && got.Raw != "{unknown history" {
				t.Errorf("unknown ACK history overwritten: %s", got.Raw)
			}
			if mode != "lock-history-malformed" && !strings.Contains(got.Raw, "other window failed") {
				t.Errorf("other window's history lost: %s", got.Raw)
			}
			if got.UnlockedAck {
				t.Error("ACK written without mutex")
			}
			if len(got.Remaining) != 1 || got.Remaining[0] != "other-owner.json" {
				t.Errorf("recovery did not clear only own evidence: %v", got.Remaining)
			}
			if got.Ack.Error != "" || got.Ack.Chrome["other"].Error != "other window failed" {
				t.Errorf("recovery lost history: %+v", got.Ack)
			}
		})
	}
}

func TestVSCodeAckRestartRecoversOnlyProvenDeadOwners(t *testing.T) {
	for _, exitMode := range []string{"lock-ack-exit", "lock-ack-exit-null"} {
		for _, historyMode := range []string{"lock-ack-restart-history", "lock-ack-restart-unreadable"} {
			t.Run(exitMode+"/"+historyMode, func(t *testing.T) {
				dir := t.TempDir()
				type outcome struct {
					Failures map[string]json.RawMessage
					Raw      string
				}
				run := func(mode string) outcome {
					t.Helper()
					var result outcome
					if err := json.Unmarshal(runVSCodeThemeDriver(t, mode, dir, nil), &result); err != nil {
						t.Fatal(err)
					}
					return result
				}
				dead := run(exitMode)
				if len(dead.Failures) != 1 {
					t.Fatalf("failed predecessor lacks evidence: %+v", dead)
				}
				broken := run(historyMode)
				for name := range dead.Failures {
					if _, ok := broken.Failures[name]; !ok {
						t.Errorf("unknown ACK history allowed predecessor deletion: %s", name)
					}
				}
				if historyMode == "lock-ack-restart-history" && broken.Raw != "{unknown history" {
					t.Errorf("malformed history overwritten: %s", broken.Raw)
				}
				if historyMode == "lock-ack-restart-unreadable" &&
					!strings.Contains(broken.Raw, "other window failed") {
					t.Errorf("unreadable history overwritten: %s", broken.Raw)
				}
				recovered := run("lock-ack-restart")
				for name := range broken.Failures {
					protected := name == "live.json" || name == "foreign.json" || name == "unknown.json" ||
						name == "denied.json"
					_, exists := recovered.Failures[name]
					if exists != protected {
						t.Errorf("restart evidence %s exists=%v, protected=%v", name, exists, protected)
					}
				}
				if !strings.Contains(recovered.Raw, "other window failed") {
					t.Errorf("successful restart lost other-window history: %s", recovered.Raw)
				}
			})
		}
	}
}

func TestVSCodeThemeLocksPreserveOtherWindows(t *testing.T) {
	for _, mode := range []string{"lock-busy", "lock-broken", "lock-stale", "lock-replaced"} {
		t.Run(mode, func(t *testing.T) {
			var got struct {
				Writes          int
				UnlockedAck     bool
				LockExists      bool
				ReplacementKept bool
				Ack             vscodeThemeAck
			}
			if err := json.Unmarshal(runVSCodeThemeDriver(t, mode, t.TempDir(), nil), &got); err != nil {
				t.Fatal(err)
			}
			if got.UnlockedAck {
				t.Error("failure acknowledgement published without the acknowledgement lock")
			}
			if got.Ack.Chrome["other"].Error != "other window failed" || got.Ack.Error == "" {
				t.Errorf("failure or other window outcome lost: %+v", got.Ack)
			}
			if !got.LockExists {
				t.Error("another window's lock was removed")
			}
			if mode == "lock-replaced" {
				if !got.ReplacementKept {
					t.Error("writer released a replacement lock")
				}
			} else if got.Writes != 0 {
				t.Errorf("settings writes = %d while another window holds the lock", got.Writes)
			}
		})
	}
}
