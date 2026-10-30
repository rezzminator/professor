//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/testjail"
)

// e2eOldTag names the older release the release-upgrade e2e updates from;
// unset, the fifth-newest release tag of the source repository.
const e2eOldTag = "PFM_E2E_OLD_TAG"

// failingDoctorSource is a file the rollback cases add to their candidate:
// the candidate installs normally, and its doctor reports one failure.
const failingDoctorSource = `package main

import (
	"fmt"
	"os"
)

func init() {
	for _, arg := range os.Args[1:] {
		if arg == "doctor" {
			fmt.Println("doctor: e2e candidate FAIL injected by the rollback case")
			fmt.Println("doctor: failures=1")
			os.Exit(3)
		}
	}
}
`

// TestUpdateFromReleasesE2E installs real releases — the previous one and an
// older one, each built from its own tag — and lets that release's own
// `pfm update` move the host to the tree under test. The updater that runs is
// the OLD binary (field reports #24, #34), so only a real old build proves the
// path adopters take; the updated host must then equal a fresh install of the
// candidate, so state an older release left behind is a failure.
func TestUpdateFromReleasesE2E(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	tags := releaseTagsNewestFirst(t)
	candidate := upgradeCandidateTag(t, tags)
	for _, release := range []struct {
		name  string
		env   string
		index int
	}{
		{"previous", e2ePreviousTag, 0},
		{"old", e2eOldTag, 4},
	} {
		t.Run(release.name, func(t *testing.T) {
			t.Parallel()
			holdUpgradeSlot(t)
			tag := releaseTagFor(t, tags, release.env, release.index)
			h, home := installBase(t, tag, tag, candidate, nil)
			result := h.pfm(home, "update", "--to", candidate, "--repo", h.repo)
			h.requireSuccess("update from "+tag, result)
			if !strings.Contains(result.stdout, "updated "+candidate) {
				t.Fatalf("update from %s output=%q, want target %s", tag, result.stdout, candidate)
			}
			h.requireVersion(home, candidate)
			h.assertInstalled(home)
			h.requireInstalledDoctor(h.pfm(home, "doctor"))
			updated, err := h.snapshot(home)
			if err != nil {
				t.Fatalf("snapshot host updated from %s: %v", tag, err)
			}
			fresh := h.newHome(filepath.Join(home, e2eCanonicalPFM))
			h.requireSuccess("fresh install of "+candidate, h.pfm(fresh, "install", "--yes"))
			expected, err := h.snapshot(fresh)
			if err != nil {
				t.Fatalf("snapshot fresh install of %s: %v", candidate, err)
			}
			emptyHash := sha256.Sum256(nil)
			expected[e2eManagedRoot+"/update.lock"] = "file:" + hex.EncodeToString(emptyHash[:])
			canonicalPluginRecords(t, fresh, expected)
			canonicalPluginRecords(t, home, updated)
			differences, err := snapshotDifferences(expected, updated)
			if err != nil {
				t.Fatal(err)
			}
			if len(differences) != 0 {
				t.Fatalf("host updated from %s differs from a fresh install of %s; differing paths: %s\n%s",
					tag, candidate, strings.Join(differences, ", "), differenceBodies(fresh, home, differences))
			}
		})
	}
}

// TestUpdateRollbackRestoresTheHostE2E installs the tree under test from
// source and updates it to a candidate — the same tree plus a new
// machine-global rule — whose doctor fails. The rollback this tree's updater
// runs must leave the host exactly as it was: nothing the candidate installed
// stays behind (#24: hooks the candidate registered stranded in settings.json).
func TestUpdateRollbackRestoresTheHostE2E(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	holdUpgradeSlot(t)
	base := upgradeCandidateTag(t, releaseTagsNewestFirst(t))
	candidate := nextPatchTag(t, base)
	h, home := installBase(t, "", base, candidate, map[string]string{
		"pfm/cmd/pfm/zz_e2e_failing_doctor.go":    failingDoctorSource,
		"templates/global/rules/e2e-candidate.md": "# E2E candidate rule\n",
	})
	if differences := h.updateRollsBack(home, base, candidate); len(differences) != 0 {
		t.Fatalf("rollback to %s did not restore the host; differing paths: %s\n%s",
			base, strings.Join(differences, ", "), differenceBodies("", home, differences))
	}
}

// TestUpdateRollbackFromPreviousReleaseE2E lets the previous release update
// to a candidate whose doctor fails. That release's rollback is frozen in it:
// it must restore its binary and source, claim no residue, and leave a host
// whose doctor reports no failure. State only the candidate knows, which no
// later fix can teach a released updater to remove, is logged, not asserted.
func TestUpdateRollbackFromPreviousReleaseE2E(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	holdUpgradeSlot(t)
	tags := releaseTagsNewestFirst(t)
	tag := releaseTagFor(t, tags, e2ePreviousTag, 0)
	candidate := upgradeCandidateTag(t, tags)
	h, home := installBase(t, tag, tag, candidate, map[string]string{
		"pfm/cmd/pfm/zz_e2e_failing_doctor.go": failingDoctorSource,
	})
	if differences := h.updateRollsBack(home, tag, candidate); len(differences) != 0 {
		t.Logf("rollback to %s kept candidate state it cannot know: %s", tag, strings.Join(differences, ", "))
	}
	doctor := h.pfm(home, "doctor")
	var exit *exec.ExitError
	if doctor.err != nil && (!errors.As(doctor.err, &exit) || exit.ExitCode() != 1) {
		t.Fatalf("doctor of the host rolled back to %s: %v, want exit 0 or 1 (warnings); stdout=%q stderr=%q",
			tag, doctor.err, doctor.stdout, doctor.stderr)
	}
}

// unauthenticatedCodex is a Codex CLI installed but never logged in: its
// self-doctor fails (field report #13).
const unauthenticatedCodex = `#!/bin/sh
if [ "${1-}" = --version ]; then printf 'codex-cli 0.149.0\n'; exit 0; fi
if [ "${1-}" = doctor ] && [ "${2-}" = --help ]; then printf 'usage: codex doctor\n'; exit 0; fi
if [ "${1-}" = doctor ]; then printf 'auth: not logged in, run codex login\n'; exit 1; fi
if [ "${1-}" = login ]; then printf 'Not logged in\n'; exit 1; fi
exit 2
`

// standingWarning is the doctor row of the user's own ~/.local/bin/engine-sync,
// a warning no update clears.
const standingWarning = "doctor: ~/.local/bin/engine-sync is not pfm's link — left unchanged"

// TestUpdateOnALivedInHostE2E lets the previous release update a host shaped
// like the field reports: Claude accounts selected through CLAUDE_CONFIG_DIR
// (#24), an ANTHROPIC_BASE_URL gateway (#34), a Codex CLI installed but not
// logged in and excluded with codex.homes: [] (#13), and a standing doctor
// warning before the update (#24). The update must land and leave the host's
// doctor with that warning alone.
func TestUpdateOnALivedInHostE2E(t *testing.T) {
	t.Parallel()
	requireE2EFence(t)
	holdUpgradeSlot(t)
	tags := releaseTagsNewestFirst(t)
	tag := releaseTagFor(t, tags, e2ePreviousTag, 0)
	candidate := upgradeCandidateTag(t, tags)
	h, home := installShapedBase(t, tag, tag, candidate, nil, func(h *e2eHarness, home string) {
		h.extraEnv = map[string]string{
			"CLAUDE_CONFIG_DIR":  filepath.Join(home, ".cc", "1"),
			"ANTHROPIC_BASE_URL": "http://127.0.0.1:9/gateway",
		}
		if err := testjail.WriteExecutable(
			filepath.Join(home, ".local", "bin", "codex"), []byte(unauthenticatedCodex), 0o700,
		); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(home, ".codex", "auth.json")); err != nil {
			t.Fatal(err)
		}
		config := h.readJSON(filepath.Join(home, "pfm.config.json"))
		// No Codex account: ask must name claude, or the config is refused.
		config["codex"] = map[string]any{"homes": []any{}}
		ask, _ := config["ask"].(map[string]any)
		if ask == nil {
			ask = map[string]any{}
		}
		ask["engine"] = "claude"
		config["ask"] = ask
		h.writeJSON(filepath.Join(home, "pfm.config.json"), config)
		if err := testjail.WriteExecutable(
			filepath.Join(home, ".local", "bin", "engine-sync"), []byte("#!/bin/sh\nexit 0\n"), 0o700,
		); err != nil {
			t.Fatal(err)
		}
	})
	before := h.pfm(home, "doctor")
	if !strings.Contains(before.stdout+before.stderr, standingWarning) {
		t.Fatalf("the %s host lacks its standing warning %q before the update: %s", tag, standingWarning,
			before.stdout+before.stderr)
	}
	result := h.pfm(home, "update", "--to", candidate, "--repo", h.repo)
	h.requireSuccess("update of a lived-in host from "+tag, result)
	h.requireVersion(home, candidate)
	// The gateway variable lives in the login shell alone: doctor names the
	// chats the MCP service spawns, which would start without it (#34).
	h.requireInstalledDoctorWarnings(h.pfm(home, "doctor"), standingWarning,
		"host-check: WARN shell-claude-env ")
	if _, err := os.Lstat(filepath.Join(home, e2eCodexHooks)); err == nil {
		t.Fatalf("update wrote %s on a host whose codex.homes is empty", e2eCodexHooks)
	}
}

// upgradeSlot keeps one release-upgrade e2e in flight: each holds a source
// fixture, a home and the update's three pfm builds, and the fence's /tmp is a
// capped tmpfs the rest of the suite shares.
var upgradeSlot = make(chan struct{}, 1)

// holdUpgradeSlot waits for the slot and frees it after the test's own
// temporary directories are gone (the cleanup registered first runs last).
func holdUpgradeSlot(t *testing.T) {
	t.Helper()
	upgradeSlot <- struct{}{}
	t.Cleanup(func() { <-upgradeSlot })
}

// updateRollsBack runs `pfm update --to candidate`, which must fail on the
// candidate's injected doctor failure and roll back to base: the binary, its
// version and the source revision restored, no residue claimed. It returns the
// paths of the host that differ from before the update.
func (h *e2eHarness) updateRollsBack(home, base, candidate string) []string {
	h.t.Helper()
	before, err := h.snapshot(home)
	if err != nil {
		h.t.Fatalf("snapshot %s install: %v", base, err)
	}
	head := runGit(h.repo, "rev-parse", "HEAD")
	if head.err != nil {
		h.t.Fatalf("read source revision before update: %v: %s", head.err, head.stderr)
	}
	result := h.pfm(home, "update", "--to", candidate, "--repo", h.repo)
	output := result.stdout + result.stderr
	if result.err == nil {
		h.t.Fatalf("update to a candidate whose doctor fails succeeded, want a rollback; output=%q", output)
	}
	if !strings.Contains(output, "e2e candidate FAIL injected") ||
		!strings.Contains(output, "rolled back source to") {
		h.t.Fatalf("update did not roll back on the candidate's failed doctor; output=%q", output)
	}
	if strings.Contains(output, "rollback residue") {
		h.t.Fatalf("rollback to %s claimed residue; output=%q", base, output)
	}
	h.requireVersion(home, base)
	if after := runGit(h.repo, "rev-parse", "HEAD"); after.err != nil || after.stdout != head.stdout {
		h.t.Fatalf("source revision after rollback=%q err=%v, want %q", after.stdout, after.err, head.stdout)
	}
	after, err := h.snapshot(home)
	if err != nil {
		h.t.Fatalf("snapshot rolled-back host: %v", err)
	}
	emptyHash := sha256.Sum256(nil)
	before[e2eManagedRoot+"/update.lock"] = "file:" + hex.EncodeToString(emptyHash[:])
	differences, err := snapshotDifferences(before, after)
	if err != nil {
		h.t.Fatal(err)
	}
	return differences
}

func (h *e2eHarness) requireVersion(home, tag string) {
	h.t.Helper()
	version := h.pfm(home, "version")
	h.requireSuccess("pfm version", version)
	if got := strings.TrimSpace(version.stdout); got != "pfm "+tag {
		h.t.Fatalf("installed pfm version=%q, want pfm %s", got, tag)
	}
}

// installBase stages a source repository holding base (a release tag, or ""
// for the tree under test) tagged baseTag, then the candidate; builds base's
// own pfm, stamped baseTag, and installs it into a new home from that
// repository, as a host that built it from source.
func installBase(t *testing.T, base, baseTag, candidate string, inject map[string]string) (*e2eHarness, string) {
	t.Helper()
	return installShapedBase(t, base, baseTag, candidate, inject, nil)
}

// installShapedBase is installBase on a home shape reworks before the install.
func installShapedBase(
	t *testing.T,
	base, baseTag, candidate string,
	inject map[string]string,
	shape func(h *e2eHarness, home string),
) (*e2eHarness, string) {
	t.Helper()
	fixture := stageUpgradeRepo(t, base, baseTag, candidate, inject)
	binary := filepath.Join(t.TempDir(), "pfm-"+baseTag)
	if err := testjail.GoBuild(
		filepath.Join(fixture, "pfm"), binary, "./cmd/pfm", "-ldflags", "-X main.version="+baseTag,
	); err != nil {
		t.Fatalf("build %s: %v", baseTag, err)
	}
	h := &e2eHarness{
		t:          t,
		repo:       fixture,
		goCache:    requiredGoEnv(t, "GOCACHE"),
		goModCache: requiredGoEnv(t, "GOMODCACHE"),
	}
	home := h.newHome(binary)
	if shape != nil {
		shape(h, home)
	}
	// The fence's /tmp is a capped tmpfs: drop each copy once its use is over.
	if err := os.Remove(binary); err != nil {
		t.Fatalf("remove the staged %s build: %v", baseTag, err)
	}
	// --skip-harvest: releases before the harvester plugin downloaded its
	// conversion sidecar at install; later ones accept the flag as a no-op.
	h.requireSuccess("install "+baseTag, h.pfm(home, "install", "--yes", "--skip-harvest"))
	return h, home
}

// stageUpgradeRepo makes a repository whose first commit is base's tree (a
// release tag's own, or the tree under test for ""), tagged baseTag, and
// whose second is the tree under test plus inject's files, tagged candidate.
// HEAD is left on baseTag, origin is the repository itself, so
// `pfm update --to candidate` fetches and fast-forwards.
func stageUpgradeRepo(t *testing.T, base, baseTag, candidate string, inject map[string]string) string {
	t.Helper()
	root, workTree, gitDir := sourceRepoRoot(t)
	fixture := filepath.Join(t.TempDir(), "source")
	if base == "" {
		if err := copySourceTreeWithGit(root, fixture, workTree, gitDir); err != nil {
			t.Fatalf("stage the tree under test as %s: %v", baseTag, err)
		}
	} else {
		stageReleaseTree(t, root, gitDir, base, fixture)
	}
	git := func(args ...string) {
		t.Helper()
		runGitFixture(t, fixture, args...)
	}
	commit := func(message, tag string) {
		t.Helper()
		stageFixtureSkill(t, fixture)
		git("add", "-A")
		git("add", "-f", filepath.ToSlash(e2eFixtureSkill))
		git("commit", "-qm", message)
		git("tag", tag)
	}
	git("init", "-q")
	git("config", "user.email", "fixture.invalid")
	git("config", "user.name", "fixture-identity")
	git("config", "core.hooksPath", ".githooks")
	commit("fixture base "+baseTag, baseTag)

	entries, err := os.ReadDir(fixture)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != ".git" {
			if err := os.RemoveAll(filepath.Join(fixture, entry.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := copySourceTreeWithGit(root, fixture, workTree, gitDir); err != nil {
		t.Fatalf("stage the tree under test over %s: %v", baseTag, err)
	}
	for relative, body := range inject {
		target := filepath.Join(fixture, filepath.FromSlash(relative))
		if err := os.WriteFile(target, []byte(body), 0o600); err != nil {
			t.Fatalf("inject %s into the candidate: %v", relative, err)
		}
	}
	commit("fixture candidate "+candidate, candidate)
	git("reset", "--hard", "-q", baseTag)
	git("remote", "add", "origin", fixture)
	return fixture
}

// stageReleaseTree extracts release's own tree from the source repository
// into fixture.
func stageReleaseTree(t *testing.T, root, gitDir, release, fixture string) {
	t.Helper()
	if err := os.MkdirAll(fixture, 0o700); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), release+".tar")
	result := runGit(root, append(sourceGitArgs(gitDir), "archive", "--output="+archive, release)...)
	if result.err != nil {
		t.Fatalf("archive release %s from %s: %v: %s", release, root, result.err, strings.TrimSpace(result.stderr))
	}
	if result := runTool(t.TempDir(), "tar", "-xf", archive, "-C", fixture); result.err != nil {
		t.Fatalf("extract release %s: %v: %s", release, result.err, strings.TrimSpace(result.stderr))
	}
	if err := os.Remove(archive); err != nil {
		t.Fatalf("remove the %s archive: %v", release, err)
	}
}

func stageFixtureSkill(t *testing.T, fixture string) {
	t.Helper()
	target := filepath.Join(fixture, filepath.FromSlash(e2eFixtureSkill))
	if _, err := os.Stat(target); err == nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatalf("stage e2e fixture skill directory: %v", err)
	}
	if err := os.WriteFile(target, []byte("# E2E fixture skill\n"), 0o600); err != nil {
		t.Fatalf("stage e2e fixture skill: %v", err)
	}
}

// sourceGitArgs addresses the source repository's own git metadata: the
// fence's explicit git dir, else discovery from the source root.
func sourceGitArgs(gitDir string) []string {
	args := []string{"-c", "safe.directory=*"}
	if gitDir != "" {
		args = append(args, "--git-dir="+gitDir)
	}
	return args
}

// releaseTagsNewestFirst lists the source repository's release tags, newest
// first; a repository with none (a shallow CI checkout) fails the test.
func releaseTagsNewestFirst(t *testing.T) []string {
	t.Helper()
	root, _, gitDir := sourceRepoRoot(t)
	result := runGit(root, append(sourceGitArgs(gitDir), "tag", "--list", "v*", "--sort=-v:refname")...)
	if result.err != nil {
		t.Fatalf("list release tags of %s: %v: %s", root, result.err, strings.TrimSpace(result.stderr))
	}
	var tags []string
	for _, tag := range strings.Fields(result.stdout) {
		if isReleaseTag(tag) {
			tags = append(tags, tag)
		}
	}
	if len(tags) == 0 {
		t.Fatalf("no release tag in %s: the release-upgrade e2e needs the repository's tags "+
			"(a CI checkout needs fetch-depth: 0)", root)
	}
	return tags
}

// releaseTagFor is env's tag when set, else tags[index].
func releaseTagFor(t *testing.T, tags []string, env string, index int) string {
	t.Helper()
	if explicit := strings.TrimSpace(os.Getenv(env)); explicit != "" {
		if !isReleaseTag(explicit) {
			t.Fatalf("invalid %s=%q: want semantic release tag", env, explicit)
		}
		return explicit
	}
	if index >= len(tags) {
		t.Fatalf("%s unset and the repository holds %d release tag(s), not the %d needed: %s",
			env, len(tags), index+1, strings.Join(tags, " "))
	}
	return tags[index]
}

// upgradeCandidateTag is PFM_E2E_CURRENT_TAG when set, else the next minor
// release after the newest tag: the version the tree under test ships as.
func upgradeCandidateTag(t *testing.T, tags []string) string {
	t.Helper()
	if explicit := strings.TrimSpace(os.Getenv(e2eCurrentTag)); explicit != "" {
		if !isReleaseTag(explicit) {
			t.Fatalf("invalid %s=%q: want semantic release tag", e2eCurrentTag, explicit)
		}
		return explicit
	}
	parts := strings.Split(strings.TrimPrefix(tags[0], "v"), ".")
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		t.Fatalf("derive candidate tag from %s: %v", tags[0], err)
	}
	return fmt.Sprintf("v%s.%d.0", parts[0], minor+1)
}

// nextPatchTag is tag with its patch number raised by one.
func nextPatchTag(t *testing.T, tag string) string {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	patch, err := strconv.Atoi(parts[2])
	if err != nil {
		t.Fatalf("derive the next patch of %s: %v", tag, err)
	}
	return fmt.Sprintf("v%s.%s.%d", parts[0], parts[1], patch+1)
}

// canonicalPluginRecords re-hashes each Claude settings.json of snapshot, and
// the installed_plugins.json beside it, as canonical JSON without the plugin
// ids that settings.json disables. Install disables a retired plugin and never
// uninstalls it (installer retiredClaudePlugins), so a host updated from a
// release that installed one keeps it, disabled, by design; one still enabled
// stays a difference.
func canonicalPluginRecords(t *testing.T, home string, snapshot surfaceSnapshot) {
	t.Helper()
	for key, value := range snapshot {
		if path.Base(key) != "settings.json" || !strings.HasPrefix(value, "file:") {
			continue
		}
		settings := readFixtureJSON(t, home, key)
		enabled, _ := settings["enabledPlugins"].(map[string]any)
		var disabled []string
		for id, on := range enabled {
			if on == false {
				disabled = append(disabled, id)
				delete(enabled, id)
			}
		}
		snapshot[key] = canonicalJSONHash(t, home, settings)
		record := path.Join(path.Dir(key), "plugins", "installed_plugins.json")
		if !strings.HasPrefix(snapshot[record], "file:") {
			continue
		}
		installed := readFixtureJSON(t, home, record)
		plugins, _ := installed["plugins"].(map[string]any)
		for _, id := range disabled {
			delete(plugins, id)
		}
		snapshot[record] = canonicalJSONHash(t, home, installed)
	}
}

func readFixtureJSON(t *testing.T, home, relative string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(home, filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	document := map[string]any{}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse %s: %v", relative, err)
	}
	return document
}

func canonicalJSONHash(t *testing.T, home string, document map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("marshal canonical JSON: %v", err)
	}
	sum := sha256.Sum256([]byte(normalizeHome(string(raw), home)))
	return "canonical:" + hex.EncodeToString(sum[:])
}

// differenceBodies shows each differing path's body in the expected home
// (none when "") and the actual one, so a red names what was left behind, not
// only where.
func differenceBodies(expectedHome, actualHome string, paths []string) string {
	var report strings.Builder
	for _, relative := range paths {
		for _, side := range []struct{ name, home string }{{"want", expectedHome}, {"got", actualHome}} {
			if side.home == "" {
				continue
			}
			target := filepath.Join(side.home, filepath.FromSlash(relative))
			if link, err := os.Readlink(target); err == nil {
				fmt.Fprintf(&report, "%s %s: link to %s\n", side.name, relative, normalizeHome(link, side.home))
				continue
			}
			body, err := os.ReadFile(target)
			if err != nil {
				fmt.Fprintf(&report, "%s %s: %v\n", side.name, relative, err)
				continue
			}
			fmt.Fprintf(&report, "%s %s:\n%s\n", side.name, relative, normalizeHome(string(body), side.home))
		}
	}
	return report.String()
}
