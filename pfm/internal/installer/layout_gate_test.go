package installer

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

const layoutGateRemedyText = "close every chat (the one running this command included), " +
	"resolve any other refusal above by hand, then rerun pfm install --yes from a plain shell"

func TestInstallGateReleasedUpdater(t *testing.T) {
	const updaterLine = "  refuse  updater — this install migrates the host layout, and the pfm update running it " +
		"predates the install journal"
	for _, tc := range []struct {
		name       string
		stage      bool
		linkedHome bool
		marked     bool
		unknown    bool
		pending    bool
		want       string
	}{
		{name: "released updater", stage: true, pending: true, want: updaterLine},
		{name: "journal-aware updater", stage: true, pending: true, marked: true},
		{name: "human with source repo and explicit config", pending: true},
		{name: "nothing pending", stage: true},
		{name: "linked home", stage: true, linkedHome: true, pending: true, want: updaterLine},
		{
			name: "executable unknown", pending: true, unknown: true,
			want: "  refuse  updater — cannot tell which program runs this install: probe failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := layoutFixture(t)
			if tc.linkedHome {
				link := filepath.Join(t.TempDir(), "linked-home")
				if err := os.Symlink(env.Home, link); err != nil {
					t.Fatal(err)
				}
				env.Home = link
			}
			values := map[string]string{"PFM_SOURCE_REPO": env.Clone}
			if tc.marked {
				values[paths.EnvUpdateInstall] = "1"
			}
			env.invocation = &paths.MapEnv{Values: values}
			exe := filepath.Join(paths.PhysicalPath(env.Home), ".local", "bin", "pfm")
			if tc.stage {
				exe = filepath.Join(paths.PhysicalPath(env.Home), ".local", "share", "pfm", "update-abc123", "pfm-a")
			}
			env.executable = func() (string, error) {
				if tc.unknown {
					return "", errors.New("probe failed")
				}
				return exe, nil
			}
			findings := []LayoutFinding{{Row: layoutRowManagedCleanup, Verdict: VerdictCreate}}
			if tc.pending {
				findings = append(findings, LayoutFinding{Row: layoutRowStateDB, Verdict: VerdictMove})
			}
			err := installGate(env, findings)
			if tc.want == "" {
				if err != nil {
					t.Fatalf("gate=%v, want no refusal", err)
				}
				return
			}
			want := "refused before any change:\n" + tc.want + "\n" +
				"cross by hand:\n" +
				"  1. close every chat, the one running this command included\n" +
				"  2. from a plain shell outside tmux, run:\n" +
				"     git -C " + env.Clone + " pull --ff-only\n" +
				"     make -C " + env.Clone + "/pfm host-install\n" +
				"     pfm install --yes"
			if err == nil || err.Error() != want {
				t.Fatalf("gate=%v, want %q", err, want)
			}
		})
	}
}

func TestInstallGateNamesEveryBlockingFinding(t *testing.T) {
	env := layoutFixture(t)
	findings := []LayoutFinding{
		{Row: layoutRowConfig, Path: "/config", Verdict: VerdictRefuse, Detail: "conflict"},
		{Row: layoutRowSessionStore, Path: "/sessions", Verdict: VerdictRefuse, Detail: "live chats: 123"},
		{Row: layoutRowAccountMCP, Path: "/mcp", Verdict: VerdictOK, Err: errors.New("bad JSON")},
	}
	want := "refused before any change:\n" +
		"  refuse  layout config /config — conflict\n" +
		"  refuse  layout session-store /sessions — live chats: 123\n" +
		"  refuse  layout account-mcp /mcp — UNREADABLE bad JSON\n" +
		layoutGateRemedyText
	if err := installGate(env, findings); err == nil || err.Error() != want {
		t.Fatalf("gate=%v, want %q", err, want)
	}
}

func TestInstallGateExemptsAdvisoryAndHeldDatabases(t *testing.T) {
	env := layoutFixture(t)
	findings := []LayoutFinding{
		{Row: layoutRowManagedCleanup, Path: "/managed", Verdict: VerdictRefuse, Err: errors.New("unreadable")},
		{Row: layoutRowStateDB, Path: "/state", Verdict: VerdictRefuse, Detail: "held by pid 77", serviceHeld: true},
		{Row: layoutRowCacheDB, Path: "/cache", Verdict: VerdictRefuse, Detail: "held by pid 88", serviceHeld: true},
	}
	if err := installGate(env, findings); err != nil {
		t.Fatal(err)
	}
	// A holder no pfm service owns is a refusal the gate names, never a skip.
	findings[1] = LayoutFinding{
		Row: layoutRowCacheDB, Path: "/cache", Verdict: VerdictRefuse,
		Detail: "held by pid 88 (pfm) — close it",
	}
	err := installGate(env, findings)
	if err == nil ||
		!strings.Contains(err.Error(), "  refuse  layout cache-db /cache — held by pid 88 (pfm) — close it") ||
		errors.As(err, new(gateUnreadableError)) {
		t.Fatalf("non-service holder gate=%v", err)
	}
}

func TestInstallGateRefusesPendingAndUnreadableJournalsBeforeLayout(t *testing.T) {
	env := layoutFixture(t)
	root := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	layoutWrite(t, filepath.Join(root, "20260101T000000Z", "journal.json"), `[{"result":"pending"}]`)
	layoutWrite(t, filepath.Join(root, "20260102T000000Z", "journal.json"), `{`)
	want := "refused before any change:\n" +
		"  refuse  journal 20260101T000000Z — pending records from a crashed or failed install: " +
		"run pfm install --rollback 20260101T000000Z\n" +
		"  refuse  journal 20260102T000000Z — UNREADABLE unexpected end of JSON input\n" +
		"  refuse  layout config /config — conflict"
	err := installGate(
		env,
		[]LayoutFinding{{Row: layoutRowConfig, Path: "/config", Verdict: VerdictRefuse, Detail: "conflict"}},
	)
	if err == nil ||
		err.Error() != want+"\n"+layoutGateRemedyText {
		t.Fatalf("gate=%v, want %q", err, want)
	}
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err == nil || !strings.Contains(err.Error(), "  refuse  journal 20260101T000000Z") || dir != "" ||
		output.Len() != 0 {
		t.Fatalf("apply journal=%q err=%v output=%q", dir, err, output.String())
	}
	if _, err := os.Stat(filepath.Join(root, "20260101T000000Z", "journal.json")); err != nil {
		t.Fatalf("pending journal changed: %v", err)
	}
}

func TestInstallGateTreatsRestoredAndRolledBackRecordsAsClosed(t *testing.T) {
	env := layoutFixture(t)
	root := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	layoutWrite(
		t,
		filepath.Join(root, "20260101T000000Z", "journal.json"),
		`[{"result":"applied"},{"result":"restored"}]`,
	)
	layoutWrite(t, filepath.Join(root, "20260102T000000Z", "journal.json"), `[{"result":"pending"}]`)
	layoutWrite(t, filepath.Join(root, "20260102T000000Z", layoutRolledBackMarker), "done")
	if err := installGate(env, nil); err != nil {
		t.Fatal(err)
	}
}

func TestInstallGateRefusesUnreadableJournalRoot(t *testing.T) {
	env := layoutFixture(t)
	root := filepath.Join(env.Home, ".local", "state", "pfm", "migrations")
	layoutWrite(t, root, "file")
	err := installGate(env, nil)
	if err == nil || !strings.Contains(err.Error(), "  refuse  journal "+root+" — UNREADABLE ") {
		t.Fatalf("gate=%v", err)
	}
}

func TestInstallGateRefusesBeforeConfigMove(t *testing.T) {
	env := layoutFixture(t)
	if err := os.Remove(env.ConfigPath); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(env.LegacyConfigDir, "pfm.config.json")
	layoutWrite(t, legacy, `{"version":2}`)
	account := env.Config.Accounts[1].ConfigDir
	store := filepath.Join(account, "file-history")
	if err := os.Remove(store); err != nil {
		t.Fatal(err)
	}
	layoutWrite(t, filepath.Join(store, "checkpoint"), "keep")
	layoutWrite(t, filepath.Join(account, "sessions", "123.json"), `{}`)
	if err := os.Mkdir(filepath.Join(env.ProcRoot, "123"), 0o700); err != nil {
		t.Fatal(err)
	}
	want := "refused before any change:\n  refuse  layout session-store " + store + " — live chats: 123\n" +
		layoutGateRemedyText
	var output bytes.Buffer
	dir, err := ApplyLayout(context.Background(), env, nil, true, &output)
	if err == nil || err.Error() != want || dir != "" || output.Len() != 0 {
		t.Fatalf("journal=%q err=%v output=%q", dir, err, output.String())
	}
	if _, err := os.Stat(legacy); err != nil {
		t.Fatalf("legacy config moved: %v", err)
	}
	if _, err := os.Stat(env.ConfigPath); !os.IsNotExist(err) {
		t.Fatalf("config changed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.Home, ".local", "state", "pfm", "migrations")); !os.IsNotExist(err) {
		t.Fatalf("journal dir created: %v", err)
	}
	if got, err := os.ReadFile(
		filepath.Join(store, "checkpoint"),
	); err != nil ||
		strings.TrimSpace(string(got)) != "keep" {
		t.Fatalf("store changed: %q %v", got, err)
	}
}
