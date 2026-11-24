//go:build jail

package resolve

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/testwait"
)

type resolveJail struct {
	root       string
	tmuxDir    string
	sidDir     string
	home       string
	sockets    []string
	scriptNext int
}

func newResolveJail(t testing.TB) *resolveJail {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux binary is not installed")
	}
	root, err := os.MkdirTemp("/tmp", "ccr")
	if err != nil {
		t.Fatal(err)
	}
	jail := &resolveJail{
		root:    root,
		tmuxDir: filepath.Join(root, "tmux-"+strconv.Itoa(os.Getuid())),
		sidDir:  filepath.Join(root, "sid"),
		home:    filepath.Join(root, "home"),
	}
	for _, directory := range []string{
		jail.tmuxDir,
		jail.sidDir,
		jail.home,
		filepath.Join(root, "claude"),
		filepath.Join(root, "codex"),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", jail.home)
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", jail.root)
	t.Setenv("PFM_HOME", jail.home)
	t.Setenv("PFM_CACHE_DB", filepath.Join(root, "pfm-cache.db"))
	t.Setenv("PFM_SID_DIR", jail.sidDir)
	t.Setenv("PFM_CLAUDE_ROOTS", filepath.Join(root, "claude"))
	t.Setenv("PFM_CODEX_ROOT", filepath.Join(root, "codex"))
	t.Setenv("PFM_TMUX_DIR", jail.tmuxDir)
	t.Cleanup(func() {
		for _, socket := range jail.sockets {
			_ = jail.command("-L", socket, "kill-server").Run()
		}
		if err := os.RemoveAll(jail.root); err != nil {
			t.Errorf("remove resolve jail: %v", err)
		}
	})
	return jail
}

func (jail *resolveJail) command(arguments ...string) *exec.Cmd {
	command := exec.Command("tmux", arguments...)
	command.Env = append(
		os.Environ(),
		"HOME="+jail.home,
		"TMUX=",
		"TMUX_TMPDIR="+jail.root,
	)
	return command
}

func (jail *resolveJail) script(t testing.TB, label string) string {
	t.Helper()
	jail.scriptNext++
	path := filepath.Join(
		jail.root,
		fmt.Sprintf("p%03d.sh", jail.scriptNext),
	)
	content := "#!/bin/sh\nprintf '%s\\n' " +
		shellSingleQuote("🥇 │ 🔖 "+label+" │ status") +
		"\n" + testwait.Hold + "\n"
	if err := testjail.WriteExecutable(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func shellSingleQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func (jail *resolveJail) start(
	t testing.TB,
	socket, session, window, label string,
) {
	t.Helper()
	command := jail.command(
		"-f",
		"/dev/null",
		"-L",
		socket,
		"new-session",
		"-d",
		"-s",
		session,
		"-n",
		window,
		jail.script(t, label),
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start %s: %v: %s", socket, err, output)
	}
	jail.sockets = append(jail.sockets, socket)
	jail.awaitLabel(t, socket, session, label)
}

// awaitLabel waits until the pane target of socket has painted label.
func (jail *resolveJail) awaitLabel(t testing.TB, socket, target, label string) {
	t.Helper()
	testwait.Until(t, fmt.Sprintf("%s to paint the label %q", socket, label), func() (bool, string) {
		output, err := jail.command("-L", socket, "capture-pane", "-p", "-t", target).CombinedOutput()
		if err == nil && strings.Contains(string(output), label) {
			return true, ""
		}
		// Two different states reach here and demand different answers: a
		// capture-pane that kept erroring means we never managed to look; one
		// that kept succeeding on a blank pane means we looked and the label
		// was not there yet.
		if err != nil {
			return false, fmt.Sprintf("capture-pane kept failing; last error: %v: %q", err, output)
		}
		return false, fmt.Sprintf(
			"capture-pane succeeded but the pane never painted the label; last pane content (%d bytes): %q",
			len(output), output,
		)
	})
}

func (jail *resolveJail) split(
	t testing.TB,
	socket, session, label string,
) {
	t.Helper()
	command := jail.command(
		"-L",
		socket,
		"split-window",
		"-d",
		"-P",
		"-F",
		"#{pane_id}",
		"-t",
		session,
		jail.script(t, label),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("split %s: %v: %s", socket, err, output)
	}
	jail.awaitLabel(t, socket, strings.TrimSpace(string(output)), label)
	if output, err := jail.command(
		"-L",
		socket,
		"select-layout",
		"-t",
		session,
		"tiled",
	).CombinedOutput(); err != nil {
		t.Fatalf("tile %s: %v: %s", socket, err, output)
	}
}

func TestJailedTmuxResolveContract(t *testing.T) {
	testjail.TmuxServer(t)
	jail := newResolveJail(t)
	jail.start(t, "cc-100-1-1", "alpha-session", "alpha", "Alpha Label")
	jail.start(
		t,
		"cx-200-1-1",
		"cx-session",
		"abcdefghijklmnopqrstuvwx",
		"Codex Label",
	)
	jail.start(t, "cc-300-1-1", "other-session", "other", "Other Label")

	resolver, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	label, err := resolver.Resolve(ctx, Label, "alpha label")
	if err != nil {
		t.Fatal(err)
	}
	if label.Code != 0 {
		t.Logf("resolve diagnostics: %s", resolveDiagnostics(t, resolver))
	}
	assertTarget(t, label, "cc-100-1-1", "%")

	session, err := resolver.Resolve(ctx, Session, "alpha-session")
	if err != nil {
		t.Fatal(err)
	}
	assertTarget(t, session, "cc-100-1-1", "%")

	cx, err := resolver.Resolve(
		ctx,
		CxWindow,
		"abcdefghijklmnopqrstuvwxyz-more",
	)
	if err != nil {
		t.Fatal(err)
	}
	assertTarget(t, cx, "cx-200-1-1", "%")

	miss, err := resolver.Resolve(ctx, Label, "Alpha")
	if err != nil || miss.Code != 1 || miss.Stdout != "" || miss.Stderr != "" {
		t.Fatalf("miss = %#v, err=%v", miss, err)
	}

	jail.start(t, "cc-400-1-1", "ambiguous", "ambiguous", "Alpha Label")
	ambiguous, err := resolver.Resolve(ctx, Label, "Alpha Label")
	if err != nil || ambiguous.Code != 2 ||
		ambiguous.Stdout != "" ||
		!strings.Contains(ambiguous.Stderr, "ambiguous") {
		t.Fatalf("ambiguous = %#v, err=%v", ambiguous, err)
	}
}

func TestJailedTmuxNewestServerTieBreak(t *testing.T) {
	testjail.TmuxServer(t)
	jail := newResolveJail(t)
	const uuid = "77777777-7777-4777-8777-777777777777"
	jail.start(t, "cc-100-1-1", "old", "old", "One Chat")
	jail.start(t, "cc-900-1-1", "new", "new", "One Chat")
	for _, socket := range []string{"cc-100-1-1", "cc-900-1-1"} {
		if err := os.WriteFile(
			filepath.Join(jail.sidDir, socket),
			[]byte(filepath.Join(jail.root, uuid+".jsonl")),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	resolver, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := resolver.Resolve(context.Background(), Label, "one chat")
	if err != nil || outcome.Code != 0 {
		t.Logf("resolve diagnostics: %s", resolveDiagnostics(t, resolver))
		t.Fatalf("tie outcome = %#v, err=%v", outcome, err)
	}
	if !strings.Contains(outcome.Stdout, "cc-900-1-1\t%") ||
		!strings.Contains(outcome.Stderr, "newest") ||
		!strings.Contains(outcome.Stderr, uuid) {
		t.Fatalf("tie outcome = %#v", outcome)
	}
}

func resolveDiagnostics(t *testing.T, resolver *Resolver) string {
	t.Helper()
	panes, err := resolver.allPanes(context.Background())
	if err != nil {
		return err.Error()
	}
	var output strings.Builder
	for _, pane := range panes {
		capture, captureErr := resolver.tmux.CapturePane(
			context.Background(),
			pane.SocketPath,
			pane.PaneID,
		)
		fmt.Fprintf(
			&output,
			"\n%+v capture=%q err=%v",
			pane,
			capture,
			captureErr,
		)
	}
	return output.String()
}

const (
	resolveStressSockets = 60
	resolveStressPanes   = 200
)

// resolveStressQueries are the three lookups the stress fleet answers: the
// last socket's label, its session, and the long Codex window name.
var resolveStressQueries = []struct {
	kind Kind
	name string
}{
	{kind: Label, name: "Stress Target"},
	{kind: Session, name: "stress-59"},
	{kind: CxWindow, name: "abcdefghijklmnopqrstuvwxyz-long"},
}

// startResolveStressFleet starts 60 tmux servers holding 200 labelled panes
// and returns a resolver over them.
func startResolveStressFleet(t testing.TB) *Resolver {
	t.Helper()
	jail := newResolveJail(t)
	const socketCount = resolveStressSockets
	const paneCount = resolveStressPanes
	createdPanes := 0
	for index := 0; index < socketCount; index++ {
		prefix := "cc"
		if index%3 == 0 {
			prefix = "cx"
		}
		socket := fmt.Sprintf("%s-%d-1-1", prefix, 1000+index)
		session := fmt.Sprintf("stress-%02d", index)
		window := fmt.Sprintf("window-%02d", index)
		label := fmt.Sprintf("Fleet %03d", createdPanes)
		if index == socketCount-1 {
			label = "Stress Target"
		}
		if index == 57 {
			window = "abcdefghijklmnopqrstuvwx"
		}
		jail.start(t, socket, session, window, label)
		createdPanes++
		wanted := 3
		if index < 20 {
			wanted = 4
		}
		switch index {
		case 57, 59:
			wanted = 1
		case 58:
			wanted = 7
		}
		for pane := 1; pane < wanted; pane++ {
			jail.split(
				t,
				socket,
				session,
				fmt.Sprintf("Fleet %03d", createdPanes),
			)
			createdPanes++
		}
	}
	if createdPanes != paneCount {
		t.Fatalf("created %d panes, want %d", createdPanes, paneCount)
	}
	resolver, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

// BenchmarkResolveSixtySocketsTwoHundredPanes measures what the stress test no
// longer judges: each of its three lookups over the 60-socket, 200-pane fleet.
func BenchmarkResolveSixtySocketsTwoHundredPanes(b *testing.B) {
	testjail.TmuxServer(b)
	resolver := startResolveStressFleet(b)
	ctx := context.Background()
	for _, query := range resolveStressQueries {
		b.Run(string(query.kind), func(b *testing.B) {
			for b.Loop() {
				if outcome, err := resolver.Resolve(ctx, query.kind, query.name); err != nil || outcome.Code != 0 {
					b.Fatalf("%s stress outcome=%#v err=%v", query.kind, outcome, err)
				}
			}
		})
	}
}

func TestStressResolveSixtySocketsTwoHundredPanes(t *testing.T) {
	testjail.TmuxServer(t)
	resolver := startResolveStressFleet(t)
	ctx := context.Background()
	for _, query := range resolveStressQueries {
		outcome, err := resolver.Resolve(ctx, query.kind, query.name)
		if err != nil || outcome.Code != 0 {
			t.Fatalf("%s stress outcome=%#v err=%v", query.kind, outcome, err)
		}
		t.Logf(
			"STRESS resolve kind=%s sockets=%d panes=%d",
			query.kind,
			resolveStressSockets,
			resolveStressPanes,
		)
	}

	for _, query := range []string{
		"",
		strings.Repeat("x", 500),
		"🧭🚀",
		"Stress",
	} {
		outcome, err := resolver.Resolve(ctx, Label, query)
		if err != nil || outcome.Code != 1 || outcome.Stdout != "" {
			t.Fatalf("adversarial %q = %#v, err=%v", query, outcome, err)
		}
	}
	t.Log("STRESS adversarial=4 false_matches=0")
}

func assertTarget(
	t *testing.T,
	outcome Outcome,
	socket, targetPrefix string,
) {
	t.Helper()
	if outcome.Code != 0 || outcome.Stderr != "" {
		t.Fatalf("outcome = %#v", outcome)
	}
	fields := strings.Split(strings.TrimSuffix(outcome.Stdout, "\n"), "\t")
	if len(fields) != 2 ||
		filepath.Base(fields[0]) != socket ||
		!strings.HasPrefix(fields[1], targetPrefix) {
		t.Fatalf("target = %#v", outcome)
	}
}

// TestPaneOwnersSurvivesAStrippedEnvironment pins the delimiter choice in the
// pane format.
//
// tmux hands a control character in a format string back as "_" unless the
// CALLER's environment either carries a UTF-8 locale or merely DEFINES $TMUX —
// an empty $TMUX is enough. Both hold for a normal fleet call and neither holds
// in the tool shell a chat engine spawns from a scrubbed environment, which is
// the one place ancestry recovery has to work: there a tab made every row
// unsplittable, recovery answered "not in tmux", and codex-origin messages went
// out UNSIGNED.
//
// The second half runs the tab against an environment stripped of both, so the
// trap is proven live and a rewrite back to a tab cannot pass quietly.
func TestPaneOwnersSurvivesAStrippedEnvironment(t *testing.T) {
	testjail.TmuxServer(t)
	jail := newResolveJail(t)
	jail.start(t, "cc-500-1-1", "locale-session", "locale", "Locale Label")

	// Cleared only AFTER the session is up: the readiness probe reads an emoji
	// label back off the pane, which a stripped client would mangle too.
	t.Setenv("LANG", "")
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")

	socketPath := filepath.Join(jail.tmuxDir, "cc-500-1-1")
	owners, err := CommandPaneOwners{TmuxDir: jail.tmuxDir}.PaneOwners(
		context.Background(),
		socketPath,
	)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, owner := range owners {
		rows = append(rows, fmt.Sprintf("%s pid>0=%t", owner.PaneID, owner.PanePID > 0))
	}
	if got, want := strings.Join(rows, "\n"), "%0 pid>0=true"; got != want {
		t.Fatalf("PaneOwners() = %q, want %q", got, want)
	}

	stripped := exec.Command(
		"tmux",
		"-S",
		socketPath,
		"list-panes",
		"-a",
		"-F",
		"#{pane_pid}\t#{pane_id}",
	)
	stripped.Env = []string{
		"HOME=" + jail.home,
		"PATH=" + os.Getenv("PATH"),
	}
	tabbed, err := stripped.Output()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := regexp.MustCompile(`(?m)^\d+`).
		ReplaceAllString(string(tabbed), "{pid}"),
		"{pid}_%0\n"; got != want {
		t.Fatalf("stripped list-panes = %q, want %q", got, want)
	}
}

// TestJailedColonLabelResolvesToAPaneNeverATmuxTarget proves, against a real
// tmux server, that the address the reply footer now hands out is safe.
//
// The footer advertises the sender's 🔖 LABEL, and the operator's labels
// carry colons — P:DO, W:ORCHESTRATOR, COSMOSTEST:A. ':' is tmux's own
// session:window separator, so a label is never a legal tmux target, and the
// second half of this test shows that outright: tmux resolves "<session>:1"
// and fails on "<session>:P:DO". Nothing in the resolve path may interpolate
// a label into a target, and the first half pins the reason it does not have
// to — label resolution answers with a %pane id, which is unambiguous
// whatever the label contained.
func TestJailedColonLabelResolvesToAPaneNeverATmuxTarget(t *testing.T) {
	testjail.TmuxServer(t)
	jail := newResolveJail(t)
	jail.start(t, "cc-500-1-1", "colon-session", "colon", "P:DO")
	// A second window, which tmux makes CURRENT. The labelled chat is now
	// deliberately not the session's current window, which is the only
	// arrangement in which the colon-target fallback below can be seen for
	// what it is.
	if output, err := jail.command(
		"-L", "cc-500-1-1", "new-window", "-n", "other", testwait.Hold,
	).CombinedOutput(); err != nil {
		t.Fatalf("second window: %v: %s", err, output)
	}
	testwait.Until(t, "the second window to be the session's current one", func() (bool, string) {
		output, err := jail.command(
			"-L", "cc-500-1-1", "display-message", "-p", "-t", "colon-session", "#{window_name}",
		).CombinedOutput()
		return strings.TrimSpace(string(output)) == "other", fmt.Sprintf("current window %q, %v", output, err)
	})

	resolver, err := New(nil)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := resolver.Resolve(context.Background(), Label, "P:DO")
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Code != 0 {
		t.Fatalf("colon label did not resolve: %#v\n%s", outcome, resolveDiagnostics(t, resolver))
	}
	assertTarget(t, outcome, "cc-500-1-1", "%")

	fields := strings.Split(strings.TrimSuffix(outcome.Stdout, "\n"), "\t")
	if strings.Contains(fields[1], ":") {
		t.Fatalf("resolved target %q carries a colon — it is not a bare pane id", fields[1])
	}

	// The empirical half, and it came out WORSE than "a colon target
	// fails": tmux does not reject "<session>:<label>" at all. It splits at
	// the first ':', finds no window by that name, and silently falls back
	// to the session's CURRENT window — the same pane it returns for
	// outright nonsense. So a label interpolated into a tmux target does
	// not error where a human would see it; it delivers to whichever chat
	// window happens to be current. That is why the label never becomes a
	// target and the resolver answers with a %pane id instead.
	//
	// The assertion is written to survive a tmux that behaves differently:
	// either both spellings fail (the target is illegal), or both succeed
	// identically (the text after ':' was ignored). What must never happen
	// is the label selecting something of its own, because that would mean
	// this whole premise needs re-verifying.
	labelTarget, labelErr := jail.command(
		"-L", "cc-500-1-1", "display-message", "-p",
		"-t", "colon-session:P:DO", "#{pane_id}",
	).Output()
	nonsenseTarget, nonsenseErr := jail.command(
		"-L", "cc-500-1-1", "display-message", "-p",
		"-t", "colon-session:__no_such_window__", "#{pane_id}",
	).Output()
	if labelErr == nil && nonsenseErr == nil &&
		strings.TrimSpace(string(labelTarget)) != strings.TrimSpace(string(nonsenseTarget)) {
		t.Fatalf(
			"tmux selected pane %q for the label and %q for nonsense — the label is being honoured as a target, which contradicts this test's premise; re-verify before trusting it",
			strings.TrimSpace(string(labelTarget)),
			strings.TrimSpace(string(nonsenseTarget)),
		)
	}
	if labelErr == nil && strings.TrimSpace(string(labelTarget)) == fields[1] {
		t.Fatalf(
			"the colon target landed on the labelled pane %q, so the fallback is indistinguishable from a real match here and this proves nothing; the second window was supposed to be current",
			fields[1],
		)
	}
	if labelErr == nil {
		t.Logf(
			"tmux answered %q for target colon-session:P:DO while the labelled chat is pane %q — a label interpolated into a tmux target silently addresses the WRONG chat",
			strings.TrimSpace(string(labelTarget)),
			fields[1],
		)
	}
}
