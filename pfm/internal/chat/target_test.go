package chat

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/headless"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/resolve"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestReviewSplitRenameRetainsAmbiguityGuard(t *testing.T) {
	root := testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(values.TmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CHAT_INJECT_POLL", "0.01")
	t.Setenv("CHAT_INJECT_ENTER_SETTLE", "0.02")
	t.Setenv("CHAT_INJECT_PROOF_SETTLE", "0.02")

	const renameUI = `import os, sys, tty
tty.setraw(0)
record = sys.argv[1]
buf = bytearray()
os.write(1, "❯ ".encode("utf-8"))
while True:
    ch = os.read(0, 1)
    if not ch:
        break
    if ch == b"\x13":
        continue
    if ch in (b"\r", b"\n"):
        if buf:
            with open(record, "ab") as stream:
                stream.write(bytes(buf) + b"\n")
            os.write(1, b"\r\nFIRED\r\n" + "❯ ".encode("utf-8"))
            buf.clear()
        continue
    buf.extend(ch)
    os.write(1, ch)
`
	script := filepath.Join(root, "rename-ui.py")
	if err := os.WriteFile(script, []byte(renameUI), 0o700); err != nil {
		t.Fatal(err)
	}

	type renameFixture struct {
		socketName string
		session    string
		panes      []string
		records    []string
		ids        []string
	}
	fixtureNumber := 0
	newFixture := func(t *testing.T, paneCount int, tmuxDirs ...string) renameFixture {
		t.Helper()
		fixtureNumber++
		fixture := renameFixture{
			socketName: fmt.Sprintf("cc-rename-review-%d", fixtureNumber),
			session:    fmt.Sprintf("rename-review-%d", fixtureNumber),
		}
		tmuxDir := values.TmuxDir
		if len(tmuxDirs) != 0 {
			tmuxDir = tmuxDirs[0]
		}
		if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
			t.Fatal(err)
		}
		socketPath := filepath.Join(tmuxDir, fixture.socketName)
		for index := 0; index < paneCount; index++ {
			id := fmt.Sprintf("%08d-1111-4111-8111-111111111111", fixtureNumber*10+index)
			record := filepath.Join(root, fmt.Sprintf("rename-%d-%d.log", fixtureNumber, index))
			seedClaudeChat(t, root, id, assistantSaid(fmt.Sprintf("pane %d", index)))
			fixture.ids = append(fixture.ids, id)
			fixture.records = append(fixture.records, record)
			var command *exec.Cmd
			if index == 0 {
				command = exec.Command(
					"tmux", "-S", socketPath, "-f", "/dev/null", "new-session", "-d",
					"-s", fixture.session, "python3", script, record,
				)
			} else {
				command = exec.Command(
					"tmux", "-S", socketPath, "split-window", "-d", "-t", fixture.session,
					"python3", script, record,
				)
			}
			if output, startErr := command.CombinedOutput(); startErr != nil {
				t.Fatalf("start rename pane %d: %v: %s", index, startErr, output)
			}
		}
		t.Cleanup(func() { _ = exec.Command("tmux", "-S", socketPath, "kill-server").Run() })
		output, err := exec.Command(
			"tmux", "-S", socketPath, "list-panes", "-t", fixture.session, "-F", "#{pane_id}",
		).Output()
		if err != nil {
			t.Fatal(err)
		}
		fixture.panes = strings.Fields(string(output))
		if len(fixture.panes) != paneCount {
			t.Fatalf("listed panes = %v, want %d", fixture.panes, paneCount)
		}
		for index, pane := range fixture.panes {
			transcript := filepath.Join(root, "claude", "project", fixture.ids[index]+".jsonl")
			if err := os.WriteFile(
				filepath.Join(values.SIDDir, fixture.socketName+"."+pane),
				[]byte(transcript+"\n"),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
		}
		deadline := time.Now().Add(2 * time.Second)
		for _, pane := range fixture.panes {
			for {
				capture, captureErr := exec.Command(
					"tmux", "-S", socketPath, "capture-pane", "-p", "-t", pane,
				).Output()
				if captureErr == nil && strings.Contains(string(capture), "❯") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("rename pane %s never became ready: %v: %q", pane, captureErr, capture)
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		return fixture
	}
	readRecord := func(t *testing.T, path string) string {
		t.Helper()
		content, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return ""
		}
		if err != nil {
			t.Fatal(err)
		}
		return string(content)
	}

	t.Run("aggregate split refuses before delivery", func(t *testing.T) {
		fixture := newFixture(t, 2)
		code, detail, err := DeliverName(context.Background(), headless.Chat{
			Engine: "cc", Socket: fixture.socketName, Session: fixture.session, Live: true,
		}, "aggregate-name", nil)
		if err != nil || code != resolve.CodeAmbiguous {
			t.Errorf("DeliverName(aggregate) = code %d detail %q err %v, want ambiguity", code, detail, err)
		}
		if detail == "" {
			t.Error("DeliverName(aggregate) returned ambiguity without resolver detail")
		}
		for index, record := range fixture.records {
			if got := readRecord(t, record); got != "" {
				t.Errorf("aggregate rename reached pane %s: %q", fixture.panes[index], got)
			}
		}
	})

	t.Run("exact split pane receives rename alone", func(t *testing.T) {
		fixture := newFixture(t, 2)
		code, detail, err := DeliverName(context.Background(), headless.Chat{
			Engine: "cc", Socket: fixture.socketName, Session: fixture.session,
			Pane: fixture.panes[1], ID: fixture.ids[1], Live: true,
		}, "exact-name", nil)
		if err != nil || code != 0 {
			t.Fatalf("DeliverName(exact pane) = code %d detail %q err %v", code, detail, err)
		}
		if got := readRecord(t, fixture.records[0]); got != "" {
			t.Errorf("exact rename reached sibling pane %s: %q", fixture.panes[0], got)
		}
		if got := readRecord(t, fixture.records[1]); got != "/rename exact-name\n" {
			t.Errorf("exact pane record = %q, want one rename", got)
		}
	})

	t.Run("exact pane uses supplied runtime tmux directory", func(t *testing.T) {
		runtimeTmuxDir := filepath.Join(root, "runtime-tmux")
		fixture := newFixture(t, 1, runtimeTmuxDir)
		runtime := &pfmconfig.Runtime{Paths: values}
		runtime.Paths.TmuxDir = runtimeTmuxDir
		code, detail, err := DeliverName(context.Background(), headless.Chat{
			Engine: "cc", Socket: fixture.socketName, Session: fixture.session,
			Pane: fixture.panes[0], ID: fixture.ids[0], Live: true,
		}, "runtime-name", runtime)
		if err != nil || code != 0 {
			t.Fatalf("DeliverName(runtime pane) = code %d detail %q err %v", code, detail, err)
		}
		if got := readRecord(t, fixture.records[0]); got != "/rename runtime-name\n" {
			t.Errorf("runtime pane record = %q, want one rename", got)
		}
	})

	t.Run("ordinary unique socket still resolves", func(t *testing.T) {
		fixture := newFixture(t, 1)
		code, detail, err := DeliverName(context.Background(), headless.Chat{
			Engine: "cc", Socket: fixture.socketName, Session: fixture.session, Live: true,
		}, "ordinary-name", nil)
		if err != nil || code != 0 {
			t.Fatalf("DeliverName(unique socket) = code %d detail %q err %v", code, detail, err)
		}
		if got := readRecord(t, fixture.records[0]); got != "/rename ordinary-name\n" {
			t.Errorf("ordinary pane record = %q, want one rename", got)
		}
	})
}

// TestMatchPrefersTheLiveSeat covers resolution: a name, an id, a socket, the
// live row winning over its own resume twin, and a genuine collision being
// refused rather than guessed.
func TestMatchPrefersTheLiveSeat(t *testing.T) {
	rows := []compose.Row{
		{Kind: compose.LiveCodex, ID: "019f-live", Name: "worker", Socket: "cx-1-2-3", Path: "/cx/live.jsonl"},
		{Kind: compose.ResumeCodex, ID: "019f-live", Name: "worker", Path: "/cx/live.jsonl"},
		{Kind: compose.ResumeClaude, ID: "b1111111-1111-4111-8111-111111111111", Name: "other"},
		{Kind: compose.ResumeClaude, ID: "c2222222-2222-4222-8222-222222222222", Name: "twin"},
		{Kind: compose.ResumeClaude, ID: "d3333333-3333-4333-8333-333333333333", Name: "twin"},
	}
	for _, testCase := range []struct {
		name    string
		query   string
		wantID  string
		live    bool
		wantErr bool
		missing bool
	}{
		{name: "by name prefers live", query: "worker", wantID: "019f-live", live: true},
		{name: "by socket", query: "cx-1-2-3", wantID: "019f-live", live: true},
		{name: "by id prefix", query: "b1111111", wantID: "b1111111-1111-4111-8111-111111111111"},
		{name: "case folded", query: "OTHER", wantID: "b1111111-1111-4111-8111-111111111111"},
		{name: "ambiguous", query: "twin", wantErr: true},
		{name: "unknown", query: "ghost", missing: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			chat, found, err := Match(rows, testCase.query)
			switch {
			case testCase.wantErr:
				if err == nil {
					t.Fatalf("ambiguous name resolved to %#v", chat)
				}
				if !strings.Contains(err.Error(), "matches 2 chats") {
					t.Fatalf("error = %v", err)
				}
			case testCase.missing:
				if found || err != nil {
					t.Fatalf("unknown name = %#v found=%t err=%v", chat, found, err)
				}
			default:
				if err != nil || !found {
					t.Fatalf("found=%t err=%v", found, err)
				}
				if chat.ID != testCase.wantID || chat.Live != testCase.live {
					t.Fatalf("chat = %#v", chat)
				}
			}
		})
	}
}

// TestMatchAcceptsAFullSocketPathAndTheBareName covers the second resolver's
// half of the same normalisation defect the cosmos graph had. A compose.Row
// carries the BARE socket name tmux is addressed by (-L), while everything the
// fleet RECORDS is a full -S path: inject writes one into the comms ledger,
// spawn writes one for every chat it starts, and `pfm chat resolve` prints
// one. An operator or a script pasting the path it was handed matched nothing,
// and the miss reported as "no chat named …" — an absence, for a chat that was
// right there.
func TestMatchAcceptsAFullSocketPathAndTheBareName(t *testing.T) {
	rows := []compose.Row{
		{Kind: compose.LiveClaude, ID: "p-do-id", Name: "P:DO", Socket: "cc-1787705979-3980493-30867", PaneID: "%0"},
		{Kind: compose.LiveCodex, ID: "other-id", Name: "Other", Socket: "cx-1787757492-3196324-4837", PaneID: "%1"},
	}
	for _, target := range []string{
		"cc-1787705979-3980493-30867",
		"/tmp/tmux-1000/cc-1787705979-3980493-30867",
		"P:DO",
		"p-do-id",
	} {
		chat, found, err := Match(rows, target)
		if err != nil {
			t.Fatalf("Match(%q) error: %v", target, err)
		}
		if !found {
			t.Fatalf("Match(%q) found nothing; the chat is in the roster", target)
		}
		if chat.Name != "P:DO" {
			t.Fatalf("Match(%q) = %q, want P:DO", target, chat.Name)
		}
	}
	if _, found, err := Match(rows, "/tmp/tmux-1000/cc-0-0-0"); found || err != nil {
		t.Fatalf("a path naming no live chat resolved: found=%v err=%v", found, err)
	}
}

// TestFromRowCarriesTheRowAndItsLiveness pins the one shape every verb
// operates on: a booting seat already has a server, so it is live; a
// resumable row has none.
func TestFromRowCarriesTheRowAndItsLiveness(t *testing.T) {
	row := compose.Row{
		Kind: compose.Booting, ID: "id-1", Name: "seat", Path: "/t/id-1.jsonl", CWD: "/work",
		Socket: "cc-1-2-3", SessionName: "cc-1-2-3", PaneID: "%4",
	}
	want := headless.Chat{
		Name: "seat", ID: "id-1", Engine: compose.EngineForKind(compose.Booting),
		Path: "/t/id-1.jsonl", CWD: "/work", Socket: "cc-1-2-3", Session: "cc-1-2-3",
		Pane: "%4", Live: true,
	}
	if got := FromRow(row); got != want {
		t.Fatalf("FromRow() = %#v, want %#v", got, want)
	}
	for _, kind := range []compose.Kind{compose.ResumeClaude, compose.ResumeCodex} {
		if kind.IsAddressable() {
			t.Fatalf("IsAddressable(%v) = true for a resumable row", kind)
		}
	}
}

// TestTargetErrorKeepsAScanFailureDistinctFromAbsence pins the root law at
// the verb layer: a scan that could not look is not "no such chat".
func TestTargetErrorKeepsAScanFailureDistinctFromAbsence(t *testing.T) {
	failure := &TargetError{Name: "x", Err: errors.New("open index: disk I/O error")}
	if errors.Is(failure, ErrUnknownChat) || failure.Error() != "open index: disk I/O error" {
		t.Fatalf("scan failure rendered as %q (unknown=%v)", failure.Error(), errors.Is(failure, ErrUnknownChat))
	}
}

func TestResolvedSelfIsRequestLocalAndExact(t *testing.T) {
	testjail.Fleet(t)
	want := headless.Chat{
		Name: "second", ID: "split-second", Engine: "cc", Path: "/jail/second.jsonl",
		CWD: "/work/second", Socket: "cc-shared", Session: "renamed", Pane: "%8", Live: true,
	}
	ctx := WithResolvedSelf(context.Background(), want)
	for _, name := range []string{"self", "me", " self ", `"me"`} {
		got, found, err := Resolve(ctx, name, io.Discard, nil)
		if err != nil || !found || got != want {
			t.Fatalf("Resolve(%q) = %+v found=%t err=%v, want exact scoped chat %+v", name, got, found, err, want)
		}
	}
	if _, found, err := Resolve(ctx, "somebody-else", io.Discard, nil); err != nil || found {
		t.Fatalf("explicit target used scoped self: found=%t err=%v", found, err)
	}
}

func TestScopedRawPaneUsesOnlyTheCallerSocket(t *testing.T) {
	testjail.Fleet(t)
	values, err := paths.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	daemonSocket := filepath.Join(values.TmuxDir, "cc-daemon")
	t.Setenv("CHAT_INJECT_SOCKET", daemonSocket)
	t.Setenv("TMUX", filepath.Join(values.TmuxDir, "cc-other")+",123,0")

	t.Run("scoped socket wins without copying caller identity", func(t *testing.T) {
		ctx := WithResolvedSelf(context.Background(), headless.Chat{
			Socket: "cx-caller", Session: "caller-session", Pane: "%1", ID: "thread-caller",
		})
		for _, target := range []string{"%2", " %2 ", `"%2"`} {
			got, found, err := Resolve(ctx, target, io.Discard, nil)
			if err != nil || !found || got.Socket != "cx-caller" || got.Pane != "%2" {
				t.Fatalf("Resolve(scoped %q) = %+v found=%t err=%v", target, got, found, err)
			}
			if got.ID != "" || got.Session != "" {
				t.Fatalf("scoped raw pane %q inherited caller identity: %+v", target, got)
			}
		}
	})

	t.Run("missing scoped socket refuses ambient fallback", func(t *testing.T) {
		ctx := WithResolvedSelf(context.Background(), headless.Chat{
			Session: "caller-session", Pane: "%1", ID: "thread-caller",
		})
		for _, target := range []string{"%2", " %2 ", `"%2"`} {
			got, found, err := Resolve(ctx, target, io.Discard, nil)
			if err != nil || found || got != (headless.Chat{}) {
				t.Fatalf("Resolve(scoped %q without socket) = %+v found=%t err=%v", target, got, found, err)
			}
		}
	})

	t.Run("metadata-free raw pane retains ambient resolution", func(t *testing.T) {
		got, found, err := Resolve(context.Background(), "%3", io.Discard, nil)
		if err != nil || !found || got.Socket != filepath.Base(daemonSocket) || got.Pane != "%3" {
			t.Fatalf("Resolve(ambient %%3) = %+v found=%t err=%v", got, found, err)
		}
	})
}

func TestAmbientSelfEnrichmentRestoresTheRosterPaneAndSession(t *testing.T) {
	seat := headless.Chat{
		Name: "Codex chat", ID: "c6666666-6666-4666-8666-666666666666", Engine: "cx",
		Socket: "cx-shared", Session: "renamed-session", Pane: "%0", Live: true,
	}
	resolved := headless.Chat{
		ID: seat.ID, Engine: "cx", Socket: seat.Socket,
		Session: seat.Socket, Pane: seat.Socket, Live: true,
	}
	resolved = enrichResolvedSelf(resolved, seat)
	if resolved != seat {
		t.Fatalf("ambient self enrichment = %+v, want exact roster seat %+v", resolved, seat)
	}
}

func TestAmbientSplitSelfEnrichmentKeepsTheLivePane(t *testing.T) {
	resolved := headless.Chat{
		ID: "b2222222-2222-4222-8222-222222222222", Engine: "cc",
		Socket: "cc-shared", Session: "renamed", Pane: "%1", Live: true,
	}
	indexed := headless.Chat{
		Name: "second", ID: resolved.ID, Engine: "cc", Path: "/jail/second.jsonl",
		CWD: "/work/second",
	}
	got := enrichResolvedSelf(resolved, indexed)
	if got.ID != indexed.ID || got.Path != indexed.Path || got.CWD != indexed.CWD ||
		got.Socket != resolved.Socket || got.Session != resolved.Session || got.Pane != resolved.Pane || !got.Live {
		t.Fatalf("split self enrichment = %+v, want metadata from %+v with address from %+v", got, indexed, resolved)
	}
}

func TestAmbientClaudeSplitSelfRetainsItsExactPane(t *testing.T) {
	root := testjail.Fleet(t)
	const (
		first  = "a1111111-1111-4111-8111-111111111111"
		second = "b2222222-2222-4222-8222-222222222222"
	)
	seedClaudeChat(t, root, first, assistantSaid("first"))
	seedClaudeChat(t, root, second, assistantSaid("second"))
	tmuxDir := filepath.Join(root, "tmux-"+fmt.Sprint(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvTmuxDir, tmuxDir)
	t.Setenv("TMUX_TMPDIR", root)
	socket := filepath.Join(tmuxDir, "cc-new-review-split")
	runTmux := func(args ...string) {
		t.Helper()
		command := exec.Command("tmux", append([]string{"-S", socket}, args...)...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("tmux %v: %v: %s", args, err, output)
		}
	}
	runTmux("-f", "/dev/null", "new-session", "-d", "-s", "cc-new-review-split", "sleep 120")
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	runTmux("split-window", "-d", "-t", "cc-new-review-split", "sleep 120")
	for pane, id := range map[string]string{"%0": first, "%1": second} {
		transcript := filepath.Join(root, "claude", "project", id+".jsonl")
		if err := os.WriteFile(
			filepath.Join(root, "sid", "cc-new-review-split."+pane),
			[]byte(transcript+"\n"),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%1")
	t.Setenv(resolve.ClaudeSessionEnv, second)
	t.Setenv(resolve.CodexThreadEnv, "")
	self, err := Target(context.Background(), "self", nil)
	if err != nil {
		t.Fatal(err)
	}
	if self.ID != second || self.Socket != "cc-new-review-split" || self.Pane != "%1" || !self.Live {
		t.Fatalf("split self = %+v, want second transcript on exact pane %%1", self)
	}
}

func TestAmbientSelfWithoutExportedIDEnrichesFromItsExactSocket(t *testing.T) {
	root := testjail.Fleet(t)
	t.Setenv(resolve.ClaudeSessionEnv, "")
	t.Setenv(resolve.CodexThreadEnv, "")
	const id = "b1111111-1111-4111-8111-111111111111"
	seedClaudeChat(t, root, id, assistantSaid("answer"))
	tmuxDir := filepath.Join(root, "tmux-"+fmt.Sprint(os.Getuid()))
	if err := os.MkdirAll(tmuxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvTmuxDir, tmuxDir)
	t.Setenv("TMUX_TMPDIR", root)
	socket := filepath.Join(tmuxDir, "cc-new-review")
	command := exec.Command(
		"tmux", "-S", socket, "-f", "/dev/null",
		"new-session", "-d", "-s", "cc-new-review", "sleep 120",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start tmux: %v: %s", err, output)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	transcriptPath := filepath.Join(root, "claude", "project", id+".jsonl")
	if err := os.WriteFile(
		filepath.Join(root, "sid", "cc-new-review.%0"),
		[]byte(transcriptPath+"\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", socket+",1,0")
	t.Setenv("TMUX_PANE", "%0")
	self, err := Target(context.Background(), "self", nil)
	if err != nil || self.ID != id || self.Path != transcriptPath || self.Pane != "%0" {
		t.Fatalf("Target(self) = %+v err=%v, want enriched exact pane and transcript", self, err)
	}
}

// TestSeatIdentityIsOnlyTheLastRung pins the refusals that keep an INHERITED
// CODEX_THREAD_ID from naming anyone: no thread id, a Claude session of the
// process's own, or a thread the fleet binds to no live socket — each is "no
// seat identity", never a handle nobody can reply to.
func TestSeatIdentityIsOnlyTheLastRung(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	t.Setenv(resolve.ClaudeSessionEnv, "")
	t.Setenv(resolve.CodexThreadEnv, "")
	if identity, found := SeatIdentity(ctx, nil); found {
		t.Fatalf("SeatIdentity() without a thread = %+v", identity)
	}
	t.Setenv(resolve.CodexThreadEnv, "019f-inherited")
	t.Setenv(resolve.ClaudeSessionEnv, "a-claude-session")
	if identity, found := SeatIdentity(ctx, nil); found {
		t.Fatalf("SeatIdentity() under a Claude session = %+v", identity)
	}
	t.Setenv(resolve.ClaudeSessionEnv, "")
	// SeatIdentity folds a scan error into "no seat"; prove the scan ran, so
	// this case is "no live socket hosts the thread", not "could not look".
	if _, err := Rows(ctx, io.Discard, nil); err != nil {
		t.Fatalf("fleet scan failed, so the next case would pass vacuously: %v", err)
	}
	if identity, found := SeatIdentity(ctx, nil); found {
		t.Fatalf("SeatIdentity() for a thread no live socket hosts = %+v", identity)
	}
}

// TestTargetReportsAScanThatCouldNotLook drives the law through a real scan:
// with the fleet's index database unopenable, Target returns a *TargetError
// carrying the failure, not ErrUnknownChat — and a healthy scan that finds
// nothing is the ErrUnknownChat answer.
func TestTargetReportsAScanThatCouldNotLook(t *testing.T) {
	testjail.Fleet(t)
	ctx := context.Background()
	if _, err := Target(ctx, "ghost", nil); !errors.Is(err, ErrUnknownChat) {
		t.Fatalf("Target(ghost) on a healthy fleet = %v, want ErrUnknownChat", err)
	}
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(paths.EnvDB, filepath.Join(blocker, "index.db"))
	_, err := Target(ctx, "ghost", nil)
	var failure *TargetError
	if !errors.As(err, &failure) || errors.Is(err, ErrUnknownChat) || failure.Name != "ghost" {
		t.Fatalf("Target(ghost) with an unopenable index = %v, want a *TargetError that is not ErrUnknownChat", err)
	}
}
