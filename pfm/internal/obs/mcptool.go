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

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// compMCP is the component every MCP tool and prompt record belongs to.
const compMCP = "mcp"

// Tool wraps a typed mcp.AddTool handler in the mcp middleware (spec
// § Middleware): `mcp.AddTool(server, tool, obs.Tool("chat_ls", service.chatLS))`.
// One record per call — tool, kind=tool, target (the input's Target or Chat
// field when it has one), the argument SHAPE (field names and byte sizes,
// never a value), the result's size in bytes, dur_ms and err — written after
// the handler returned and returning exactly what it returned. Both
// transports share the registration, so stdio and HTTP calls log once each.
// A handler error logs at ERROR; a result the handler marked IsError at WARN.
func Tool[In, Out any](name string, handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, request *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		timing := current(ctx).timing
		started := timing.Now()
		result, output, err := handler(ctx, request, input)
		attrs := []slog.Attr{
			slog.String("op", "call"),
			slog.String("kind", "tool"),
			slog.String("tool", name),
			slog.String("args", argumentShape(input)),
			slog.Int64(FieldDur, timing.Now().Sub(started).Milliseconds()),
		}
		if target := targetOf(input); target != "" {
			attrs = append(attrs, slog.String("target", target))
		}
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

// Prompt is Tool for a Server.AddPrompt callback — the harvester's prompt
// door is not an unlogged side entrance. The record carries kind=prompt, the
// prompt name under tool, the argument shape (name and byte size per prompt
// argument), the result size and err.
func Prompt(name string, handler mcp.PromptHandler) mcp.PromptHandler {
	return func(ctx context.Context, request *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		timing := current(ctx).timing
		started := timing.Now()
		result, err := handler(ctx, request)
		var arguments map[string]string
		if request != nil && request.Params != nil {
			arguments = request.Params.Arguments
		}
		attrs := []slog.Attr{
			slog.String("op", "call"),
			slog.String("kind", "prompt"),
			slog.String("tool", name),
			slog.String("args", argumentShape(arguments)),
			slog.Int64(FieldDur, timing.Now().Sub(started).Milliseconds()),
		}
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
