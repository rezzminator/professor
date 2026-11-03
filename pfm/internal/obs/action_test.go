package obs

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestActionEntryAndObserver(t *testing.T) {
	for _, row := range []struct {
		args []string
		op   string
	}{
		{[]string{"install"}, "lifecycle.install"},
		{[]string{}, "picker.open"},
		{[]string{"chat", "new"}, "chat.new"},
		{[]string{"chat", "status"}, ""},
		{[]string{"chat", "ls"}, ""},
		{[]string{"chat", "capture"}, ""},
		{[]string{"log"}, ""},
		{[]string{"install", "--help"}, ""},
		{[]string{"chat", "unknown"}, ""},
	} {
		t.Run(Verb(row.args), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "activity.jsonl")
			ctx, finish := OpenLog(context.Background(), Settings{
				Cmd: Verb(row.args), Args: row.args, Path: path,
				Env: &paths.MapEnv{Values: map[string]string{EnvRun: "synthetic", EnvBeat: "entry"}},
			})
			Logger(ctx).Info("runner.run")
			finish(0)
			records := readLog(t, path)
			wantRecords := 3
			if row.op != "" {
				wantRecords++
			}
			if len(records) != wantRecords {
				t.Fatalf("%s: records=%v; want start, runner, exit", path, records)
			}
			for index, record := range records {
				op, present := record.Field(FieldAction)
				if index == 1 && row.op != "" {
					if !present || op != row.op {
						t.Fatalf("%s: op=%v, want %s", path, op, row.op)
					}
					requireField(t, record, FieldBeat, "entry")
					requireField(t, record, FieldRun, "synthetic")
				} else if present {
					t.Fatalf("%s: observer or diagnostic credited an action: %v", path, record)
				}
			}
		})
	}
}
