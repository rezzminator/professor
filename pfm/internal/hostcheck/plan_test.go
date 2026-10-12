package hostcheck

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func planPaths(rows []Row) []string {
	paths := make([]string, len(rows))
	for i, row := range rows {
		paths[i] = row.Path
	}
	return paths
}

// TestPlanOrdersFixesByDependency pins the ordering rule: phase first (config,
// databases, store and identity, wiring, cleanup, unknown classes, the fixes
// pfm install applies itself), Block before Warn inside a phase, then the
// class's place in fixOrder, then an unknown class by name. Input order never
// changes the result.
func TestPlanOrdersFixesByDependency(t *testing.T) {
	rows := []Row{
		{Warn, "beside-backup", "/h/x.bak", "a backup beside the file", "rm -r /h/x.bak"},
		{Warn, checkRetiredStoreEntry, "/h/store/e", "retired store copy", "run pfm install"},
		{Block, checkPFMMCP, "/h/.cc/1/.claude.json", "carries pfm mcpServers.p", "remove mcpServers.p"},
		{Warn, "home-state-file", "/h/.claude.json", "state file", "check, then rm"},
		{Block, "store-identity", "/h/store/.credentials.json", "identity in the store", "mv"},
		{Block, "account-is-store", "/h/acct", "resolves to the store", "replace the link"},
		{Warn, "zz-new-check", "/h/z", "new warn", "fix z"},
		{Block, "aa-new-check", "/h/a", "new block", "fix a"},
		{Warn, "aa-new-check", "/h/a2", "new warn", "fix a2"},
		{Block, "legacy-state-db", "/h/state.db", "legacy db", "mv db"},
		{Block, "pre-split-config", "/h/config.json", "pre-split name", "mv cfg"},
		{Warn, "stale-state-tmp", "/h/t", "stale temp", "rm /h/t"},
		{Block, "staged-shim", "/h/.zshrc", "retired shim", "delete the line"},
	}
	want := []string{
		"/h/config.json",
		"/h/state.db",
		"/h/acct",
		"/h/store/.credentials.json",
		"/h/.claude.json",
		"/h/.cc/1/.claude.json",
		"/h/.zshrc",
		"/h/t",
		"/h/x.bak",
		"/h/a",
		"/h/a2",
		"/h/z",
		"/h/store/e",
	}
	plan := NewPlan(len(Detectors()), rows)
	if got := planPaths(plan.Steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("steps=%q\nwant=%q", got, want)
	}
	reversed := slices.Clone(rows)
	slices.Reverse(reversed)
	if got := planPaths(NewPlan(len(Detectors()), reversed).Steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("reversed input steps=%q\nwant=%q", got, want)
	}
	if len(plan.Failed) != 0 || plan.Blocking() != 7 {
		t.Fatalf("failed=%v blocking=%d", plan.Failed, plan.Blocking())
	}
}

// TestPlanUnknownClassOrdersBeforeInstallRun pins where a class missing from
// fixOrder lands: after every known fix a person applies by hand, before the
// fixes `pfm install` performs (install refuses while any BLOCK stands).
func TestPlanUnknownClassOrdersBeforeInstallRun(t *testing.T) {
	plan := NewPlan(1, []Row{
		{Warn, checkRetiredStoreEntry, "/h/r", "retired", "run pfm install"},
		{Block, "brand-new-check", "/h/n", "new", "fix n"},
		{Warn, "beside-backup", "/h/b", "backup", "rm /h/b"},
	})
	if got, want := planPaths(plan.Steps), []string{"/h/b", "/h/n", "/h/r"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("steps=%q want=%q", got, want)
	}
}

func TestPlanRender(t *testing.T) {
	failed := unreadable("pfm-settings", "/h/s.json", errors.New("permission denied"))
	step := Row{
		Block,
		"legacy-config",
		"/h/old.json",
		"legacy pfm config outside the clone",
		"mv /h/old.json /h/new.json",
	}
	for _, test := range []struct {
		name string
		plan Plan
		want string
	}{
		{"no check ran", NewPlan(0, nil), "install plan: FAILED — no host check ran; the plan is unknown\n"},
		{"clean", NewPlan(3, nil), "install plan: 3 host checks ran, 0 failed — no fixes\n"},
		{
			"fixes",
			NewPlan(3, []Row{step}),
			"install plan: 3 host checks ran, 0 failed — 1 fixes, apply them in this order\n" +
				"  1. BLOCK legacy-config /h/old.json — legacy pfm config outside the clone\n" +
				"      fix: mv /h/old.json /h/new.json\n" +
				"  then: pfm install --yes, then pfm doctor\n",
		},
		{
			"failed only",
			NewPlan(3, []Row{failed}),
			"install plan: 3 host checks ran, 1 failed — the plan is incomplete until every failed check can look\n" +
				"  FAILED pfm-settings /h/s.json — UNREADABLE /h/s.json: permission denied\n" +
				"      fix: make /h/s.json readable to you, then rerun\n" +
				"  then: rerun pfm install --check --plan\n",
		},
		{
			"failed and fixes",
			NewPlan(3, []Row{step, failed}),
			"install plan: 3 host checks ran, 1 failed — the plan is incomplete until every failed check can look\n" +
				"  FAILED pfm-settings /h/s.json — UNREADABLE /h/s.json: permission denied\n" +
				"      fix: make /h/s.json readable to you, then rerun\n" +
				"  1. BLOCK legacy-config /h/old.json — legacy pfm config outside the clone\n" +
				"      fix: mv /h/old.json /h/new.json\n" +
				"  then: rerun pfm install --check --plan\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.plan.Render(); got != test.want {
				t.Fatalf("render=%q\nwant=%q", got, test.want)
			}
		})
	}
}

// TestPlanForCountsEveryDetector proves the "no fixes" verdict rests on
// detectors that ran: Ran counts them, and a detector that errors lands in
// Failed, never in silence.
func TestPlanForCountsEveryDetector(t *testing.T) {
	plan := planFor(Env{}, []Detector{
		{"quiet", func(Env) ([]Row, error) { return nil, nil }},
		{"broken", func(Env) ([]Row, error) { return nil, errors.New("probe exploded") }},
	})
	if plan.Ran != 2 || len(plan.Failed) != 1 || plan.Failed[0].Check != "broken" || len(plan.Steps) != 0 {
		t.Fatalf("plan=%+v", plan)
	}
}

// TestPlanPlacesHookAndEnvChecks pins the two gateway-host classes in the
// wiring phase (they edit an account's .claude.json and a settings env, so
// they follow the store moves), and every row whose check could not look —
// unreadable, unparsable, not checked — as FAILED, never as a fix.
func TestPlanPlacesHookAndEnvChecks(t *testing.T) {
	plan := NewPlan(1, []Row{
		{Warn, "beside-backup", "/h/b", "backup", "rm /h/b"},
		{Warn, checkShellClaudeEnv, "/h/store/settings.json", "shell only", "add it to env"},
		{Warn, checkFunctionHookModules, "/h/.cc/1/.claude.json", "flag off", "set the flag"},
		{Block, "store-identity", "/h/store/.credentials.json", "identity in the store", "mv"},
		unparsable(checkFunctionHookModules, "/h/bad.json", errors.New("unexpected end"), "flag unknown"),
		{
			Warn,
			checkShellClaudeEnv,
			"login shell",
			"not checked: pfm runs inside a chat",
			"run pfm doctor from a terminal",
		},
	})
	want := []string{"/h/store/.credentials.json", "/h/.cc/1/.claude.json", "/h/store/settings.json", "/h/b"}
	if got := planPaths(plan.Steps); !reflect.DeepEqual(got, want) {
		t.Fatalf("steps=%q want=%q", got, want)
	}
	if got := planPaths(plan.Failed); !reflect.DeepEqual(got, []string{"/h/bad.json", "login shell"}) {
		t.Fatalf("failed=%q", got)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("stdout closed") }

// TestPlanPrint pins install's verdict on a plan: unknown is 1, a BLOCK row
// is 4, WARN rows alone are 0, and a plan that could not be written is 1.
func TestPlanPrint(t *testing.T) {
	warn := Row{Warn, "shared-db", "/h/s.db", "retired", "rm /h/s.db"}
	block := Row{Block, "legacy-config", "/h/c.json", "legacy", "mv /h/c.json /h/n.json"}
	for _, test := range []struct {
		name    string
		plan    Plan
		code    int
		refusal string
	}{
		{"unknown", NewPlan(0, nil), 1, "no host check ran; the plan is unknown"},
		{"warn only", NewPlan(2, []Row{warn}), 0, ""},
		{
			"blocking",
			NewPlan(2, []Row{warn, block}),
			4,
			"1 blocking — apply the plan above in order, then rerun pfm install --check --plan",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var out strings.Builder
			code, refusal := test.plan.Print(&out)
			if code != test.code || refusal != test.refusal || out.String() != test.plan.Render() {
				t.Fatalf("code=%d refusal=%q out=%q", code, refusal, out.String())
			}
		})
	}
	if code, refusal := NewPlan(2, nil).Print(failingWriter{}); code != 1 ||
		refusal != "write the install plan: stdout closed" {
		t.Fatalf("code=%d refusal=%q", code, refusal)
	}
}

// TestPlanClassesAreRegistered keeps the order table honest: every class it
// places is a registered detector's check, named once.
func TestPlanClassesAreRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, detector := range Detectors() {
		registered[detector.Check] = true
	}
	seen := map[string]bool{}
	for _, entry := range fixOrder {
		if !registered[entry.class] || seen[entry.class] {
			t.Fatalf("class %q: registered=%t duplicate=%t", entry.class, registered[entry.class], seen[entry.class])
		}
		seen[entry.class] = true
	}
	if len(fixOrder) == 0 {
		t.Fatal("fixOrder is empty: the table was not read")
	}
}
