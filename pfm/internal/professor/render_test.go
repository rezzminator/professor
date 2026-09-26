package professor

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newRenderFixtureStore lays down a self-hosted blueprint clone whose
// CLAUDE.md template carries one install-time token supplied by the fixture
// manifests ({PROJECT_NAME}), one left unsupplied ({PROJECT_TAGLINE}), one
// runtime metavariable ({SHA}), and one lowercase pattern metavariable
// ({project}) — enough to exercise every render/leftover/metavariable rule
// in one template. docs/PLACEHOLDERS.md carries both registry sections.
func newRenderFixtureStore(t *testing.T) string {
	t.Helper()
	root := newScaffoldFixtureStore(t)
	claudeMD := filepath.Join(root, "templates", "project", "CLAUDE.md")
	if err := os.WriteFile(claudeMD, []byte(
		"# {PROJECT_NAME} contract\ntagline: {PROJECT_TAGLINE}\nsha: {SHA}\ngeneric: {project}\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	placeholders := filepath.Join(root, "docs", "PLACEHOLDERS.md")
	if err := os.MkdirAll(filepath.Dir(placeholders), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(placeholders, []byte(
		"# Placeholders\n\n"+
			"Install-time tokens: {PROJECT_NAME}, {PROJECT_TAGLINE}.\n\n"+
			"## Runtime metavariables\n\n"+
			"Filled at runtime: {SHA}.\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// scaffoldedRenderTarget scaffolds source into a fresh target directory and
// returns it, failing the test on any scaffold error.
func scaffoldedRenderTarget(t *testing.T, source string) string {
	t.Helper()
	target := t.TempDir()
	if _, err := Scaffold(source, target, false, io.Discard); err != nil {
		t.Fatalf("Scaffold() err = %v", err)
	}
	return target
}

func writeRenderManifest(t *testing.T, target, tokensJSON string) {
	t.Helper()
	path := filepath.Join(target, ".professor", "manifest.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"tokens":`+tokensJSON+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
}

func claudeMDPath(target string) string {
	return filepath.Join(target, ClaudeInstructionsFile)
}

// TestRenderScaffoldRendersOnceAndReportsLeftovers pins the "render",
// "once", "leftovers" and "metavariables" Done-when rows together: a fresh
// scaffold renders PROJECT_NAME once and reports the unsupplied
// PROJECT_TAGLINE as LEFT, the runtime {SHA} and lowercase {project} are
// never touched or reported, and a second --render over the same answers
// skips the now-changed file byte-identically.
func TestRenderScaffoldRendersOnceAndReportsLeftovers(t *testing.T) {
	source := newRenderFixtureStore(t)
	target := scaffoldedRenderTarget(t, source)
	writeRenderManifest(t, target, `{"PROJECT_NAME":"Acme"}`)

	var stdout bytes.Buffer
	rendered, left, err := RenderScaffold(source, target, &stdout)
	if err != nil {
		t.Fatalf("RenderScaffold() err = %v, stdout=%q", err, stdout.String())
	}
	if rendered != 1 || left != 1 {
		t.Fatalf("RenderScaffold() = (%d, %d), want (1, 1): stdout=%q", rendered, left, stdout.String())
	}
	if !strings.Contains(stdout.String(), "RENDERED CLAUDE.md (1 substitutions)") {
		t.Fatalf("stdout missing RENDERED line: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "LEFT CLAUDE.md: {PROJECT_TAGLINE}") {
		t.Fatalf("stdout missing LEFT line: %q", stdout.String())
	}
	rendered1, err := os.ReadFile(claudeMDPath(target))
	if err != nil {
		t.Fatal(err)
	}
	want := "# Acme contract\ntagline: {PROJECT_TAGLINE}\nsha: {SHA}\ngeneric: {project}\n"
	if string(rendered1) != want {
		t.Fatalf("rendered CLAUDE.md = %q, want %q", rendered1, want)
	}
	if strings.Contains(stdout.String(), "{SHA}") && strings.Contains(stdout.String(), "LEFT") {
		t.Fatalf("runtime metavariable {SHA} reported as LEFT: %q", stdout.String())
	}

	// Once: a second --render over the same answers never re-renders, and
	// the file is byte-identical to after the first run.
	stdout.Reset()
	rendered2, left2, err := RenderScaffold(source, target, &stdout)
	if err != nil {
		t.Fatalf("second RenderScaffold() err = %v, stdout=%q", err, stdout.String())
	}
	if rendered2 != 0 {
		t.Fatalf("second RenderScaffold() rendered=%d, want 0: stdout=%q", rendered2, stdout.String())
	}
	if left2 != 1 {
		t.Fatalf("second RenderScaffold() left=%d, want 1: stdout=%q", left2, stdout.String())
	}
	if strings.Contains(stdout.String(), "RENDERED") {
		t.Fatalf("second run printed a RENDERED line: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "SKIP CLAUDE.md: changed since scaffold") {
		t.Fatalf("second run missing SKIP changed-since-scaffold: %q", stdout.String())
	}
	rendered2Bytes, err := os.ReadFile(claudeMDPath(target))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rendered1, rendered2Bytes) {
		t.Fatalf("second run changed the file: before=%q after=%q", rendered1, rendered2Bytes)
	}
}

// TestRenderScaffoldSkipsAHandEditedFile pins the "hand-edited file" row: a
// scaffolded file touched before render is skipped and left untouched.
func TestRenderScaffoldSkipsAHandEditedFile(t *testing.T) {
	source := newRenderFixtureStore(t)
	target := scaffoldedRenderTarget(t, source)
	writeRenderManifest(t, target, `{"PROJECT_NAME":"Acme"}`)

	edited := []byte("# hand-edited\n")
	if err := os.WriteFile(claudeMDPath(target), edited, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	rendered, _, err := RenderScaffold(source, target, &stdout)
	if err != nil {
		t.Fatalf("RenderScaffold() err = %v", err)
	}
	if rendered != 0 {
		t.Fatalf("RenderScaffold() rendered=%d, want 0: stdout=%q", rendered, stdout.String())
	}
	if !strings.Contains(stdout.String(), "SKIP CLAUDE.md: changed since scaffold") {
		t.Fatalf("stdout = %q, want SKIP changed since scaffold", stdout.String())
	}
	got, err := os.ReadFile(claudeMDPath(target))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, edited) {
		t.Fatalf("hand-edited file was touched: got %q, want %q", got, edited)
	}
}

// TestRenderScaffoldSkipsATemplateMovedSincePin pins the "template moved"
// row: the store template diverging from the pin's TemplateHash skips.
func TestRenderScaffoldSkipsATemplateMovedSincePin(t *testing.T) {
	source := newRenderFixtureStore(t)
	target := scaffoldedRenderTarget(t, source)
	writeRenderManifest(t, target, `{"PROJECT_NAME":"Acme"}`)

	templatePath := filepath.Join(source, "templates", "project", "CLAUDE.md")
	if err := os.WriteFile(templatePath, []byte("# moved upstream\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	rendered, _, err := RenderScaffold(source, target, &stdout)
	if err != nil {
		t.Fatalf("RenderScaffold() err = %v", err)
	}
	if rendered != 0 {
		t.Fatalf("RenderScaffold() rendered=%d, want 0: stdout=%q", rendered, stdout.String())
	}
	if !strings.Contains(stdout.String(), "SKIP CLAUDE.md: template changed since pin") {
		t.Fatalf("stdout = %q, want SKIP template changed since pin", stdout.String())
	}
}

// TestRenderScaffoldSkipsAMissingLocal pins the "missing local" row.
func TestRenderScaffoldSkipsAMissingLocal(t *testing.T) {
	source := newRenderFixtureStore(t)
	target := scaffoldedRenderTarget(t, source)
	writeRenderManifest(t, target, `{"PROJECT_NAME":"Acme"}`)

	if err := os.Remove(claudeMDPath(target)); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	rendered, left, err := RenderScaffold(source, target, &stdout)
	if err != nil {
		t.Fatalf("RenderScaffold() err = %v", err)
	}
	if rendered != 0 || left != 0 {
		t.Fatalf("RenderScaffold() = (%d, %d), want (0, 0): stdout=%q", rendered, left, stdout.String())
	}
	if !strings.Contains(stdout.String(), "SKIP CLAUDE.md: missing") {
		t.Fatalf("stdout = %q, want SKIP missing", stdout.String())
	}
}

// TestRenderScaffoldRejectsUnregisteredKey pins the "unregistered key" row.
func TestRenderScaffoldRejectsUnregisteredKey(t *testing.T) {
	source := newRenderFixtureStore(t)
	target := scaffoldedRenderTarget(t, source)
	writeRenderManifest(t, target, `{"LOG_FILE":"/var/log/x"}`)

	var stdout bytes.Buffer
	rendered, left, err := RenderScaffold(source, target, &stdout)
	if err == nil {
		t.Fatalf("RenderScaffold() err = nil, want an error: stdout=%q", stdout.String())
	}
	if rendered != 0 || left != 0 {
		t.Fatalf("RenderScaffold() = (%d, %d), want (0, 0) on error", rendered, left)
	}
	if !strings.Contains(stdout.String(), "INVALID LOG_FILE: not a registered install-time token") {
		t.Fatalf("stdout = %q, want the INVALID line", stdout.String())
	}
	assertUnchanged(t, target)
}

// TestRenderScaffoldRejectsARuntimeKey pins the "runtime key" row.
func TestRenderScaffoldRejectsARuntimeKey(t *testing.T) {
	source := newRenderFixtureStore(t)
	target := scaffoldedRenderTarget(t, source)
	writeRenderManifest(t, target, `{"SHA":"abc123"}`)

	var stdout bytes.Buffer
	_, _, err := RenderScaffold(source, target, &stdout)
	if err == nil {
		t.Fatalf("RenderScaffold() err = nil, want an error: stdout=%q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "INVALID SHA: a runtime metavariable") {
		t.Fatalf("stdout = %q, want the INVALID line", stdout.String())
	}
	assertUnchanged(t, target)
}

// TestRenderScaffoldRejectsBadValues pins the "bad values" row: a number, an
// empty or whitespace string, and a value carrying a registered token.
func TestRenderScaffoldRejectsBadValues(t *testing.T) {
	cases := []struct {
		name       string
		tokensJSON string
		want       string
	}{
		{"number", `{"PROJECT_NAME":42}`, "INVALID PROJECT_NAME: not a string"},
		{"empty", `{"PROJECT_NAME":""}`, "INVALID PROJECT_NAME: empty value"},
		{"whitespace", `{"PROJECT_NAME":"   "}`, "INVALID PROJECT_NAME: empty value"},
		{
			"carries a token",
			`{"PROJECT_NAME":"prefix {PROJECT_TAGLINE} suffix"}`,
			"INVALID PROJECT_NAME: value carries a registered token",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			source := newRenderFixtureStore(t)
			target := scaffoldedRenderTarget(t, source)
			writeRenderManifest(t, target, testCase.tokensJSON)

			var stdout bytes.Buffer
			_, _, err := RenderScaffold(source, target, &stdout)
			if err == nil {
				t.Fatalf("RenderScaffold() err = nil, want an error: stdout=%q", stdout.String())
			}
			if !strings.Contains(stdout.String(), testCase.want) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), testCase.want)
			}
			assertUnchanged(t, target)
		})
	}
}

// TestRenderScaffoldRejectsMissingOrEmptyAnswers pins the "no answers" row:
// a missing manifest, unparsable JSON, and an empty tokens object.
func TestRenderScaffoldRejectsMissingOrEmptyAnswers(t *testing.T) {
	t.Run("manifest missing", func(t *testing.T) {
		source := newRenderFixtureStore(t)
		target := scaffoldedRenderTarget(t, source)
		var stdout bytes.Buffer
		_, _, err := RenderScaffold(source, target, &stdout)
		if err == nil {
			t.Fatal("RenderScaffold() err = nil, want an error")
		}
		manifestPath := filepath.Join(target, ".professor", "manifest.json")
		if !strings.Contains(err.Error(), manifestPath) {
			t.Fatalf("error = %v, want it to name %s", err, manifestPath)
		}
		assertUnchanged(t, target)
	})

	t.Run("manifest unparsable", func(t *testing.T) {
		source := newRenderFixtureStore(t)
		target := scaffoldedRenderTarget(t, source)
		manifestPath := filepath.Join(target, ".professor", "manifest.json")
		if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout bytes.Buffer
		_, _, err := RenderScaffold(source, target, &stdout)
		if err == nil {
			t.Fatal("RenderScaffold() err = nil, want an error")
		}
		if !strings.Contains(err.Error(), manifestPath) {
			t.Fatalf("error = %v, want it to name %s", err, manifestPath)
		}
		assertUnchanged(t, target)
	})

	t.Run("tokens empty", func(t *testing.T) {
		source := newRenderFixtureStore(t)
		target := scaffoldedRenderTarget(t, source)
		writeRenderManifest(t, target, `{}`)
		var stdout bytes.Buffer
		_, _, err := RenderScaffold(source, target, &stdout)
		if err == nil {
			t.Fatal("RenderScaffold() err = nil, want an error")
		}
		manifestPath := filepath.Join(target, ".professor", "manifest.json")
		if !strings.Contains(err.Error(), manifestPath) {
			t.Fatalf("error = %v, want it to name %s", err, manifestPath)
		}
		assertUnchanged(t, target)
	})
}

// TestRenderScaffoldRejectsAnUnscaffoldedDir pins the "no baseline" row.
func TestRenderScaffoldRejectsAnUnscaffoldedDir(t *testing.T) {
	source := newRenderFixtureStore(t)
	target := t.TempDir()
	writeRenderManifest(t, target, `{"PROJECT_NAME":"Acme"}`)

	var stdout bytes.Buffer
	_, _, err := RenderScaffold(source, target, &stdout)
	if err == nil {
		t.Fatal("RenderScaffold() err = nil, want an error")
	}
	baselinePath := BaselinePath(target)
	if !strings.Contains(err.Error(), baselinePath) || !strings.Contains(err.Error(), "pfm init") {
		t.Fatalf("error = %v, want it to name %s and pfm init", err, baselinePath)
	}
}

// TestRenderScaffoldRejectsABrokenRegistry pins the "registry broken" row:
// a missing PLACEHOLDERS.md, one missing the heading, and one with no
// install-time token.
func TestRenderScaffoldRejectsABrokenRegistry(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		source := newRenderFixtureStore(t)
		if err := os.Remove(filepath.Join(source, "docs", "PLACEHOLDERS.md")); err != nil {
			t.Fatal(err)
		}
		target := scaffoldedRenderTarget(t, source)
		writeRenderManifest(t, target, `{"PROJECT_NAME":"Acme"}`)
		var stdout bytes.Buffer
		_, _, err := RenderScaffold(source, target, &stdout)
		if err == nil || !strings.Contains(err.Error(), "registry unreadable") {
			t.Fatalf("error = %v, want registry unreadable", err)
		}
	})

	t.Run("no heading", func(t *testing.T) {
		source := newRenderFixtureStore(t)
		placeholders := filepath.Join(source, "docs", "PLACEHOLDERS.md")
		if err := os.WriteFile(placeholders, []byte("Install-time tokens: {PROJECT_NAME}.\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		target := scaffoldedRenderTarget(t, source)
		writeRenderManifest(t, target, `{"PROJECT_NAME":"Acme"}`)
		var stdout bytes.Buffer
		_, _, err := RenderScaffold(source, target, &stdout)
		if err == nil || !strings.Contains(err.Error(), "registry unreadable") {
			t.Fatalf("error = %v, want registry unreadable", err)
		}
	})

	t.Run("no install-time token", func(t *testing.T) {
		source := newRenderFixtureStore(t)
		placeholders := filepath.Join(source, "docs", "PLACEHOLDERS.md")
		if err := os.WriteFile(placeholders, []byte(
			"# Placeholders\n\n## Runtime metavariables\n\nFilled at runtime: {SHA}.\n",
		), 0o600); err != nil {
			t.Fatal(err)
		}
		target := scaffoldedRenderTarget(t, source)
		writeRenderManifest(t, target, `{"PROJECT_NAME":"Acme"}`)
		var stdout bytes.Buffer
		_, _, err := RenderScaffold(source, target, &stdout)
		if err == nil || !strings.Contains(err.Error(), "registry unreadable") {
			t.Fatalf("error = %v, want registry unreadable", err)
		}
	})
}

// assertUnchanged verifies an error path wrote nothing: CLAUDE.md is still
// exactly the scaffolded template bytes.
func assertUnchanged(t *testing.T, target string) {
	t.Helper()
	got, err := os.ReadFile(claudeMDPath(target))
	if err != nil {
		t.Fatal(err)
	}
	want := "# {PROJECT_NAME} contract\ntagline: {PROJECT_TAGLINE}\nsha: {SHA}\ngeneric: {project}\n"
	if string(got) != want {
		t.Fatalf("CLAUDE.md changed on an error path: got %q, want %q", got, want)
	}
}

// TestRenderScaffoldSkipsATemplateGoneUpstream: a store refreshed between
// init and render may no longer carry a pinned template. That pin is a
// template changed since pin — skipped — and every other file still renders.
// Watched failing against the HashTemplate error aborting the whole render
// (exit 1, no summary) on the gone template.
func TestRenderScaffoldSkipsATemplateGoneUpstream(t *testing.T) {
	source := newRenderFixtureStore(t)
	target := scaffoldedRenderTarget(t, source)
	writeRenderManifest(t, target, `{"PROJECT_NAME":"Acme"}`)
	if err := os.Remove(filepath.Join(source, "templates", "project", "rumdl-policy.toml")); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	rendered, _, err := RenderScaffold(source, target, &stdout)
	if err != nil {
		t.Fatalf("RenderScaffold() err = %v, stdout=%q", err, stdout.String())
	}
	if rendered != 1 || !strings.Contains(stdout.String(), "SKIP .rumdl.toml: template changed since pin\n") ||
		!strings.Contains(stdout.String(), "RENDERED CLAUDE.md (1 substitutions)") {
		t.Fatalf("RenderScaffold() rendered=%d stdout=%q", rendered, stdout.String())
	}
}

// TestRenderScaffoldWritesNothingWhenALaterLocalCannotBeInspected: a local
// whose path cannot be inspected (a parent that is a file: ENOTDIR) is a
// failure to look, never "missing", and it fails the run before any file is
// written — CLAUDE.md, which sorts before it and would render, stays as
// scaffolded. Watched failing against the stat-error-is-missing branch:
// SKIP docs/guide.md: missing, exit 0, CLAUDE.md rendered.
func TestRenderScaffoldWritesNothingWhenALaterLocalCannotBeInspected(t *testing.T) {
	source := newRenderFixtureStore(t)
	target := scaffoldedRenderTarget(t, source)
	writeRenderManifest(t, target, `{"PROJECT_NAME":"Acme"}`)
	baseline, err := Load(target)
	if err != nil {
		t.Fatal(err)
	}
	baseline.Files["docs/guide.md"] = baseline.Files[ClaudeInstructionsFile]
	if err := Save(target, baseline); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(target, "docs"),
		[]byte("a file where a directory belongs\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(claudeMDPath(target))
	if err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	_, _, err = RenderScaffold(source, target, &stdout)
	if err == nil || !strings.Contains(err.Error(), "docs/guide.md") {
		t.Fatalf("RenderScaffold() err = %v, want a failure naming docs/guide.md: stdout=%q", err, stdout.String())
	}
	if after, _ := os.ReadFile(claudeMDPath(target)); !bytes.Equal(after, before) {
		t.Fatalf("CLAUDE.md was written before the failing local: %q", after)
	}
}

// TestRenderScaffoldRefusesABaselineLocalOutsideTheProject: baseline.json
// ships inside a cloned project, so its local and template paths are input.
// A local escaping the project root is refused before anything is written,
// as buildProjectReport refuses it. Watched failing against the unchecked
// filepath.Join: ../escape.toml, byte-equal to its unmarked template, was
// rendered outside the project.
func TestRenderScaffoldRefusesABaselineLocalOutsideTheProject(t *testing.T) {
	source := newRenderFixtureStore(t)
	policy := []byte("name = \"{PROJECT_NAME}\"\n")
	policyTemplate := filepath.Join(source, "templates", "project", "rumdl-policy.toml")
	if err := os.WriteFile(policyTemplate, policy, 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "project")
	if _, err := Scaffold(source, target, false, io.Discard); err != nil {
		t.Fatalf("Scaffold() err = %v", err)
	}
	writeRenderManifest(t, target, `{"PROJECT_NAME":"Acme"}`)
	baseline, err := Load(target)
	if err != nil {
		t.Fatal(err)
	}
	baseline.Files["../escape.toml"] = baseline.Files[".rumdl.toml"]
	if err := Save(target, baseline); err != nil {
		t.Fatal(err)
	}
	escape := filepath.Join(filepath.Dir(target), "escape.toml")
	if err := os.WriteFile(escape, policy, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	if _, _, err := RenderScaffold(source, target, &stdout); err == nil {
		t.Fatalf("RenderScaffold() err = nil, want the escaping local refused: stdout=%q", stdout.String())
	}
	if got, _ := os.ReadFile(escape); !bytes.Equal(got, policy) {
		t.Fatalf("a file outside the project was rendered: %q", got)
	}
	assertUnchanged(t, target)
}
