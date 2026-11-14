//go:build journey

package journey

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/professor"
	"github.com/rezzminator/professor/pfm/journey/harness"
)

// The owner's project before Professor: its README and its own markdown lint policy, at the path
// pfm init scaffolds Professor's policy into.
const (
	adopterReadme = "the owner wrote this before Professor\n"
	adopterRumdl  = "# the markdown lint policy the owner wrote before Professor\n"
	rumdlLocal    = ".rumdl.toml"
	rumdlTemplate = "project/rumdl-policy.toml"
)

// deployed is the count line pfm init prints after it scaffolds.
var deployed = regexp.MustCompile(`deployed (\d+) project files; baseline: `)

// adopterInit: in a plain terminal the owner makes a fresh git project of their own under ~/work and
// types `pfm init .`; every file the init wrote is pinned in .professor/baseline.json at the clone's
// commit and template, nothing else is; the owner's own files are as they were, the one on a scaffold
// path left unpinned under a CONFLICT row.
func adopterInit(c *harness.Ctx, s State) State {
	name := c.Unique("adopter")
	dir := filepath.Join(s.Home, "work", name)
	term := c.Terminal("adopter")
	term.WaitFor(prompt)
	if code := shellRun(term, "project", `mkdir -p "$HOME/work/`+name+`" && cd "$HOME/work/`+name+
		`" && git init -q && printf '%s\n' '`+strings.TrimSuffix(adopterReadme, "\n")+`' >README.md && printf '%s\n' '`+
		strings.TrimSuffix(adopterRumdl, "\n")+`' >`+rumdlLocal, stepBound); code != 0 {
		c.Failf("making the owner's project exited %d:\n%s", code, term.Screen())
	}
	if code := shellRun(term, "init", "pfm init .", stepBound); code != 0 {
		c.Failf("pfm init . exited %d:\n%s", code, term.Screen())
	}
	screen := term.Screen()
	m := deployed.FindStringSubmatch(screen)
	conflict := "CONFLICT " + rumdlLocal + ": exists"
	if m == nil || strings.Count(screen, "CONFLICT ") != 1 || !strings.Contains(screen, conflict) ||
		strings.Contains(screen, "SKIPPED ") {
		c.Failf("pfm init's screen wants a `deployed N project files` line, the one row %q and no SKIPPED row:\n%s",
			conflict, screen)
	}
	count, _ := strconv.Atoi(m[1])
	baseline := adopterBaseline(c, dir)
	head := cloneHead(c, s)
	if baseline.Version != professor.BaselineVersion || baseline.Blueprint.SHA != head || len(baseline.Files) != count {
		c.Failf(
			"baseline.json: version %d, blueprint %s, %d pins; want version %d, blueprint %s (the clone's HEAD), %d pins (pfm init's count)",
			baseline.Version,
			baseline.Blueprint.SHA,
			len(baseline.Files),
			professor.BaselineVersion,
			head,
			count,
		)
	}
	var present []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		switch {
		case entry.IsDir() && rel == ".git":
			return filepath.SkipDir
		case entry.IsDir(), rel == "README.md", rel == rumdlLocal, rel == ".professor/baseline.json":
			return nil
		}
		present = append(present, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		c.Failf("walk %s: %v", dir, err)
	}
	pinned := make([]string, 0, len(baseline.Files))
	for local, pin := range baseline.Files {
		pinned = append(pinned, local)
		want := templateHash(c, s, pin.Template)
		if pin.PinnedSHA != head || pin.TemplateHash != want {
			c.Failf("%s's pin = %+v, want pinnedSha %s and templateHash %s (templates/%s in the clone)",
				local, pin, head, want, pin.Template)
		}
	}
	slices.Sort(present)
	slices.Sort(pinned)
	if !slices.Equal(present, pinned) {
		c.Failf("the files pfm init wrote into %s:\n%s\nthe files .professor/baseline.json pins:\n%s",
			dir, strings.Join(present, "\n"), strings.Join(pinned, "\n"))
	}
	for file, want := range map[string]string{"README.md": adopterReadme, rumdlLocal: adopterRumdl} {
		if got := read(c, "cat", filepath.Join(dir, file)); got != want {
			c.Failf("%s after pfm init = %q, want the owner's %q", file, got, want)
		}
	}
	s.Project = dir
	return s
}

// adopterBaseline is the project's .professor/baseline.json.
func adopterBaseline(c *harness.Ctx, dir string) professor.Baseline {
	file := filepath.Join(dir, ".professor", "baseline.json")
	raw := read(c, "cat", file)
	var baseline professor.Baseline
	if err := json.Unmarshal([]byte(raw), &baseline); err != nil {
		c.Failf("%s: %v:\n%s", file, err, raw)
	}
	return baseline
}

// professorClone is the clone INSTALL.md made, the store pfm init scaffolds from.
func professorClone(s State) string { return filepath.Join(s.Home, ".professor") }

// cloneHead is the clone's HEAD, abbreviated as a pin records it.
func cloneHead(c *harness.Ctx, s State) string {
	return strings.TrimSpace(read(c, "git", "-C", professorClone(s), "rev-parse", "--short", "HEAD"))
}

// templateHash is the clone's template as a pin hashes it.
func templateHash(c *harness.Ctx, s State, template string) string {
	file := filepath.Join(professorClone(s), "templates", filepath.FromSlash(template))
	raw, err := os.ReadFile(file)
	if err != nil {
		c.Failf("read %s: %v", file, err)
	}
	return fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
}
