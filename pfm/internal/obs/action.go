package obs

import (
	"context"
	"log/slog"
	"slices"
	"strings"
)

// Entry actions are separate from diagnostics and observer commands.
var entryActions = map[string]string{
	"picker":            "picker.open",
	"doctor":            "ops.doctor",
	"install":           "lifecycle.install",
	"uninstall":         "lifecycle.uninstall",
	"restart":           "lifecycle.restart",
	"generate":          "lifecycle.generate",
	"init":              "lifecycle.init",
	"update":            "lifecycle.update-check",
	"update adopt":      "lifecycle.update-adopt",
	"update pin":        "lifecycle.update-pin",
	"update ignore":     "lifecycle.update-ignore",
	"update drop":       "lifecycle.update-drop",
	"update accept":     "lifecycle.update-accept",
	"archive":           "ops.archive",
	"reap":              "ops.reap",
	"heal":              "ops.heal",
	"name-sync":         "ops.name-sync",
	"index":             "report.index",
	"chat new":          "chat.new",
	"chat open":         "chat.resume",
	"chat reload":       "chat.reload",
	"chat name":         "chat.name",
	"chat call":         "chat.call",
	"chat kill":         "chat.kill",
	"chat unkill":       "chat.unkill",
	"chat end":          "chat.end",
	"chat ask":          "chat.ask-cli",
	"chat branch":       "chat.branch-cli",
	"chat recover":      "chat.recover-cli",
	"chat save":         "chat.save-cli",
	"chat keys":         "chat.keys-cli",
	"chat modal":        "chat.modal-cli",
	"chat schedule set": "chat.schedule-set",
	"chat schedule rm":  "chat.schedule-rm",
	"config init":       "config.init",
	"headless exec":     "headless.exec",
	"theme use":         "theme.use",
}

var hookActions = map[string]string{
	"resume-unkill": "hook.resume-unkill",
	"git-guard":     "hook.git-guard", "store-guard": "hook.store-guard", "pcm-guard": "hook.pcm-guard",
	"engine-guard": "hook.engine-guard", "rr-dir": "hook.rr-dir", "epic-inject": "hook.epic-inject",
	"launcher-repair": "hook.launcher-repair", "herdr-state": "hook.run", "schedule-fire": "schedule.fire",
}

// CommandOperation names an action at its public CLI entry; reads never
// borrow a name from a log message or a child process command.
func CommandOperation(args []string) string {
	if len(args) == 0 {
		return entryActions["picker"]
	}
	if slices.Contains(args, "--help") || slices.Contains(args, "-h") {
		return ""
	}
	if args[0] == "internal" {
		return ""
	}
	for size := min(3, len(args)); size > 0; size-- {
		if op := entryActions[strings.Join(args[:size], " ")]; op != "" {
			return op
		}
	}
	return ""
}

// Action stamps an invocation, leaving subsequent diagnostic records uncredited.
func Action(ctx context.Context, operation, door string) {
	if operation == "" {
		return
	}
	attrs := []slog.Attr{slog.String(FieldAction, operation)}
	if validDoor(door) {
		attrs = append(attrs, slog.String(FieldDoor, door))
	}
	Logger(ctx).LogAttrs(ctx, slog.LevelInfo, "action.start", attrs...)
}

func toolOperation(name string) string {
	switch name {
	case "open", "kill", "unkill", "save":
		return "chat." + name + "-mcp"
	case "keys", "exit":
		return "chat." + name
	default:
		return entryActions["chat "+name]
	}
}
