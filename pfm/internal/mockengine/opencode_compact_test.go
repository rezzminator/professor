package mockengine

import (
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rezzminator/professor/pfm/internal/sqlitedb"
)

func TestOpenCodeCompactionWritesNativeBoundary(t *testing.T) {
	fix := newFixture(t)
	fix.write(
		Scenario{
			Pane:      openCodeShapes,
			SessionID: "ses_atlas01",
			Steps:     []Step{{Type: StepCompact, Reply: "Atlas summary"}},
		},
	)
	session := fix.startTUI("opencode", []string{fix.work}, nil)
	session.waitFrame("composer", func(frame string) bool { return strings.Contains(frame, openCodeShapes.Composer) })
	session.typeLine("/compact")
	session.waitFrame(
		"compact receipt",
		func(frame string) bool { return strings.Contains(frame, openCodeShapes.Compacted) },
	)
	proc := &process{
		ctx:    context.Background(),
		cwd:    fix.work,
		env:    fix.env(nil),
		script: newScript(Scenario{}, engineOpenCode),
		stderr: io.Discard,
	}
	database, err := sqlitedb.OpenReadWrite(openCodeStore(proc), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error(err)
		}
	}()
	var boundary string
	query := `SELECT json_object('role',json_extract(u.data,'$.role'),'auto',json_extract(p.data,'$.auto'),'summary',json_extract(a.data,'$.summary'),'mode',json_extract(a.data,'$.mode'),'text',json_extract(t.data,'$.text')) FROM part p JOIN message u ON u.id=p.message_id JOIN message a ON json_extract(a.data,'$.parentID')=u.id JOIN part t ON t.message_id=a.id WHERE p.session_id=? AND json_extract(p.data,'$.type')='compaction'`
	if err := database.QueryRowContext(context.Background(), query, "ses_atlas01").Scan(&boundary); err != nil {
		t.Fatal(err)
	}
	want := `{"role":"user","auto":0,"summary":1,"mode":"compaction","text":"Atlas summary"}`
	if boundary != want {
		t.Fatalf(
			"%s: boundary=%s, want %s",
			filepath.Join(filepath.Dir(openCodeStore(proc)), "opencode.db"),
			boundary,
			want,
		)
	}
}

func TestOpenCodeCompactionRequiresScriptedSummary(t *testing.T) {
	session := &openCodeSession{}
	if err := session.compact(Step{}); err == nil || err.Error() != "OpenCode compaction needs a scripted reply" {
		t.Fatalf("unscripted compaction: %v", err)
	}
}
