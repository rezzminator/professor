package main

// The watch ladder's discovery rungs: the scan snapshot watches share, the
// fingerprint that spares a followed seat its scan, and the precheck's scan
// serving the first poll. They run on the ladder in chat_watch_ladder_test.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// shareLadder is a ladder whose watches share the snapshot at a path the test
// names through PFM_WATCH_SNAPSHOT.
func shareLadder(t *testing.T) (*watchLadder, string) {
	t.Helper()
	ladder := newWatchLadder(t)
	snapshot := filepath.Join(ladder.jail.root, "share", "watch-snapshot.json")
	t.Setenv(paths.EnvWatchSnapshot, snapshot)
	return ladder, snapshot
}

func writeLadderFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

// holdShareLock takes the snapshot's lock as a watch that began a scan just
// now and never finishes it would, until release.
func holdShareLock(t *testing.T, snapshot string) func() {
	t.Helper()
	lock, err := os.OpenFile(snapshot+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("lock %s: %v", lock.Name(), err)
	}
	stamp := strconv.FormatInt(time.Now().UnixNano(), 10) + " 1\n"
	if _, err := lock.WriteAt([]byte(stamp), 0); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { _ = lock.Close() }) }
	t.Cleanup(release)
	return release
}

// Rungs: the shared snapshot corrupt, torn by a writer that died mid-write,
// missing, and locked by a holder that never publishes — none of them hides a
// turn from the next poll.
func TestWatchLadderAnUnfitSharedSnapshotNeverHidesATurn(t *testing.T) {
	ladder, snapshot := shareLadder(t)
	seat := ladder.start("lad-a")
	watch := ladder.watch("lad-a", "--transitions")
	watch.expect("SEEN lad-a idle")
	published, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatalf("the watch's first poll published no snapshot: %v", err)
	}
	scratch := filepath.Join(filepath.Dir(snapshot), "."+filepath.Base(snapshot)+".tmp-dead")
	for _, fault := range []struct {
		name   string
		damage func() func()
	}{
		{"corrupt", func() func() { writeLadderFile(t, snapshot, []byte("{not a snapshot")); return func() {} }},
		{"torn by a writer that died mid-write", func() func() {
			writeLadderFile(t, snapshot, published[:len(published)/2])
			writeLadderFile(t, scratch, published[:64])
			return func() {}
		}},
		{"missing", func() func() {
			if err := os.Remove(snapshot); err != nil {
				t.Fatal(err)
			}
			return func() {}
		}},
		{"locked by a holder that never publishes", func() func() { return holdShareLock(t, snapshot) }},
	} {
		t.Log(fault.name)
		release := fault.damage()
		seat.turn()
		watch.poll()
		watch.expect("WORKING lad-a", "IDLE lad-a idle_seconds=")
		release()
	}
}

// Rungs: a snapshot that lists a seat alive — taken before its exit and put
// back after, or stamped in this clock's future as a clock stepped back would
// leave it — never makes the exited seat read alive.
func TestWatchLadderASharedSnapshotNeverOutlivesAnExit(t *testing.T) {
	for _, test := range []struct {
		name  string
		forge func(t *testing.T, alive []byte) []byte
	}{
		{"stale: taken before the exit", func(_ *testing.T, alive []byte) []byte { return alive }},
		{"stamped in this clock's future", func(t *testing.T, alive []byte) []byte {
			header, payload, _ := bytes.Cut(alive, []byte("\n"))
			var fields map[string]any
			if err := json.Unmarshal(header, &fields); err != nil {
				t.Fatal(err)
			}
			fields["start_ns"] = time.Now().Add(time.Hour).UnixNano()
			forged, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			return append(append(forged, '\n'), payload...)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			ladder, snapshot := shareLadder(t)
			seat := ladder.start("lad-a")
			watch := ladder.watch("lad-a", "--transitions")
			watch.expect("SEEN lad-a idle")
			alive, err := os.ReadFile(snapshot)
			if err != nil {
				t.Fatalf("the watch's first poll published no snapshot: %v", err)
			}
			seat.exit()
			writeLadderFile(t, snapshot, test.forge(t, alive))
			watch.poll()
			watch.expect("EXIT lad-a")
			watch.expectEnded(codeDeadChat)
		})
	}
}

// snapshotScan is when the shared snapshot's scan began: it moves only when a
// watch scanned.
func snapshotScan(t *testing.T, snapshot string) int64 {
	t.Helper()
	content, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	header, _, _ := bytes.Cut(content, []byte("\n"))
	var fields struct {
		StartNS int64 `json:"start_ns"`
	}
	if err := json.Unmarshal(header, &fields); err != nil {
		t.Fatalf("snapshot header %q: %v", header, err)
	}
	return fields.StartNS
}

// Rung: a followed seat whose fingerprint holds is not rescanned, and its
// turns still arrive; a /clear rewrites its crumb, which brings the scan back
// and moves the watch onto the new conversation.
func TestWatchLadderAQuietFollowedSeatIsNotRescanned(t *testing.T) {
	ladder, snapshot := shareLadder(t)
	seat := ladder.start("lad-a")
	watch := ladder.watch("lad-a", "--transitions")
	watch.expect("SEEN lad-a idle")
	// The polls after binding list the seat against its fingerprint until a
	// listing finds it as the fingerprint saw it: that pair is the baseline.
	watch.poll()
	watch.poll()
	watch.expect()
	scanned := snapshotScan(t, snapshot)
	seat.turn()
	watch.poll()
	watch.expect("WORKING lad-a", "IDLE lad-a idle_seconds=")
	watch.poll()
	watch.expect()
	if got := snapshotScan(t, snapshot); got != scanned {
		t.Fatalf("the watch rescanned (snapshot scan %d, was %d) a seat whose fingerprint held", got, scanned)
	}
	seat.typeLine("/clear")
	seat.waitPane("❯")
	ladder.replies++
	reply := fmt.Sprintf("reply-%d", ladder.replies)
	seat.typeLine(`mock-engine: [{"type":"turn","reply":"` + reply + `","busy_ms":0}]`)
	waitForReply(t, filepath.Dir(seat.transcript), reply)
	watch.poll()
	if got := snapshotScan(t, snapshot); got == scanned {
		t.Fatal("a /clear left the watch on its old row: no scan followed the rewritten crumb")
	}
	// The /clear itself may read as a turn; the next real turn is announced
	// once, last.
	lines := strings.Split(strings.TrimSuffix(watch.stdout.String(), "\n"), "\n")[watch.read:]
	idles := 0
	for _, line := range lines {
		if strings.HasPrefix(line, "IDLE lad-a idle_seconds=") {
			idles++
		}
	}
	if idles != 1 || !strings.HasPrefix(lines[len(lines)-1], "IDLE lad-a") {
		t.Fatalf(
			"after /clear the next turn was not announced once, last: %q (stderr %q)",
			lines,
			watch.stderr.String(),
		)
	}
}

// Rung (D6): a seat first listed without its socket — its socket file
// unlinked, or its server's probe timed out under load — is the same seat
// once its socket answers again: never an EXIT for the named watch, never a
// DEAD and a second SEEN for the glob, and its turns still arrive once each.
func TestWatchLadderASeatFirstSeenWithoutItsSocketKeepsItsWatch(t *testing.T) {
	ladder := newWatchLadder(t)
	seat := ladder.start("lad-a")
	if err := os.Remove(seat.socketPath()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if syscall.Kill(seat.pid, 0) == nil {
			seat.signalServer(syscall.SIGTERM)
		}
	})
	ladder.waitRow("lad-a", "", true)
	named := ladder.watch("lad-a", "--transitions")
	glob := ladder.watch("lad-*", "--transitions")
	named.expect("SEEN lad-a idle")
	glob.expect("SEEN lad-a idle")
	seat.signalServer(syscall.SIGUSR1) // tmux recreates its socket
	ladder.waitRow("lad-a", seat.socket, true)
	named.poll()
	named.expect()
	glob.poll()
	glob.expect()
	seat.turn()
	named.poll()
	named.expect("WORKING lad-a", "IDLE lad-a idle_seconds=")
	glob.poll()
	glob.expect("WORKING lad-a", "IDLE lad-a idle_seconds=")
	seat.exit()
	named.poll()
	named.expect("EXIT lad-a")
	glob.poll()
	glob.expect("EXIT lad-a")
}

// listings counts the tmux pane listings recorder holds: a fleet scan lists
// every server once.
func listings(recorder *obs.Recorder) int {
	count := 0
	for _, record := range recorder.Records() {
		if subcommand, _ := record.Field("subcmd"); record.Message == "tmux.exec" && subcommand == "list-panes" {
			count++
		}
	}
	return count
}

// Rung: a named watch checks its target exists on the scan its first poll
// then reads — a watch starts on one scan, not two.
func TestWatchLadderAWatchStartsOnOneScan(t *testing.T) {
	ladder, _ := shareLadder(t)
	ladder.start("lad-a")
	_, recorder := obs.Test(t)
	watch := ladder.watch("lad-a", "--idle-after", "3600")
	started := listings(recorder)
	// The second poll lists the fleet: the seat has no fingerprint baseline yet.
	watch.poll()
	scan := listings(recorder) - started
	if scan == 0 || started != scan {
		t.Fatalf("starting the watch listed the fleet's servers %d times; one scan lists them %d times", started, scan)
	}
	watch.expect()
}
