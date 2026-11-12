package mockengine

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/cmdparse"
)

// The literals pfm stages into its Herdr reporter plugin
// (internal/workbench/opencode_herdr_plugin.mjs), and the plugin's timeouts:
// REPORT_TIMEOUT_MS for a queued report, the exit handler's spawnSync for the end.
const (
	openCodePluginBinary  = "const PFM_BINARY = "
	openCodePluginEntry   = "const ENTRY = "
	openCodePluginVerbArg = "herdr-state"
	openCodeReportTimeout = 5 * time.Second
	openCodeEndTimeout    = 1500 * time.Millisecond
)

// openCodePane is the TUI's engine: the session, wrapped in the reports of
// pfm's Herdr plugin when the config OpenCode loads names that plugin.
func openCodePane(proc *process, call openCodeInvocation) paneEngine {
	session := newOpenCodeSession(proc, call)
	binary, entry := openCodeHerdrReporter(proc)
	if binary == "" {
		return session
	}
	return &openCodePluginSession{openCodeSession: session, binary: binary, entry: entry}
}

// openCodeHerdrReporter finds pfm's Herdr reporter among the plugins pfm's
// OPENCODE_CONFIG_CONTENT names: the file plugin whose entry argv runs
// herdr-state. A plugin that cannot be read is warned about and skipped, as
// OpenCode logs a plugin it fails to load and starts without it.
func openCodeHerdrReporter(proc *process) (binary string, entry []string) {
	content := proc.env("OPENCODE_CONFIG_CONTENT")
	if content == "" {
		return "", nil
	}
	var config struct {
		Plugin []string `json:"plugin"`
	}
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		warn(proc.stderr, "OPENCODE_CONFIG_CONTENT %q: %v (no plugin loaded)", content, err)
		return "", nil
	}
	for _, plugin := range config.Plugin {
		location, err := url.Parse(plugin)
		if err != nil {
			warn(proc.stderr, "opencode plugin %q: %v (not loaded)", plugin, err)
			continue
		}
		if location.Scheme != "file" {
			continue // a package plugin: pfm stages none
		}
		body, err := os.ReadFile(location.Path)
		if err != nil {
			warn(proc.stderr, "opencode plugin %s: %v (not loaded)", location.Path, err)
			continue
		}
		binary, entry, err := openCodePluginLiterals(string(body))
		if err != nil {
			warn(proc.stderr, "opencode plugin %s: %v (not loaded)", location.Path, err)
			continue
		}
		if binary != "" && slices.Contains(entry, openCodePluginVerbArg) {
			return binary, entry
		}
	}
	return "", nil
}

// openCodePluginLiterals reads a staged plugin's PFM_BINARY and ENTRY lines.
// Hand-read on purpose: both are JSON literals pfm writes itself (json.Marshal
// at staging), so two prefixed lines need no JavaScript parser.
func openCodePluginLiterals(body string) (binary string, entry []string, err error) {
	for line := range strings.Lines(body) {
		line = strings.TrimSpace(line)
		if value, found := strings.CutPrefix(line, openCodePluginBinary); found {
			if err := json.Unmarshal([]byte(value), &binary); err != nil {
				return "", nil, fmt.Errorf("read %s%s: %w", openCodePluginBinary, value, err)
			}
		}
		if value, found := strings.CutPrefix(line, openCodePluginEntry); found {
			if err := json.Unmarshal([]byte(value), &entry); err != nil {
				return "", nil, fmt.Errorf("read %s%s: %w", openCodePluginEntry, value, err)
			}
		}
	}
	return binary, entry, nil
}

// openCodePluginSession is an OpenCode session whose pane events reach pfm's
// Herdr plugin: each runs `{PFM_BINARY} {ENTRY} {verb}` with the session's JSON
// on stdin, in event order, an identical consecutive report sent once. The
// real plugin queues its reports; the fake sends each before the pane goes on.
type openCodePluginSession struct {
	*openCodeSession
	binary  string
	entry   []string
	started bool   // the plugin's root: a start was reported
	last    string // the last report's verb and payload
	titled  string // the last title reported
}

func (plugin *openCodePluginSession) start(resumed bool) error {
	if err := plugin.openCodeSession.start(resumed); err != nil {
		return err
	}
	plugin.started, plugin.last = true, ""
	plugin.report("start", nil, openCodeReportTimeout)
	return nil
}

func (plugin *openCodePluginSession) prompt(text string) (string, error) {
	blocked, err := plugin.openCodeSession.prompt(text)
	if err != nil || blocked != "" {
		return blocked, err
	}
	// Submits are distinct even when consecutive state reports dedupe.
	plugin.last = ""
	plugin.report("working", map[string]string{"hook_event_name": "chat.message"}, openCodeReportTimeout)
	return "", nil
}

func (plugin *openCodePluginSession) recordUser(text string) error {
	err := plugin.openCodeSession.recordUser(text)
	plugin.reportTitle()
	return err
}

func (plugin *openCodePluginSession) recordAssistant(reply string, usage Tokens) error {
	err := plugin.openCodeSession.recordAssistant(reply, usage)
	plugin.report("idle", nil, openCodeReportTimeout)
	return err
}

func (plugin *openCodePluginSession) rename(name string) error {
	err := plugin.openCodeSession.rename(name)
	plugin.reportTitle()
	return err
}

// finish reports the end as the plugin's exit handler does, before the
// process leaves: outside the dedupe, under its shorter timeout.
func (plugin *openCodePluginSession) finish(reason string) error {
	if plugin.started {
		plugin.started, plugin.last = false, ""
		plugin.report("end", nil, openCodeEndTimeout)
	}
	return plugin.openCodeSession.finish(reason)
}

// reportTitle reports a session title the plugin has not reported yet, as
// OpenCode's session.updated carries it; an empty title is no title.
func (plugin *openCodePluginSession) reportTitle() {
	if plugin.title == "" || plugin.title == plugin.titled {
		return
	}
	plugin.titled = plugin.title
	plugin.report("title", map[string]string{"title": plugin.title}, openCodeReportTimeout)
}

// report runs one plugin report. A failed report is warned about and never
// fails the engine, as the plugin logs it and OpenCode goes on.
func (plugin *openCodePluginSession) report(verb string, fields map[string]string, timeout time.Duration) {
	proc := plugin.proc
	payload := map[string]string{"session_id": plugin.id}
	maps.Copy(payload, fields)
	encoded, err := json.Marshal(payload)
	if err != nil {
		warn(proc.stderr, "pfm-herdr: encode %s report: %v", verb, err)
		return
	}
	key := verb + "\n" + string(encoded)
	if key == plugin.last {
		return
	}
	plugin.last = key
	words := []string{cmdparse.Quote(plugin.binary)}
	for _, word := range append(slices.Clone(plugin.entry), verb) {
		words = append(words, cmdparse.Quote(word))
	}
	answer, err := runHookCommand(proc.ctx, strings.Join(words, " "), encoded, proc.cwd, os.Environ(), timeout)
	if err != nil {
		warn(proc.stderr, "pfm-herdr: %s report: %v", verb, err)
		return
	}
	if answer.exitCode != 0 {
		warn(proc.stderr, "pfm-herdr: %s report exited %d: %s", verb, answer.exitCode, answer.stderr)
	}
}
