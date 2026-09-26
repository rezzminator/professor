package obs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// compMCP is the component every MCP tool and prompt record belongs to.
const compMCP = "mcp"

// nonChatTargetTools are the registered tool names whose Target field
// addresses something other than a chat — chat_save's is a FILE PATH
// (mcpserv.SaveInput's doc comment), the field `pfm log --chat` filters on —
// so Tool never records their Target value as the log's target field. Their
// path still reaches the log as a SIZE only, through args' shape
// (argumentShape already reports target:<byte length> for every field).
var nonChatTargetTools = map[string]bool{
	"chat_save": true,
}

// Tool wraps a typed mcp.AddTool handler in the mcp middleware (spec
// § Middleware): `mcp.AddTool(server, tool, obs.Tool("chat_ls", service.chatLS))`.
// One record per call — tool, kind=tool, target (the input's Target or Chat
// field when it has one and the tool is not in nonChatTargetTools), the
// argument SHAPE (field names and byte sizes, never a value), the result's
// size in bytes, dur_ms and err — written after the handler returned and
// returning exactly what it returned. Both transports share the
// registration, so stdio and HTTP calls log once each. A handler error logs
// at ERROR; a result the handler marked IsError at WARN.
//
// A panicking handler is recovered here, at the one chokepoint every
// registered tool crosses: the MCP SDK runs each request on its own
// goroutine with no recover of its own, so an unrecovered panic would kill
// the machine-wide daemon. The recovered call is one ERROR record naming the
// tool (op=call, kind=tool, tool=name) plus obs.Recovered's bounded,
// scrubbed message, and an error returned to the SDK so the caller sees a
// tool error — the daemon survives and the next request runs normally.
func Tool[In, Out any](name string, handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, request *mcp.CallToolRequest, input In) (result *mcp.CallToolResult, output Out, err error) {
		timing := current(ctx).timing
		started := timing.Now()
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			err = Recovered("tool "+name, recovered)
			var zero Out
			result, output = nil, zero
			attrs := append(toolAttrs(name, input, started, timing), slog.String(FieldErr, err.Error()))
			Logger(Component(ctx, compMCP)).LogAttrs(ctx, slog.LevelError, "mcp.call", attrs...)
		}()
		result, output, err = handler(ctx, request, input)
		attrs := toolAttrs(name, input, started, timing)
		level := slog.LevelInfo
		switch {
		case err != nil:
			level = slog.LevelError
			attrs = append(attrs, slog.String(FieldErr, err.Error()))
		case result != nil && result.IsError:
			level = slog.LevelWarn
			attrs = append(attrs, slog.String(FieldErr, "tool result IsError"), slog.Int("bytes", encodedSize(result)))
		case result != nil:
			attrs = append(attrs, slog.Int("bytes", encodedSize(result)))
		default:
			attrs = append(attrs, slog.Int("bytes", encodedSize(output)))
		}
		Logger(Component(ctx, compMCP)).LogAttrs(ctx, level, "mcp.call", attrs...)
		return result, output, err
	}
}

// toolAttrs is the record shape every mcp.call for a tool carries before the
// result/err-specific attrs: op, kind, tool, the argument shape and dur_ms,
// plus target when the input has one and name is not a nonChatTargetTools
// entry. Shared between the success path and the recovered-panic path so
// both write the identical shape.
func toolAttrs(name string, input any, started time.Time, timing clock.Clock) []slog.Attr {
	attrs := []slog.Attr{
		slog.String("op", "call"),
		slog.String("kind", "tool"),
		slog.String("tool", name),
		slog.String("args", argumentShape(input)),
		slog.Int64(FieldDur, timing.Now().Sub(started).Milliseconds()),
	}
	if target := targetOf(input); target != "" && !nonChatTargetTools[name] {
		attrs = append(attrs, slog.String("target", target))
	}
	return attrs
}

// Prompt is Tool for a Server.AddPrompt callback — the harvester's prompt
// door is not an unlogged side entrance. The record carries kind=prompt, the
// prompt name under tool, the argument shape (name and byte size per prompt
// argument), the result size and err. A panicking handler is recovered the
// same way Tool's is: one ERROR record, an error returned to the SDK, the
// daemon survives.
func Prompt(name string, handler mcp.PromptHandler) mcp.PromptHandler {
	return func(ctx context.Context, request *mcp.GetPromptRequest) (result *mcp.GetPromptResult, err error) {
		timing := current(ctx).timing
		started := timing.Now()
		var arguments map[string]string
		if request != nil && request.Params != nil {
			arguments = request.Params.Arguments
		}
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			err = Recovered("prompt "+name, recovered)
			result = nil
			attrs := append(promptAttrs(name, arguments, started, timing), slog.String(FieldErr, err.Error()))
			Logger(Component(ctx, compMCP)).LogAttrs(ctx, slog.LevelError, "mcp.call", attrs...)
		}()
		result, err = handler(ctx, request)
		attrs := promptAttrs(name, arguments, started, timing)
		level := slog.LevelInfo
		if err != nil {
			level = slog.LevelError
			attrs = append(attrs, slog.String(FieldErr, err.Error()))
		} else {
			attrs = append(attrs, slog.Int("bytes", encodedSize(result)))
		}
		Logger(Component(ctx, compMCP)).LogAttrs(ctx, level, "mcp.call", attrs...)
		return result, err
	}
}

// promptAttrs is toolAttrs's Prompt-shaped sibling: kind=prompt, no target
// (no registered prompt takes one today).
func promptAttrs(name string, arguments map[string]string, started time.Time, timing clock.Clock) []slog.Attr {
	return []slog.Attr{
		slog.String("op", "call"),
		slog.String("kind", "prompt"),
		slog.String("tool", name),
		slog.String("args", argumentShape(arguments)),
		slog.Int64(FieldDur, timing.Now().Sub(started).Milliseconds()),
	}
}

// argumentShape renders the SHAPE of a tool input — `target:4,message:27` —
// the exported fields of a struct (json names, declaration order) or the keys
// of a map (sorted), each with its byte size; a scalar is `bytes:N`; nil is
// empty. Sizes are a string's length or its JSON encoding's; a value itself
// never appears.
func argumentShape(value any) string {
	reflected, present := dereference(value)
	if !present {
		return ""
	}
	parts := make([]string, 0, 8)
	switch reflected.Kind() {
	case reflect.Struct:
		for index := range reflected.NumField() {
			field := reflected.Type().Field(index)
			name := jsonName(field)
			if !field.IsExported() || name == "-" {
				continue
			}
			parts = append(parts, name+":"+strconv.Itoa(encodedSizeOf(reflected.Field(index))))
		}
	case reflect.Map:
		for _, key := range reflected.MapKeys() {
			parts = append(parts, fmt.Sprint(key.Interface())+":"+strconv.Itoa(encodedSizeOf(reflected.MapIndex(key))))
		}
		sort.Strings(parts)
	default:
		parts = append(parts, "bytes:"+strconv.Itoa(encodedSizeOf(reflected)))
	}
	return strings.Join(parts, ",")
}

// targetOf reads the chat a tool input addresses: a struct's Target or Chat
// string field, a map's "target" or "chat" string entry, else empty.
func targetOf(value any) string {
	reflected, present := dereference(value)
	if !present {
		return ""
	}
	for _, name := range []string{"Target", "Chat"} {
		var field reflect.Value
		switch reflected.Kind() {
		case reflect.Struct:
			field = reflected.FieldByName(name)
		case reflect.Map:
			if reflected.Type().Key().Kind() == reflect.String {
				field = reflected.MapIndex(reflect.ValueOf(strings.ToLower(name)).Convert(reflected.Type().Key()))
			}
		}
		if field.IsValid() && field.Kind() == reflect.Interface && !field.IsNil() {
			field = field.Elem()
		}
		if field.IsValid() && field.Kind() == reflect.String && field.String() != "" {
			return field.String()
		}
	}
	return ""
}

// dereference follows pointers and interfaces to the value they hold; present
// is false for nil at any hop.
func dereference(value any) (reflect.Value, bool) {
	reflected := reflect.ValueOf(value)
	for reflected.IsValid() && (reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface) {
		if reflected.IsNil() {
			return reflect.Value{}, false
		}
		reflected = reflected.Elem()
	}
	return reflected, reflected.IsValid()
}

// jsonName is the name a field carries on the wire: its json tag's name, else
// the Go field name.
func jsonName(field reflect.StructField) string {
	name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
	if name == "" {
		return field.Name
	}
	return name
}

// encodedSizeOf is a string's byte length or the JSON size of anything else;
// -1 when the value cannot be encoded, so an unencodable field is visible
// rather than silently absent.
func encodedSizeOf(value reflect.Value) int {
	if value.Kind() == reflect.Interface && !value.IsNil() {
		value = value.Elem()
	}
	if value.Kind() == reflect.String {
		return len(value.String())
	}
	if !value.IsValid() || !value.CanInterface() {
		return -1
	}
	return encodedSize(value.Interface())
}

// encodedSize is the JSON size of value, -1 when it cannot be encoded.
func encodedSize(value any) int {
	encoded, err := json.Marshal(value)
	if err != nil {
		return -1
	}
	return len(encoded)
}
