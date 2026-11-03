package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestLiveChatPIDs(t *testing.T) {
	root := t.TempDir()
	account, proc := filepath.Join(root, "account"), filepath.Join(root, "proc")
	for _, pid := range []string{"12", "3"} {
		if err := os.MkdirAll(filepath.Join(proc, pid), 0o700); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(account, "sessions", pid+".json"), "{}")
	}
	for _, name := range []string{"42.json", "not-a-pid.json", "12.txt"} {
		writeFixture(t, filepath.Join(account, "sessions", name), "{}")
	}
	if err := os.Mkdir(filepath.Join(account, "sessions", "99.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := liveChatPIDs(proc, account)
	if err != nil || !reflect.DeepEqual(got, []string{"12", "3"}) {
		t.Fatalf("live=%v err=%v", got, err)
	}
}

func TestLiveChatPIDsMissingAndUnreadable(t *testing.T) {
	root := t.TempDir()
	if got, err := liveChatPIDs(root, filepath.Join(root, "missing")); err != nil || len(got) != 0 {
		t.Fatalf("missing sessions=%v err=%v", got, err)
	}
	account := filepath.Join(root, "account")
	writeFixture(t, filepath.Join(account, "sessions"), "not a directory")
	if _, err := liveChatPIDs(root, account); err == nil {
		t.Fatal("unreadable sessions read as empty")
	}
}

// TestLiveChatDoorPlanMatchesApply pins the dry run as the apply's preview:
// with a live chat on the account, every caller of the live-chat door prints
// the same rows on a plan as on the apply that follows it on the same host.
func TestLiveChatDoorPlanMatchesApply(t *testing.T) {
	liveChat := func(t *testing.T, account, proc string) {
		t.Helper()
		writeFixture(t, filepath.Join(account, "sessions", "4242.json"), "{}")
		if err := os.MkdirAll(filepath.Join(proc, "4242"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, caller := range []string{"retired store entry", "store repoint", "fullscreen canary"} {
		t.Run(caller, func(t *testing.T) {
			home := t.TempDir()
			store, account, proc := ClaudeStore(home), filepath.Join(home, "account"), emptyProcRoot(t, home)
			accounts := []pfmconfig.Account{{ID: 1, ConfigDir: account}}
			var step func(*engine) error
			switch caller {
			case "retired store entry":
				name := fixtureStoreEntries.StoreEntries[0]
				writeFixture(t, filepath.Join(store, name), "shared bytes")
				symlinkFixture(t, filepath.Join(store, name), filepath.Join(account, name))
				step = (*engine).retireStoreEntries
			case "store repoint":
				if err := (&engine{options: Options{Home: home, ConfigDir: store, Stdout: io.Discard}, apply: true}).
					wireClaudeStore(); err != nil {
					t.Fatal(err)
				}
				symlinkFixture(t, filepath.Join(store, "gone"), filepath.Join(account, "agents"))
				step = (*engine).wireClaudeStore
			case "fullscreen canary":
				writeFixture(t, filepath.Join(account, "settings.json"), `{"tui":"fullscreen"}`)
				writeFixture(t, filepath.Join(account, ".claude.json"), canaryRegistry)
				step = (*engine).clearFullscreenAutoDisable
			}
			liveChat(t, account, proc)
			run := func(apply bool) string {
				var output bytes.Buffer
				installer := &engine{
					options: Options{
						Home: home, ConfigDir: store, ClaudeAccounts: accounts, RosterConfigDirs: []string{account},
						ProcRoot: proc, Stdout: &output, retired: &fixtureStoreEntries,
					},
					apply: apply, stamp: "test", managedRoot: managedRootForHome(home),
				}
				if err := step(installer); err != nil {
					t.Fatalf("apply=%v: %v\n%s", apply, err, output.String())
				}
				return output.String()
			}
			plan, applied := run(false), run(true)
			if plan != applied {
				t.Fatalf("plan is not the apply's preview:\nplan:\n%s\napply:\n%s", plan, applied)
			}
			if !strings.Contains(applied, "live chats") {
				t.Fatalf("apply deferred nothing to a live chat:\n%s", applied)
			}
		})
	}
}

// TestAccountChatsLiveCountsAChatOnce pins that a chat holding both a live
// session and a live launch claim is one live chat, not two.
func TestAccountChatsLiveCountsAChatOnce(t *testing.T) {
	dir, self := t.TempDir(), os.Getpid()
	launch, err := gather.AcquireAccountGuard(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := launch.Record(self); err != nil {
		t.Fatal(err)
	}
	if err := launch.Close(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := launch.Abort(); err != nil {
			t.Error(err)
		}
	})
	writeFixture(t, filepath.Join(dir, "sessions", strconv.Itoa(self)+".json"), "{}")
	install, err := gather.AcquireAccountGuard(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := install.Close(); err != nil {
			t.Error(err)
		}
	}()
	live, err := (&accountChats{dir: dir, guard: install}).live()
	if err != nil || !reflect.DeepEqual(live, []string{strconv.Itoa(self)}) {
		t.Fatalf("live = %v, %v; want [%d] once", live, err, self)
	}
}

func TestAccountEntryAttributesApplyAndLeavesObserversUncredited(t *testing.T) {
	for _, row := range []struct {
		name           string
		apply, guarded bool
		actions        []string
	}{
		{"apply", true, true, []string{"lifecycle.account-entry"}},
		{"preview", false, true, nil},
		{"observer", true, false, nil},
	} {
		t.Run(row.name, func(t *testing.T) {
			obs.Test(t)
			directory := t.TempDir()
			channel := filepath.Join(directory, "beat.json")
			writeFixture(t, channel, `{"beat":"A","run":"synthetic"}`)
			logPath := filepath.Join(directory, "activity.jsonl")
			_, finish := obs.OpenLog(context.Background(), obs.Settings{
				Cmd:  "log",
				Args: []string{"log"},
				Path: logPath,
				Env: &paths.MapEnv{
					Values: map[string]string{obs.EnvBeatFile: channel, obs.EnvBeat: "birth", obs.EnvRun: "birth"},
				},
			})
			defer finish(0)
			writeFixture(t, channel, `{"beat":"B","run":"synthetic"}`)
			account := filepath.Join(directory, "account")
			stats := filepath.Join(account, "stats-cache.json")
			writeFixture(t, stats, `{"version":2}`)
			installer := &engine{options: Options{ProcRoot: emptyProcRoot(t, directory)}, apply: row.apply}
			chats, err := installer.openAccountChats(account, row.guarded)
			if err != nil {
				t.Fatal(err)
			}
			if err := chats.release(); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(logPath)
			if err != nil {
				t.Fatal(err)
			}
			var actions []string
			for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
				var record map[string]any
				if err := json.Unmarshal(line, &record); err != nil {
					t.Fatal(err)
				}
				if action, found := record[obs.FieldAction]; found {
					actions = append(actions, action.(string))
					if record["beat"] != "B" || record["run"] != "synthetic" {
						t.Fatalf("%s: record = %v, want beat B/run synthetic", logPath, record)
					}
				}
			}
			if !reflect.DeepEqual(actions, row.actions) {
				t.Fatalf("%s: actions = %v, want %v; log=%s", logPath, actions, row.actions, raw)
			}
			assertContent(t, stats, `{"version":2}`)
		})
	}
}
