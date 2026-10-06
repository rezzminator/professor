package professor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/obs"
)

// installTimeTokenPattern matches an ALL-CAPS brace metavariable — the same
// pattern the template token gate (dev.sh verify templates) enforces.
var installTimeTokenPattern = regexp.MustCompile(`\{[A-Z][A-Z0-9_]+\}`)

// renderManifest reads only the tokens object; interview and every other
// manifest field are none of pfm init --render's business.
type renderManifest struct {
	Tokens map[string]interface{} `json:"tokens"`
}

// RenderScaffold fills install-time tokens once, from the interview's
// answers, into every scaffolded file still exactly as scaffolded. It prints
// INVALID / RENDERED / SKIP / LEFT lines to stdout and returns the rendered
// and left-with-tokens counts. Every error path but a failed write itself — a
// bad registry, unreadable or empty answers, an INVALID token, no baseline, a
// pin that cannot be inspected — writes nothing to disk.
func RenderScaffold(source, target string, stdout io.Writer) (rendered, left int, err error) {
	trail := obs.NewTrail(context.Background(), "professor", "requested")
	defer func() { trail.End(err) }()

	store, err := InspectStore(source)
	if err != nil {
		return 0, 0, err
	}
	installTime, runtimeSet, err := loadRenderRegistry(store.Root)
	if err != nil {
		return 0, 0, err
	}
	answers, err := loadRenderAnswers(target)
	if err != nil {
		return 0, 0, err
	}
	values, err := validateRenderAnswers(answers, installTime, runtimeSet, stdout)
	if err != nil {
		return 0, 0, err
	}
	baseline, err := loadRenderBaseline(target)
	if err != nil {
		return 0, 0, err
	}

	locals := make([]string, 0, len(baseline.Files))
	for local := range baseline.Files {
		locals = append(locals, local)
	}
	sort.Strings(locals)

	// Every pin is validated and every file inspected before the first write:
	// a failure anywhere leaves the project exactly as it was.
	plans := make([]renderPlan, 0, len(locals))
	for _, local := range locals {
		plan, planErr := planRender(store, target, local, baseline.Files[local], values)
		if planErr != nil {
			return 0, 0, planErr
		}
		plans = append(plans, plan)
	}
	for _, plan := range plans {
		finalBytes := plan.current
		switch {
		case plan.skip != "":
			fmt.Fprintf(stdout, "SKIP %s: %s\n", plan.local, plan.skip)
		case plan.count > 0:
			if err := atomicfile.Write(plan.path, plan.rendered, plan.mode); err != nil {
				return rendered, left, fmt.Errorf("render %s: %w", plan.local, err)
			}
			fmt.Fprintf(stdout, "RENDERED %s (%d substitutions)\n", plan.local, plan.count)
			rendered++
			finalBytes = plan.rendered
		}
		if finalBytes == nil {
			continue
		}
		if leftovers := leftoverTokens(finalBytes, installTime); len(leftovers) > 0 {
			fmt.Fprintf(stdout, "LEFT %s: %s\n", plan.local, strings.Join(leftovers, " "))
			left++
		}
	}
	trail.Reach("rendered", "tokens substituted")
	return rendered, left, nil
}

// renderPlan is one pin's decided outcome: skip (with its reason), render
// (count > 0, rendered holds the new bytes) or leave as is. current is nil
// only for a missing local — nothing to leftover-scan.
type renderPlan struct {
	local, path, skip string
	mode              fs.FileMode
	current, rendered []byte
	count             int
}

// planRender decides one baseline pin without writing. The baseline ships
// inside the project, so its local and template paths are input: they pass
// the same checks buildProjectReport applies before any path is joined.
func planRender(store Store, target, local string, pin FilePin, values map[string]string) (renderPlan, error) {
	plan := renderPlan{local: local}
	cleanLocal, err := safeProjectRelative(local)
	if err != nil {
		return plan, fmt.Errorf("baseline %s: %w", BaselinePath(target), err)
	}
	template, err := safeTemplateRelative(pin.Template)
	if err != nil {
		return plan, fmt.Errorf("baseline %s, %s: %w", BaselinePath(target), local, err)
	}
	plan.path = filepath.Join(target, filepath.FromSlash(cleanLocal))
	info, err := os.Lstat(plan.path)
	if errors.Is(err, fs.ErrNotExist) {
		plan.skip = "missing"
		return plan, nil
	} else if err != nil {
		return plan, fmt.Errorf("inspect %s: %w", local, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return plan, fmt.Errorf("render %s: a symlink, refused — pfm init --render writes only regular files", local)
	}
	projectRoot, err := filepath.EvalSymlinks(target)
	if err != nil {
		return plan, fmt.Errorf("render %s: resolve project: %w", local, err)
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(plan.path))
	if err != nil {
		return plan, fmt.Errorf("render %s: resolve parent: %w", local, err)
	}
	relative, err := filepath.Rel(projectRoot, parent)
	if err != nil {
		return plan, fmt.Errorf("render %s: resolve parent relative to project: %w", local, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return plan, fmt.Errorf("render %s: resolves outside the project", local)
	}
	plan.mode = info.Mode().Perm()
	if plan.current, err = os.ReadFile(plan.path); err != nil {
		return plan, fmt.Errorf("read %s: %w", local, err)
	}
	templatePath := filepath.Join(store.Templates, filepath.FromSlash(template))
	templateHash, err := HashTemplate(templatePath)
	if errors.Is(err, fs.ErrNotExist) || (err == nil && templateHash != pin.TemplateHash) {
		plan.skip = "template changed since pin"
		return plan, nil
	} else if err != nil {
		return plan, fmt.Errorf("hash template %s: %w", pin.Template, err)
	}
	templateRaw, err := os.ReadFile(templatePath)
	if err != nil {
		return plan, fmt.Errorf("read template %s: %w", pin.Template, err)
	}
	if !bytes.Equal(plan.current, addScaffoldMarker(local, pin.Template, pin.PinnedSHA, templateRaw)) {
		plan.skip = "changed since scaffold"
		return plan, nil
	}
	plan.rendered, plan.count = substituteTokens(plan.current, values)
	return plan, nil
}

// loadRenderRegistry splits the store's docs/PLACEHOLDERS.md registry into
// its install-time and runtime metavariable sets, per the line starting
// "## Runtime metavariables".
func loadRenderRegistry(storeRoot string) (installTime, runtimeSet map[string]bool, err error) {
	path := filepath.Join(storeRoot, "docs", "PLACEHOLDERS.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("registry unreadable: %s: %w", path, err)
	}
	lines := strings.Split(string(raw), "\n")
	headingIndex := -1
	for index, line := range lines {
		if strings.HasPrefix(line, "## Runtime metavariables") {
			headingIndex = index
			break
		}
	}
	if headingIndex == -1 {
		return nil, nil, fmt.Errorf(
			"registry unreadable: %s: missing the '## Runtime metavariables' heading", path,
		)
	}
	before := strings.Join(lines[:headingIndex], "\n")
	after := strings.Join(lines[headingIndex:], "\n")
	runtimeSet = tokenSet(after)
	installTime = tokenSet(before)
	for token := range runtimeSet {
		delete(installTime, token)
	}
	if len(installTime) == 0 {
		return nil, nil, fmt.Errorf("registry unreadable: %s: no install-time token registered", path)
	}
	return installTime, runtimeSet, nil
}

func tokenSet(text string) map[string]bool {
	set := make(map[string]bool)
	for _, match := range installTimeTokenPattern.FindAllString(text, -1) {
		set[strings.Trim(match, "{}")] = true
	}
	return set
}

// loadRenderAnswers reads the interview's answers, keyed by token name
// without braces, from the target's .professor/manifest.json.
func loadRenderAnswers(target string) (map[string]interface{}, error) {
	path := filepath.Join(target, ".professor", "manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var manifest renderManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(manifest.Tokens) == 0 {
		return nil, fmt.Errorf("%s: no tokens answered", path)
	}
	return manifest.Tokens, nil
}

// loadRenderBaseline names the baseline path and pfm init when this
// directory was never scaffolded, rather than letting Load's generic
// UNREADABLE wording stand in for "run pfm init".
func loadRenderBaseline(target string) (Baseline, error) {
	path := BaselinePath(target)
	if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
		return Baseline{}, fmt.Errorf("%s not found; run pfm init", path)
	} else if statErr != nil {
		return Baseline{}, fmt.Errorf("inspect baseline %s: %w", path, statErr)
	}
	return Load(target)
}

// validateRenderAnswers prints every INVALID line before returning an error,
// so the interview sees every bad answer at once, not just the first.
func validateRenderAnswers(
	answers map[string]interface{},
	installTime, runtimeSet map[string]bool,
	stdout io.Writer,
) (map[string]string, error) {
	keys := make([]string, 0, len(answers))
	for key := range answers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	values := make(map[string]string, len(answers))
	invalid := false
	for _, key := range keys {
		switch reason, ok := invalidReason(key, answers[key], installTime, runtimeSet); {
		case ok:
			values[key] = reason
		default:
			fmt.Fprintf(stdout, "INVALID %s: %s\n", key, reason)
			invalid = true
		}
	}
	if invalid {
		return nil, errors.New("invalid tokens")
	}
	return values, nil
}

// invalidReason returns (validated string value, true) or (reason, false).
func invalidReason(
	key string,
	raw interface{},
	installTime, runtimeSet map[string]bool,
) (string, bool) {
	switch {
	case runtimeSet[key]:
		return "a runtime metavariable", false
	case !installTime[key]:
		return "not a registered install-time token", false
	}
	value, ok := raw.(string)
	if !ok {
		return "not a string", false
	}
	if strings.TrimSpace(value) == "" {
		return "empty value", false
	}
	if carriesRegisteredToken(value, installTime, runtimeSet) {
		return "value carries a registered token", false
	}
	return value, true
}

func carriesRegisteredToken(value string, installTime, runtimeSet map[string]bool) bool {
	for _, match := range installTimeTokenPattern.FindAllString(value, -1) {
		token := strings.Trim(match, "{}")
		if installTime[token] || runtimeSet[token] {
			return true
		}
	}
	return false
}

// substituteTokens replaces every {KEY} of every supplied key with its
// value in one pass over the original bytes, and returns the substitution count.
func substituteTokens(raw []byte, values map[string]string) ([]byte, int) {
	text := string(raw)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	replacements := make([]string, 0, 2*len(keys))
	total := 0
	for _, key := range keys {
		token := "{" + key + "}"
		total += strings.Count(text, token)
		replacements = append(replacements, token, values[key])
	}
	return []byte(strings.NewReplacer(replacements...).Replace(text)), total
}

// leftoverTokens names every install-time token still present in content,
// sorted and unique — the interview's remaining work.
func leftoverTokens(content []byte, installTime map[string]bool) []string {
	seen := make(map[string]bool)
	for _, match := range installTimeTokenPattern.FindAllString(string(content), -1) {
		token := strings.Trim(match, "{}")
		if installTime[token] {
			seen[token] = true
		}
	}
	tokens := make([]string, 0, len(seen))
	for token := range seen {
		tokens = append(tokens, "{"+token+"}")
	}
	sort.Strings(tokens)
	return tokens
}
