package mockengine

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	pfmcall "github.com/rezzminator/professor/pfm/internal/call"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/testjail"
	"github.com/rezzminator/professor/pfm/internal/workbench"
)

// The fake OpenCode loads pfm's Herdr reporter plugin from the config pfm
// hands it, among other plugins, and plays its reports from the pane: a
// start, the prompt's working, the session's first title, idle at the turn's
// end, the title a /rename sets at once, and the end at exit.
func TestOpenCodePaneReportsThroughPfmsHerdrPlugin(t *testing.T) {
	fix := newFixture(t)
	fix.write(Scenario{Pane: openCodeShapes, Steps: []Step{{Type: StepTurn, Reply: "first reply"}}})
	reports := filepath.Join(fix.root, "reports.log")
	reporter := filepath.Join(fix.bin, "pfm-reporter")
	script := "#!/bin/sh\nprintf '%s %s\\n' \"$*\" \"$(cat)\" >> '" + reports + "'\n"
	if err := testjail.WriteExecutable(reporter, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	guard, herdrPlugin := filepath.Join(fix.root, "guard.js"), filepath.Join(fix.root, "herdr.mjs")
	if err := workbench.StageOpenCodeEngineGuardPlugin(guard, reporter); err != nil {
		t.Fatal(err)
	}
	if err := workbench.StageOpenCodeHerdrPlugin(herdrPlugin, reporter); err != nil {
		t.Fatal(err)
	}
	config, err := json.Marshal(map[string][]string{"plugin": {
		(&url.URL{Scheme: "file", Path: guard}).String(), (&url.URL{Scheme: "file", Path: herdrPlugin}).String(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	session := fix.startTUI("opencode", []string{fix.work, "--session", fixtureOpenCodeSession},
		map[string]string{"OPENCODE_CONFIG_CONTENT": string(config)})
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, openCodeShapes.Composer) })
	session.typeLine("prove the bound")
	session.waitFrame("idle reply", func(frame string) bool {
		return strings.Contains(frame, "⏺ first reply") && !pfmcall.IsBusyFor(pfmengine.OpenCode, frame)
	})
	session.typeLine("/rename Fix login")
	session.typeLine("/exit")
	if code := session.waitExit(); code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, session.stderr.String())
	}
	const entry = "internal herdr-state --engine opencode "
	want := []string{
		entry + `start {"session_id":"ses_fixture0001"}`,
		entry + `working {"hook_event_name":"chat.message","session_id":"ses_fixture0001"}`,
		entry + `title {"session_id":"ses_fixture0001","title":"prove the bound"}`,
		entry + `idle {"session_id":"ses_fixture0001"}`,
		entry + `title {"session_id":"ses_fixture0001","title":"Fix login"}`,
		entry + `end {"session_id":"ses_fixture0001"}`,
	}
	body, err := os.ReadFile(reports)
	got := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("reports in %s = %q (err %v), want %q; stderr=%q", reports, got, err, want, session.stderr.String())
	}
}
