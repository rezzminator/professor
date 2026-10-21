package installer

import (
	"encoding/json"
	"testing"
)

// Lock faults run the real extension, with virtual wait ticks rather than ten wall seconds.
const vscodeThemeLockSetupJS = `
const lockMode = mode.startsWith('lock-');
const applyLock = path.join(stateDir, 'vscode-apply.lock');
const ackLock = path.join(stateDir, 'vscode-ack.lock');
let unlockedAck = false;
let replacement = null;
if (lockMode) {
  const realTimeout = global.setTimeout;
  global.setTimeout = (fn, ms, ...args) => realTimeout(fn, 0, ...args);
  fs.mkdirSync(applyLock);
  if (mode === 'lock-stale') fs.utimesSync(applyLock, new Date(0), new Date(0));
  fs.writeFileSync(path.join(stateDir, 'current.json'), mode === 'lock-broken' ? '{broken' :
    JSON.stringify({ enabled: true, generation: 1, colors: { 'terminal.background': '#111111' } }));
  fs.writeFileSync(path.join(stateDir, 'vscode-ack.json'), JSON.stringify({ generation: 1,
    chrome: { other: { generation: 1, keys: [], error: 'other window failed' } } }));
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
