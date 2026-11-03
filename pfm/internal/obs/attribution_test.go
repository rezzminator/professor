package obs

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/paths"
)

func TestActivityAttribution(t *testing.T) {
	for _, door := range []string{"typed", "cli", "picker", "mcp", "hook"} {
		t.Run(door, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "activity.jsonl")
			ctx, finish := OpenLog(context.Background(), Settings{
				Cmd: "chat", Level: "debug", Path: path,
				Env: &paths.MapEnv{Values: map[string]string{
					EnvBeat: "birth", EnvRun: "synthetic", EnvDoor: door, EnvChat: "atlas", EnvSession: "session-atlas",
				}},
			})
			ctx = With(
				ctx,
				FieldOp,
				"chat.call",
				FieldBeat,
				"attention",
				FieldChat,
				"beacon",
				FieldSession,
				"session-beacon",
				FieldCaller,
				"atlas",
				FieldCallee,
				"beacon",
				FieldParent,
				"cobalt",
			)
			end := Span(ctx, "chat.call")
			end(nil)
			Logger(
				ctx,
			).Info("chat.result", FieldOp, "chat.call", "prompt", "private prompt", "account", "/fixture/account", "reason", "sk-secret")
			Action(ctx, "chat.call", door)
			finish(0)
			records := readLog(t, path)
			if len(records) != 6 {
				t.Fatalf("%s: records = %d, want 6", path, len(records))
			}
			for _, record := range records {
				if record.Message == "action.start" {
					requireField(t, record, FieldRun, "synthetic")
				} else if _, exists := record.Field(FieldRun); exists {
					t.Fatalf("diagnostic gained run: %v", record)
				}
				if record.Message == "action.start" {
					requireField(t, record, FieldDoor, door)
				} else if _, exists := record.Field(FieldDoor); exists {
					t.Fatalf("diagnostic gained door: %v", record)
				}
			}
			requireField(t, records[4], FieldBeat, "attention")
			requireField(t, records[5], FieldChat, "atlas")
			for _, record := range records[1:4] {
				for key, value := range map[string]string{
					FieldChat: "beacon", FieldSession: "session-beacon", FieldOp: "chat.call",
					FieldCaller: "atlas", FieldCallee: "beacon", FieldParent: "cobalt",
				} {
					requireField(t, record, key, value)
				}
			}
			for _, key := range []string{"prompt", "account", "reason"} {
				requireField(t, records[3], key, "<redacted>")
			}
		})
	}
}

func TestToolAttributionChangesPerRequest(t *testing.T) {
	ctx, recorder := Test(t)
	tool := Tool(
		"call",
		func(ctx context.Context, _ *mcp.CallToolRequest, _ toolInput) (*mcp.CallToolResult, toolOutput, error) {
			Logger(ctx).Info("chat.deliver")
			return nil, toolOutput{Status: "delivered"}, nil
		},
	)
	for _, beat := range []string{"first-call", "second-call"} {
		request := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: mcp.Meta{
			"pfmTrace": map[string]any{
				FieldRun:     "synthetic",
				FieldBeat:    beat,
				FieldCaller:  "atlas",
				FieldCallee:  "beacon",
				FieldSession: "session-atlas",
			},
		}}}
		_, result, err := tool(ctx, request, toolInput{Target: "beacon"})
		if err != nil || result.Status != "delivered" {
			t.Fatalf("tool = %+v, %v; want delivered", result, err)
		}
	}
	Logger(ctx).Info("unscoped")
	records := recorder.Records()
	if len(records) != 7 {
		t.Fatalf("records = %d, want 7: %s", len(records), recorder.Raw())
	}
	for index, beat := range []string{"first-call", "first-call", "first-call", "second-call", "second-call", "second-call"} {
		if index == 0 || index == 3 {
			requireField(t, records[index], FieldBeat, beat)
			requireField(t, records[index], FieldRun, "synthetic")
		} else if _, exists := records[index].Field(FieldBeat); exists {
			t.Fatalf("diagnostic gained beat: %v", records[index])
		}
		if index == 0 || index == 3 {
			requireField(t, records[index], FieldDoor, "mcp")
		} else if _, exists := records[index].Field(FieldDoor); exists {
			t.Fatalf("diagnostic gained door: %v", records[index])
		}
		requireField(t, records[index], FieldCaller, "atlas")
		requireField(t, records[index], FieldCallee, "beacon")
		requireField(t, records[index], FieldSession, "session-atlas")
	}
	if _, exists := records[6].Field(FieldBeat); exists {
		t.Fatalf("unscoped diagnostic gained beat: %v", records[6])
	}
	requireField(t, records[0], FieldAction, "chat.call")
	requireField(t, records[3], FieldAction, "chat.call")
}

func TestAttributionDefaultsAndGroups(t *testing.T) {
	for _, row := range []struct{ cmd, door string }{{"chat", "cli"}, {"picker", "picker"}, {"internal", "hook"}, {"mcp", "mcp"}} {
		t.Run(row.cmd, func(t *testing.T) {
			recorder := &Recorder{}
			logger := slog.New(
				attributed(
					slog.NewJSONHandler(recorder, nil),
					&paths.MapEnv{Values: map[string]string{EnvDoor: "invalid"}},
					row.cmd,
				),
			)
			logger.With(FieldBeat, "outer").WithGroup("detail").With(FieldBeat, "inner").Info("runner.run", "count", 2)
			record := recorder.Records()[0]
			if _, exists := record.Fields[FieldAction]; exists {
				t.Fatalf("runner diagnostic credited an action: %s", recorder.Raw())
			}
			requireField(t, record, FieldOp, "runner.run")
			if _, exists := record.Field(FieldDoor); exists {
				t.Fatalf("diagnostic gained door: %v", record)
			}
			logger.With(FieldDoor, "shim.cx").Info("chat.placement")
			Action(context.WithValue(context.Background(), contextKey{}, &scope{logger: logger}), "chat.call", "")
			requireField(t, recorder.Records()[1], FieldDoor, "shim.cx")
			requireField(t, recorder.Records()[2], FieldDoor, row.door)
			requireField(t, recorder.Records()[2], FieldAction, "chat.call")
			requireField(t, recorder.Records()[2], FieldBeat, "")
			requireField(t, recorder.Records()[2], FieldRun, "")
			group, ok := record.Fields["detail"].(map[string]any)
			if !ok || group["count"] != float64(2) || group[FieldBeat] != "inner" {
				t.Fatalf("detail = %#v, want count=2 and beat=inner", group)
			}
		})
	}
}
