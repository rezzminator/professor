package run

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"hostops/pfm/internal/atomicfile"
	pfmengine "hostops/pfm/internal/engine"
	"hostops/pfm/internal/paths"
)

// prepareOpenCode creates the private plugin/config and maps the configured
// OpenCode account into the XDG data root OpenCode actually reads. It returns
// the cleanup operation and the handshake path separately because the caller
// must verify the marker before accepting any output.
func prepareOpenCode(
	environment []string,
	request Request,
	cwd string,
	target *[]string,
) (_ func() error, _ string, runErr error) {
	base, err := ensureScratchBase(request.TempDir)
	if err != nil {
		return nil, "", err
	}
	runDir, err := os.MkdirTemp(base, "pfm-opencode-")
	if err != nil {
		return nil, "", fmt.Errorf("create OpenCode private directory in %s: %w", base, err)
	}
	cleanup := func() error { return os.RemoveAll(runDir) }
	defer func() {
		if runErr != nil {
			if cleanupErr := cleanup(); cleanupErr != nil {
				runErr = errors.Join(runErr, fmt.Errorf("cleanup failed OpenCode preparation: %w", cleanupErr))
			}
		}
	}()

	pluginPath := filepath.Join(runDir, "pfm-headless-plugin.mjs")
	if err := os.WriteFile(pluginPath, []byte(openCodePluginSource), 0o600); err != nil {
		return nil, "", fmt.Errorf("write OpenCode headless plugin: %w", err)
	}
	readyPath := filepath.Join(runDir, "plugin.ready")
	pluginURL := (&url.URL{Scheme: "file", Path: pluginPath}).String()

	config := map[string]any{"plugin": []string{pluginURL}}
	if request.NoSessionPersistence || request.Sealed {
		// These values are consumed by eager OpenCode services before plugin
		// hooks run. The plugin repeats them in config() so both boundaries
		// enforce the same no-persistence contract.
		config["share"] = "disabled"
		config["snapshot"] = false
	}
	var allowedTools []string
	if request.Tools != nil {
		var permissionErr error
		if !openCodeToolsDefault(*request.Tools) {
			allowedTools, permissionErr = openCodeToolNames(*request.Tools)
			if permissionErr != nil {
				return nil, "", permissionErr
			}
		}
	}
	if request.Schema != nil {
		// OpenCode's wildcard deny also hides its synthetic StructuredOutput
		// tool. Keep the schema submission tool available even when the caller
		// selected an empty or narrow Claude tool set.
		if allowedTools != nil {
			allowedTools = appendOpenCodeTool(allowedTools, openCodeStructuredTool)
		}
	}
	content, err := json.Marshal(config)
	if err != nil {
		return nil, "", fmt.Errorf("encode OpenCode headless config: %w", err)
	}

	values := envMap(environment)
	scrubOpenCodeControls(values, request.SettingsSources != nil || request.Sealed)
	if err := configureOpenCodeServerAuth(values); err != nil {
		return nil, "", err
	}
	values["OPENCODE_CONFIG_CONTENT"] = string(content)
	values["PFM_OPENCODE_PLUGIN_READY"] = readyPath
	if openCodeAutoPermission(request.Args) {
		values["PFM_OPENCODE_AUTO_PERMISSION"] = "1"
	}
	if allowedTools != nil {
		allowedJSON, marshalErr := json.Marshal(allowedTools)
		if marshalErr != nil {
			return nil, "", fmt.Errorf("encode OpenCode tool allowlist: %w", marshalErr)
		}
		values["PFM_OPENCODE_ALLOWED_TOOLS_JSON"] = string(allowedJSON)
	}
	if request.SystemPrompt != nil {
		// Run materializes the system prompt before this helper is called.
		if request.systemPromptFile == "" {
			return nil, "", fmt.Errorf("OpenCode system prompt file was not materialized")
		}
		values["PFM_OPENCODE_SYSTEM_FILE"] = request.systemPromptFile
	}
	if request.Schema != nil {
		if request.schemaFilePath == "" {
			return nil, "", fmt.Errorf("OpenCode output schema file was not materialized")
		}
		values["PFM_OPENCODE_SCHEMA_FILE"] = request.schemaFilePath
		values["PFM_OPENCODE_SCHEMA_READY"] = filepath.Join(runDir, "schema.ready")
	}
	values["PFM_OPENCODE_ASSISTANTS_FILE"] = filepath.Join(runDir, "assistants.json")
	values["PFM_OPENCODE_TOOL_IDS_FILE"] = filepath.Join(runDir, "tool-ids.json")
	if request.StrictMCP {
		values["PFM_OPENCODE_STRICT_MCP"] = "1"
	}
	if request.Effort != "" {
		values["PFM_OPENCODE_EFFORT"] = request.Effort
	}
	if request.NoSessionPersistence || request.Sealed {
		values["PFM_OPENCODE_NO_SESSION_PERSISTENCE"] = "1"
	}
	if request.Sealed {
		values["OPENCODE_DISABLE_CLAUDE_CODE"] = "1"
	}
	// OPENCODE_PURE disables external plugins. A caller-supplied copy would
	// otherwise weaken the required system/schema/tool controls silently.
	delete(values, "OPENCODE_PURE")

	accountRoot := filepath.Clean(request.ConfigDir)
	if accountRoot == "." || accountRoot == "" {
		return nil, "", fmt.Errorf("OpenCode account data directory is empty")
	}
	if filepath.Base(accountRoot) != openCodeDataDirName() {
		return nil, "", fmt.Errorf("OpenCode account data directory %q must end in opencode", accountRoot)
	}
	if !filepath.IsAbs(accountRoot) {
		return nil, "", fmt.Errorf("OpenCode account data directory %q must be absolute", accountRoot)
	}

	dataHome := filepath.Dir(accountRoot)
	var authCleanup func() error
	if request.NoSessionPersistence || request.Sealed {
		dataHome = filepath.Join(runDir, "data")
		if err := os.MkdirAll(filepath.Join(dataHome, openCodeDataDirName()), 0o700); err != nil {
			return nil, "", fmt.Errorf("create private OpenCode data home: %w", err)
		}
		authCleanup, err = stageOpenCodeAuth(
			filepath.Join(accountRoot, "auth.json"),
			filepath.Join(dataHome, openCodeDataDirName(), "auth.json"),
		)
		if err != nil {
			return nil, "", err
		}
	}
	if authCleanup != nil {
		previousCleanup := cleanup
		cleanup = func() error {
			authErr := authCleanup()
			removeErr := previousCleanup()
			if authErr != nil && removeErr != nil {
				return fmt.Errorf("preserve OpenCode account credentials: %v; cleanup run: %w", authErr, removeErr)
			}
			if authErr != nil {
				return fmt.Errorf("preserve OpenCode account credentials: %w", authErr)
			}
			return removeErr
		}
	}
	values["XDG_DATA_HOME"] = dataHome
	actualCWD := cwd
	if actualCWD == "" {
		actualCWD, err = os.Getwd()
		if err != nil {
			return nil, "", fmt.Errorf("resolve OpenCode working directory: %w", err)
		}
	} else if !filepath.IsAbs(actualCWD) {
		actualCWD, err = filepath.Abs(actualCWD)
		if err != nil {
			return nil, "", fmt.Errorf("resolve OpenCode working directory %q: %w", cwd, err)
		}
	}

	if request.Sealed || (request.SettingsSources != nil && strings.TrimSpace(*request.SettingsSources) == "") {
		values["HOME"] = filepath.Join(runDir, "home")
		values["XDG_CONFIG_HOME"] = filepath.Join(runDir, "config")
		values["XDG_STATE_HOME"] = filepath.Join(runDir, "state")
		values["XDG_CACHE_HOME"] = filepath.Join(runDir, "cache")
		values["OPENCODE_DISABLE_PROJECT_CONFIG"] = "1"
	} else if request.SettingsSources != nil {
		// The operator's own home is read ONLY here, for an explicit `user`
		// settings source. Resolving it unconditionally would make every
		// OpenCode run depend on a host path it never looks at.
		resolved, resolveErr := paths.Resolve()
		if resolveErr != nil {
			return nil, "", fmt.Errorf("resolve OpenCode operator home: %w", resolveErr)
		}
		if err := configureOpenCodeSources(
			values,
			runDir,
			resolved.Home,
			actualCWD,
			*request.SettingsSources,
		); err != nil {
			return nil, "", err
		}
	}

	if values["HOME"] == filepath.Join(runDir, "home") {
		if err := os.MkdirAll(values["HOME"], 0o700); err != nil {
			return nil, "", fmt.Errorf("create private OpenCode home: %w", err)
		}
	}
	values["PWD"] = actualCWD
	*target = envList(values)
	return cleanup, readyPath, nil
}

func openCodeDataDirName() string {
	return filepath.Base(pfmengine.MustLookup(pfmengine.OpenCode).DefaultRoots("")[0])
}

func scrubOpenCodeControls(values map[string]string, isolateSources bool) {
	for key := range values {
		if strings.HasPrefix(key, "PFM_OPENCODE_") {
			delete(values, key)
		}
	}
	// These flags override the account, database, permission, or project
	// identity selected by pfm. Leaving one inherited in place would let an
	// explicit account silently read another store or weaken a mapped control.
	for _, key := range []string{
		"OPENCODE_AUTH_CONTENT", "OPENCODE_DB", "OPENCODE_PERMISSION",
		"OPENCODE_WORKSPACE_ID", "OPENCODE_EXPERIMENTAL_WORKSPACES", "OPENCODE_FAKE_VCS",
		"OPENCODE_SERVER_USERNAME", "OPENCODE_SERVER_PASSWORD",
	} {
		delete(values, key)
	}
	delete(values, "OPENCODE_PURE")
	// Model metadata is fetched lazily by the server and can make a headless
	// startup wait indefinitely on an unavailable models.dev endpoint. The
	// selected model still resolves through the authenticated provider.
	values["OPENCODE_DISABLE_MODELS_FETCH"] = "1"
	if isolateSources {
		delete(values, "OPENCODE_CONFIG")
		delete(values, "OPENCODE_CONFIG_DIR")
		delete(values, "OPENCODE_DISABLE_PROJECT_CONFIG")
	}
}

func configureOpenCodeServerAuth(values map[string]string) error {
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return fmt.Errorf("generate OpenCode server credentials: %w", err)
	}
	username := "pfm-" + base64.RawURLEncoding.EncodeToString(random[:9])
	password := base64.RawURLEncoding.EncodeToString(random[9:])
	values["OPENCODE_SERVER_USERNAME"] = username
	values["OPENCODE_SERVER_PASSWORD"] = password
	return nil
}

func stageOpenCodeAuth(source, target string) (func() error, error) {
	body, err := os.ReadFile(source)
	if errors.Is(err, os.ErrNotExist) {
		// A manually supplied test roster may not have an auth file. OpenCode
		// will report the missing subscription login from the child process.
		return func() error { return nil }, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read OpenCode account credentials: %w", err)
	}
	if err := os.WriteFile(target, body, 0o600); err != nil {
		return nil, fmt.Errorf("stage OpenCode account credentials: %w", err)
	}
	return func() error { return preserveOpenCodeAuth(source, target, body) }, nil
}

func preserveOpenCodeAuth(source, target string, original []byte) error {
	updated, err := os.ReadFile(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read staged OpenCode account credentials: %w", err)
	}
	if bytes.Equal(updated, original) {
		return nil
	}
	current, err := os.ReadFile(source)
	if errors.Is(err, os.ErrNotExist) {
		// The account was removed or replaced while this run was active. Do not
		// recreate it from a stale refresh result.
		return nil
	}
	if err != nil {
		return fmt.Errorf("read OpenCode account credentials before refresh: %w", err)
	}
	if !bytes.Equal(current, original) {
		// Another process refreshed or edited the account. Skip this best-effort
		// write rather than deliberately replacing the newer state.
		return nil
	}
	// The source comparison above is best-effort: a writer can still race the
	// check. Atomic replacement guarantees that either complete credential
	// document is visible, so a race cannot leave a torn auth file.
	if err := atomicfile.Write(source, updated, 0o600); err != nil {
		return fmt.Errorf("install refreshed OpenCode account credentials: %w", err)
	}
	return nil
}

func configureOpenCodeSources(values map[string]string, runDir, operatorHome, cwd, raw string) error {
	allowed := map[string]bool{"user": false, "project": false, "local": false}
	for _, item := range strings.Split(raw, ",") {
		name := strings.ToLower(strings.TrimSpace(item))
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; !ok {
			return fmt.Errorf("OpenCode setting source %q is unsupported (want user, project, or local)", name)
		}
		allowed[name] = true
	}
	// An explicit source selection owns the project layer. Native project
	// discovery walks every ancestor and would silently reintroduce sources
	// that were omitted from --setting-sources.
	values["OPENCODE_DISABLE_PROJECT_CONFIG"] = "1"
	if allowed["user"] {
		configHome := strings.TrimSpace(values["XDG_CONFIG_HOME"])
		if configHome == "" {
			configHome = filepath.Join(operatorHome, ".config")
		}
		values["XDG_CONFIG_HOME"] = configHome
		values["HOME"] = operatorHome
	} else {
		values["XDG_CONFIG_HOME"] = filepath.Join(runDir, "config")
		values["HOME"] = filepath.Join(runDir, "home")
	}
	if allowed["local"] {
		localDir, err := projectOpenCodeDir(cwd)
		if err != nil {
			return fmt.Errorf("resolve OpenCode local settings: %w", err)
		}
		if localDir != "" {
			values["OPENCODE_CONFIG_DIR"] = localDir
		}
	} else {
		delete(values, "OPENCODE_CONFIG_DIR")
	}
	if allowed["project"] {
		projectConfig, err := projectOpenCodeConfig(cwd)
		if err != nil {
			return fmt.Errorf("resolve OpenCode project settings: %w", err)
		}
		if projectConfig != "" {
			values["OPENCODE_CONFIG"] = projectConfig
		}
	}
	return nil
}

func projectOpenCodeDir(cwd string) (string, error) {
	root, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	for {
		candidate := filepath.Join(root, ".opencode")
		info, statErr := os.Stat(candidate)
		if statErr == nil {
			if info.IsDir() {
				return candidate, nil
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		atRepositoryRoot, markerErr := hasRepositoryMarker(root)
		if markerErr != nil {
			return "", markerErr
		}
		if atRepositoryRoot {
			return "", nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", nil
		}
		root = parent
	}
}

func projectOpenCodeConfig(cwd string) (string, error) {
	root, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	for {
		for _, name := range []string{"opencode.jsonc", "opencode.json"} {
			candidate := filepath.Join(root, name)
			info, statErr := os.Stat(candidate)
			if statErr == nil {
				if info.Mode().IsRegular() {
					return candidate, nil
				}
				continue
			}
			if !errors.Is(statErr, os.ErrNotExist) {
				return "", statErr
			}
		}
		atRepositoryRoot, markerErr := hasRepositoryMarker(root)
		if markerErr != nil {
			return "", markerErr
		}
		if atRepositoryRoot {
			return "", nil
		}
		parent := filepath.Dir(root)
		if parent == root {
			return "", nil
		}
		root = parent
	}
}

func hasRepositoryMarker(root string) (bool, error) {
	_, err := os.Lstat(filepath.Join(root, ".git"))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func openCodeToolsDefault(raw string) bool {
	for _, value := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		if strings.EqualFold(strings.TrimSpace(value), "default") {
			return true
		}
	}
	return false
}

func openCodeToolNames(raw string) ([]string, error) {
	mapping := map[string]string{
		"bash": "bash", "edit": openCodeToolEdit, "multiedit": openCodeToolEdit, "read": openCodeToolRead,
		"glob": "glob", "grep": "grep", "ls": openCodeToolRead, "list": openCodeToolRead,
		"webfetch": "webfetch", "websearch": "websearch", "task": "task", "write": "write",
		"apply_patch": openCodeToolApplyPatch, "applypatch": openCodeToolApplyPatch,
		"patch": openCodeToolApplyPatch, "todowrite": "todowrite",
	}
	seen := map[string]bool{}
	result := make([]string, 0)
	for _, value := range strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' }) {
		value = strings.TrimSpace(value)
		key := strings.ToLower(value)
		if key == "default" {
			continue
		}
		if key == "notebookedit" {
			return nil, fmt.Errorf("OpenCode does not support Claude tool %q", value)
		}
		if strings.HasPrefix(key, "mcp__") {
			parts := strings.SplitN(value[len("mcp__"):], "__", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return nil, fmt.Errorf("OpenCode MCP tool %q must be mcp__server__tool", value)
			}
			serverName := sanitizeOpenCodeToolPart(parts[0])
			toolName := sanitizeOpenCodeToolPart(parts[1])
			if serverName == "" || toolName == "" {
				return nil, fmt.Errorf("OpenCode MCP tool %q has no usable server or tool name", value)
			}
			name := serverName + "_" + toolName
			if !seen[name] {
				seen[name] = true
				result = append(result, name)
			}
			continue
		}
		name, ok := mapping[key]
		if !ok {
			// OpenCode can expose configured custom tools. Preserve their exact
			// identifier; lowercasing an unknown name would silently select a
			// different tool or disable the requested one.
			name = value
		}
		if name != "" && !seen[name] {
			seen[name] = true
			result = append(result, name)
		}
	}
	return result, nil
}

func appendOpenCodeTool(tools []string, name string) []string {
	for _, tool := range tools {
		if tool == name {
			return tools
		}
	}
	return append(tools, name)
}

func sanitizeOpenCodeToolPart(value string) string {
	var result strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			result.WriteRune(r)
		} else {
			result.WriteByte('_')
		}
	}
	return result.String()
}

func validateOpenCodeArgs(args []string, request Request) error {
	blocked := map[string]bool{
		"--model": true, "-m": true, "--variant": true, openCodeAttachFlag: true, "--pure": true,
	}
	if !request.Native {
		blocked["--format"] = true
	}
	if request.CWD != "" || request.SettingsSources != nil {
		blocked["--dir"] = true
	}
	if request.SystemPrompt != nil || request.Sealed {
		blocked["--agent"] = true
	}
	if request.NoSessionPersistence || request.Sealed {
		blocked["--session"] = true
		blocked["-s"] = true
		blocked["--continue"] = true
		blocked["-c"] = true
		blocked["--fork"] = true
		blocked["--share"] = true
	}
	if request.Tools != nil {
		// A resumed session carries its own permission rules. OpenCode merges
		// those after agent configuration, so the common --tools allowlist
		// could otherwise be widened by a native session/continue/fork flag.
		blocked["--session"] = true
		blocked["-s"] = true
		blocked["--continue"] = true
		blocked["-c"] = true
		blocked["--fork"] = true
	}
	for _, argument := range args {
		name := openCodeArgumentName(argument)
		if blocked[name] {
			return fmt.Errorf("OpenCode native argument %q conflicts with an explicit pfm control", argument)
		}
	}
	if request.Sealed && len(args) != 0 {
		return fmt.Errorf("sealed OpenCode headless runs do not accept native arguments")
	}
	return nil
}

func openCodeArgumentName(argument string) string {
	name := argument
	if strings.HasPrefix(name, "--") {
		name, _, _ = strings.Cut(name, "=")
		return name
	}
	// OpenCode accepts attached short-option values (for example, -mMODEL)
	// and clustered short flags. Match the protected option prefix so a native
	// tail cannot bypass a mapped model or session control by changing spelling.
	for _, short := range []string{"-m", "-s", "-c"} {
		if strings.HasPrefix(name, short) && len(name) > len(short) {
			return short
		}
	}
	return name
}

func openCodeModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" || strings.Contains(model, "/") {
		return model
	}
	if strings.HasPrefix(strings.ToLower(model), "gpt-") {
		return "openai/" + model
	}
	return model
}

func openCodeEffortSupported(effort string) bool {
	switch effort {
	case "none", "minimal", "low", "medium", "high", "xhigh":
		return true
	default:
		return false
	}
}

func envMap(environment []string) map[string]string {
	values := make(map[string]string, len(environment))
	for _, item := range environment {
		name, value, ok := strings.Cut(item, "=")
		if ok {
			values[name] = value
		}
	}
	return values
}

func envList(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	// Stable environment ordering makes child diagnostics and tests easier to
	// inspect while preserving every variable's value.
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

// verifyOpenCodePluginFor re-reads the handshake marker after the run. A
// non-OpenCode request has no plugin and passes; an OpenCode one whose marker
// was never configured is an error, never a silent pass — the marker is the
// only proof the private controls were in force.
func verifyOpenCodePluginFor(request Request) error {
	if request.Engine != pfmengine.OpenCode {
		return nil
	}
	if request.openCodePluginReady == "" {
		return errors.New("OpenCode headless required plugin handshake was not configured")
	}
	return verifyOpenCodePluginPath(request.openCodePluginReady)
}

func verifyOpenCodePluginPath(path string) error {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("OpenCode headless required plugin did not load")
	}
	if err != nil {
		return fmt.Errorf("read OpenCode plugin handshake: %w", err)
	}
	if strings.TrimSpace(string(body)) != "pfm-opencode-plugin-ready" {
		return fmt.Errorf("OpenCode headless plugin handshake is invalid")
	}
	return nil
}

func verifyOpenCodeSchemaPath(path string) error {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("OpenCode schema format was not decoded by the public REST API")
	}
	if err != nil {
		return fmt.Errorf("read OpenCode schema handshake: %w", err)
	}
	if strings.TrimSpace(string(body)) != "pfm-opencode-schema-ready" {
		return errors.New("OpenCode schema handshake is invalid")
	}
	return nil
}

// Match the three boolean flags used by OpenCode run's noninteractive
// permission responder. Each alias retains its final value; run ORs them.
func openCodeAutoPermission(args []string) bool {
	values := map[string]bool{}
	for i, argument := range args {
		if argument == "--" {
			break
		}
		name, value, explicit := strings.Cut(argument, "=")
		negative := strings.HasPrefix(name, "--no-")
		if negative {
			name = "--" + strings.TrimPrefix(name, "--no-")
		}
		switch name {
		case "--auto", "--yolo", "--dangerously-skip-permissions":
			if !explicit && i+1 < len(args) && (args[i+1] == "true" || args[i+1] == "false") {
				value, explicit = args[i+1], true
			}
			values[name] = !negative && (!explicit || value == "true")
		}
	}
	for _, enabled := range values {
		if enabled {
			return true
		}
	}
	return false
}
