// Package professor is the blueprint's template store: it scaffolds a project once, pins its baseline, reports drift against the store, and prints the project-side doctor row.
package professor

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
)

type projectStatus string

const (
	projectCurrent         projectStatus = "current"
	projectIgnored         projectStatus = "ignored"
	projectUpdated         projectStatus = "UPDATED"
	projectNew             projectStatus = "NEW"
	projectGoneUpstream    projectStatus = "GONE-UPSTREAM"
	projectLocalDeleted    projectStatus = "LOCAL-DELETED"
	missingBaselineMessage               = ".professor/baseline.json not found — pfm update adopt pins an existing install; pfm init scaffolds a new one"
	emptySummary                         = "none"
)

var (
	projectStatusOrder = []projectStatus{
		projectCurrent,
		projectIgnored,
		projectUpdated,
		projectNew,
		projectGoneUpstream,
		projectLocalDeleted,
	}
	errBaselineNotFound = errors.New(missingBaselineMessage)
)

type projectReportItem struct {
	Status   projectStatus `json:"status"`
	Local    string        `json:"local,omitempty"`
	Template string        `json:"template"`
	Pin      FilePin       `json:"pin,omitempty"`
}
type projectReport struct {
	Root     string
	Store    Store
	Baseline Baseline
	Counts   map[projectStatus]int
	Items    []projectReportItem
}
type projectTerminalEnvelope struct {
	Error    string `json:"error,omitempty"`
	Terminal string `json:"terminal"`
}

func (r projectReport) reviewRequired() int {
	return r.Counts[projectUpdated] + r.Counts[projectNew] + r.Counts[projectGoneUpstream] + r.Counts[projectLocalDeleted]
}

func renderProjectCheck(root, home string, jsonOutput bool, stdout io.Writer) int {
	report, err := buildProjectReport(root, home)
	if err != nil {
		writeProjectFailure(stdout, jsonOutput, err)
		return 1
	}
	if jsonOutput {
		if err := writeProjectJSON(stdout, report); err != nil {
			writeProjectFailure(stdout, false, err)
			return 1
		}
	} else {
		writeProjectHuman(stdout, report)
	}
	if report.reviewRequired() != 0 {
		return 3
	}
	return 0
}

func buildProjectReport(root, home string) (projectReport, error) {
	baseline, err := Load(root)
	if err != nil {
		return projectReport{}, err
	}
	store, err := ResolveStore(root, home)
	if err != nil {
		return projectReport{}, err
	}
	report := projectReport{
		Root:     root,
		Store:    store,
		Baseline: baseline,
		Counts:   make(map[projectStatus]int, len(projectStatusOrder)),
	}
	for _, s := range projectStatusOrder {
		report.Counts[s] = 0
	}
	pinned := make(map[string]bool, len(baseline.Files))
	ignored := make(map[string]bool, len(baseline.Ignored))
	for _, v := range baseline.Ignored {
		ignored[v] = true
	}
	locals := make([]string, 0, len(baseline.Files))
	for local := range baseline.Files {
		locals = append(locals, local)
	}
	sort.Strings(locals)
	for _, rawLocal := range locals {
		pin := baseline.Files[rawLocal]
		local, err := safeProjectRelative(rawLocal)
		if err != nil {
			return projectReport{}, fmt.Errorf("baseline local %q: %w", rawLocal, err)
		}
		template, err := safeTemplateRelative(pin.Template)
		if err != nil {
			return projectReport{}, fmt.Errorf("baseline template %q: %w", pin.Template, err)
		}
		pinned[template] = true
		templatePath := filepath.Join(store.Templates, filepath.FromSlash(template))
		status := projectCurrent
		if _, err := os.Stat(templatePath); errors.Is(err, fs.ErrNotExist) {
			status = projectGoneUpstream
		} else if err != nil {
			return projectReport{}, fmt.Errorf("UNREADABLE %s: %w", templatePath, err)
		} else {
			localPath := filepath.Join(root, filepath.FromSlash(local))
			if _, err := os.Stat(localPath); errors.Is(err, fs.ErrNotExist) {
				status = projectLocalDeleted
			} else if err != nil {
				return projectReport{}, fmt.Errorf("UNREADABLE %s: %w", localPath, err)
			} else {
				hash, err := HashTemplate(templatePath)
				if err != nil {
					return projectReport{}, err
				}
				if hash != pin.TemplateHash {
					status = projectUpdated
				}
			}
		}
		report.Counts[status]++
		if status != projectCurrent {
			report.Items = append(
				report.Items,
				projectReportItem{Status: status, Local: local, Template: template, Pin: pin},
			)
		}
	}
	projectTemplates := filepath.Join(store.Templates, "project")
	if err := filepath.WalkDir(projectTemplates, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("UNREADABLE %s: %w", path, walkErr)
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("UNREADABLE %s: symlink templates are unsupported", path)
		}
		relative, err := filepath.Rel(store.Templates, path)
		if err != nil {
			return err
		}
		template := filepath.ToSlash(relative)
		if pinned[template] {
			return nil
		}
		if ignored[template] {
			report.Counts[projectIgnored]++
			return nil
		}
		if _, err := HashTemplate(path); err != nil {
			return err
		}
		report.Counts[projectNew]++
		report.Items = append(report.Items, projectReportItem{Status: projectNew, Template: template})
		return nil
	}); err != nil {
		return projectReport{}, err
	}
	sort.Slice(report.Items, func(i, j int) bool {
		if report.Items[i].Status != report.Items[j].Status {
			return statusIndex(report.Items[i].Status) < statusIndex(report.Items[j].Status)
		}
		if report.Items[i].Local != report.Items[j].Local {
			return report.Items[i].Local < report.Items[j].Local
		}
		return report.Items[i].Template < report.Items[j].Template
	})
	return report, nil
}

func statusIndex(status projectStatus) int {
	for i, s := range projectStatusOrder {
		if status == s {
			return i
		}
	}
	return len(projectStatusOrder)
}

func writeProjectHuman(stdout io.Writer, r projectReport) {
	fmt.Fprintf(stdout, "professor: %s  blueprint %s → %s\n", r.Root, r.Baseline.Blueprint.SHA, r.Store.SHA)
	for _, status := range projectStatusOrder {
		fmt.Fprintf(stdout, "  %-13s %d\n", status, r.Counts[status])
		for _, item := range r.Items {
			if item.Status != status {
				continue
			}
			switch status {
			case projectUpdated:
				fmt.Fprintf(stdout, "    %s   %s  pinned @%s\n", item.Local, item.Template, item.Pin.PinnedSHA)
				if item.Pin.PinnedSHA == UnknownSelfHostedSHA || r.Store.SHA == UnknownSelfHostedSHA {
					fmt.Fprintf(
						stdout,
						"      review: self-hosted pin or store — no git history, so an exact %s..%s diff cannot run; "+
							"comparing the current template against your local file instead:\n",
						item.Pin.PinnedSHA,
						r.Store.SHA,
					)
					fmt.Fprintf(
						stdout,
						"      review: diff %s %s\n",
						filepath.Join(r.Root, filepath.FromSlash(item.Local)),
						filepath.Join(r.Store.Templates, filepath.FromSlash(item.Template)),
					)
				} else {
					fmt.Fprintf(
						stdout,
						"      review: git -C %s diff %s..%s -- templates/%s\n",
						r.Store.Root,
						item.Pin.PinnedSHA,
						r.Store.SHA,
						item.Template,
					)
				}
				fmt.Fprintf(stdout, "      then apply by hand and: pfm update pin %s\n", item.Local)
			case projectNew:
				fmt.Fprintf(
					stdout,
					"    %s — adopt: copy/adapt it locally, then pfm update pin --template %s <local> — or ignore\n",
					item.Template,
					item.Template,
				)
			case projectGoneUpstream:
				fmt.Fprintf(
					stdout,
					"    %s   %s — retired upstream — delete it and pfm update drop %s; keep it and drop only its pin if the project still uses it\n",
					item.Local,
					item.Template,
					item.Local,
				)
			case projectLocalDeleted:
				fmt.Fprintf(
					stdout,
					"    %s   %s — pfm update drop %s to forget, or restore the file\n",
					item.Local,
					item.Template,
					item.Local,
				)
			}
		}
	}
	if n := r.reviewRequired(); n != 0 {
		fmt.Fprintf(stdout, "REVIEW REQUIRED — %d items; nothing was written.\n", n)
	} else {
		fmt.Fprintln(stdout, "clean")
	}
}

func writeProjectJSON(stdout io.Writer, r projectReport) error {
	type blueprintJSON struct {
		Pinned  string `json:"pinned"`
		Current string `json:"current"`
	}
	payload := struct {
		Professor      string                `json:"professor"`
		Blueprint      blueprintJSON         `json:"blueprint"`
		Counts         map[projectStatus]int `json:"counts"`
		Items          []projectReportItem   `json:"items"`
		Ignored        []string              `json:"ignored"`
		ReviewRequired int                   `json:"reviewRequired"`
		Terminal       string                `json:"terminal"`
	}{r.Root, blueprintJSON{r.Baseline.Blueprint.SHA, r.Store.SHA}, r.Counts, r.Items, r.Baseline.Ignored, r.reviewRequired(), ""}
	if payload.ReviewRequired == 0 {
		payload.Terminal = "clean"
	} else {
		payload.Terminal = fmt.Sprintf("REVIEW REQUIRED — %d items", payload.ReviewRequired)
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return fmt.Errorf("encode project report: %w", err)
	}
	return nil
}

func writeProjectFailure(stdout io.Writer, jsonOutput bool, err error) {
	terminal := "FAILED — " + err.Error()
	if jsonOutput {
		if encodeErr := json.NewEncoder(stdout).
			Encode(projectTerminalEnvelope{Error: err.Error(), Terminal: terminal}); encodeErr != nil {
			fmt.Fprintf(stdout, "FAILED — encode project error: %v\n", encodeErr)
		}
		return
	}
	fmt.Fprintln(stdout, terminal)
}

// RunPostUpdate reports project drift after a successful binary update. No baseline is a successful NOT-MANAGED terminal.
func runPostUpdate(rootFlag string, jsonOutput bool, stdout io.Writer, runtime config.Runtime) int {
	root, found, err := resolveProjectRoot(rootFlag)
	if err != nil {
		writeProjectFailure(stdout, jsonOutput, err)
		return 1
	}
	if found {
		return renderProjectCheck(root, runtime.Paths.Home, jsonOutput, stdout)
	}
	writeProjectUnmanaged(stdout, jsonOutput)
	return 0
}

// writeProjectUnmanaged prints the successful post-update terminal for a source clone outside a managed project.
func writeProjectUnmanaged(stdout io.Writer, jsonOutput bool) {
	terminal := "NOT-MANAGED — no .professor/baseline.json at or above this directory (expected in the Professor source clone); run `pfm update check` inside each adopted project"
	if jsonOutput {
		if err := json.NewEncoder(stdout).Encode(projectTerminalEnvelope{Terminal: terminal}); err != nil {
			fmt.Fprintf(stdout, "NOT-MANAGED — encode project report: %v\n", err)
		}
		return
	}
	fmt.Fprintln(stdout, terminal)
}

func runProjectPin(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet(
		"update pin",
		"usage: pfm update pin <local>... | --all [--template TEMPLATE] [--root DIR]",
		stderr,
	)
	rootFlag := flags.String("root", "", "project root")
	pinAll := flags.Bool("all", false, "pin every UPDATED file")
	templateFlag := flags.String("template", "", "template path for a newly adopted local file")
	locals, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if (*pinAll && (len(locals) != 0 || *templateFlag != "")) || (*templateFlag != "" && len(locals) != 1) ||
		(!*pinAll && *templateFlag == "" && len(locals) == 0) {
		flags.Usage()
		return 2
	}
	root, found, err := resolveProjectRoot(*rootFlag)
	if err != nil || !found {
		if err == nil {
			err = errBaselineNotFound
		}
		fmt.Fprintf(stderr, "pfm update pin: %v\n", err)
		return 1
	}
	report, err := buildProjectReport(root, runtime.Paths.Home)
	if err != nil {
		fmt.Fprintf(stderr, "pfm update pin: %v\n", err)
		return 1
	}
	selected := locals
	if *pinAll {
		selected = nil
		for _, item := range report.Items {
			if item.Status == projectUpdated {
				selected = append(selected, item.Local)
			}
		}
	}
	if *templateFlag != "" {
		template, err := safeTemplateRelative(*templateFlag)
		if err != nil {
			fmt.Fprintf(stderr, "pfm update pin: %v\n", err)
			return 1
		}
		local, err := safeProjectRelative(selected[0])
		if err != nil {
			fmt.Fprintf(stderr, "pfm update pin: %v\n", err)
			return 1
		}
		if _, exists := report.Baseline.Files[local]; exists {
			fmt.Fprintf(stderr, "pfm update pin: %s already has a pin\n", local)
			return 1
		}
		if err := requireReadableLocal(root, local); err != nil {
			fmt.Fprintf(stderr, "pfm update pin: %v\n", err)
			return 1
		}
		hash, err := HashTemplate(filepath.Join(report.Store.Templates, filepath.FromSlash(template)))
		if err != nil {
			fmt.Fprintf(stderr, "pfm update pin: %v\n", err)
			return 1
		}
		report.Baseline.Files[local] = FilePin{
			Template:     template,
			TemplateHash: hash,
			PinnedSHA:    report.Store.SHA,
			PinnedAt:     clock.Real.Now().Format(time.DateOnly),
		}
		report.Baseline.Ignored = removeIgnored(report.Baseline.Ignored, template)
		selected[0] = local
	} else {
		for i, value := range selected {
			local, err := safeProjectRelative(value)
			if err != nil {
				fmt.Fprintf(stderr, "pfm update pin: %v\n", err)
				return 1
			}
			pin, exists := report.Baseline.Files[local]
			if !exists {
				fmt.Fprintf(stderr, "pfm update pin: %s has no pin; use --template for a new adoption\n", local)
				return 1
			}
			if err := requireReadableLocal(root, local); err != nil {
				fmt.Fprintf(stderr, "pfm update pin: %v\n", err)
				return 1
			}
			hash, err := HashTemplate(filepath.Join(report.Store.Templates, filepath.FromSlash(pin.Template)))
			if err != nil {
				fmt.Fprintf(stderr, "pfm update pin: %v\n", err)
				return 1
			}
			pin.TemplateHash = hash
			pin.PinnedSHA = report.Store.SHA
			pin.PinnedAt = clock.Real.Now().Format(time.DateOnly)
			report.Baseline.Files[local] = pin
			selected[i] = local
		}
	}
	if len(selected) != 0 {
		report.Baseline.Blueprint = BlueprintPin{Version: report.Store.Version, SHA: report.Store.SHA}
		if err := Save(root, report.Baseline); err != nil {
			fmt.Fprintf(stderr, "pfm update pin: %v\n", err)
			return 1
		}
	}
	fmt.Fprintf(stdout, "pinned %d file(s) at %s\n", len(selected), report.Store.SHA)
	return 0
}

func runProjectDrop(args []string, stdout, stderr io.Writer) int {
	flags := cli.NewFlagSet("update drop", "usage: pfm update drop <local>... [--root DIR]", stderr)
	rootFlag := flags.String("root", "", "project root")
	locals, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(locals) == 0 {
		flags.Usage()
		return 2
	}
	root, found, err := resolveProjectRoot(*rootFlag)
	if err != nil || !found {
		if err == nil {
			err = errBaselineNotFound
		}
		fmt.Fprintf(stderr, "pfm update drop: %v\n", err)
		return 1
	}
	baseline, err := Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "pfm update drop: %v\n", err)
		return 1
	}
	for i, value := range locals {
		local, err := safeProjectRelative(value)
		if err != nil {
			fmt.Fprintf(stderr, "pfm update drop: %v\n", err)
			return 1
		}
		if _, exists := baseline.Files[local]; !exists {
			fmt.Fprintf(stderr, "pfm update drop: %s has no pin\n", local)
			return 1
		}
		delete(baseline.Files, local)
		locals[i] = local
	}
	if err := Save(root, baseline); err != nil {
		fmt.Fprintf(stderr, "pfm update drop: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "dropped %d pin(s)\n", len(locals))
	return 0
}

func runProjectAdopt(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet("update adopt", "usage: pfm update adopt [--root DIR] [--at REF]", stderr)
	rootFlag := flags.String("root", "", "project root")
	atFlag := flags.String("at", "", "blueprint ref to pin against (defaults to the store HEAD)")
	positional, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(positional) != 0 {
		flags.Usage()
		return 2
	}
	root, found, err := resolveProjectRoot(*rootFlag)
	if err != nil {
		fmt.Fprintf(stderr, "pfm update adopt: %v\n", err)
		return 1
	}
	var baseline Baseline
	if found {
		baseline, err = Load(root)
		if err != nil {
			fmt.Fprintf(stderr, "pfm update adopt: %v\n", err)
			return 1
		}
	} else {
		start := strings.TrimSpace(*rootFlag)
		if start == "" {
			start, err = os.Getwd()
			if err != nil {
				fmt.Fprintf(stderr, "pfm update adopt: resolve current directory: %v\n", err)
				return 1
			}
		}
		root, err = filepath.Abs(start)
		if err != nil {
			fmt.Fprintf(stderr, "pfm update adopt: resolve project root %s: %v\n", start, err)
			return 1
		}
		baseline = Baseline{Version: BaselineVersion, Files: make(map[string]FilePin)}
	}
	store, err := ResolveStore(root, runtime.Paths.Home)
	if err != nil {
		fmt.Fprintf(stderr, "pfm update adopt: %v\n", err)
		return 1
	}
	plan, err := planInitCopies(store)
	if err != nil {
		fmt.Fprintf(stderr, "pfm update adopt: %v\n", err)
		return 1
	}
	ref := strings.TrimSpace(*atFlag)
	usingAt := ref != ""
	var sha, version string
	if usingAt {
		if store.SHA == UnknownSelfHostedSHA {
			fmt.Fprintf(stderr, "pfm update adopt: --at requires a git blueprint clone; %s has no .git\n", store.Root)
			return 1
		}
		shaOut, errText, gitErr := adoptGit(store.runner, store.Root, "rev-parse", "--short", ref+"^{commit}")
		if gitErr != nil {
			fmt.Fprintf(stderr, "pfm update adopt: %v\n", adoptGitFailure("resolve --at "+ref, gitErr, errText))
			return 1
		}
		sha = strings.TrimSpace(shaOut)
		versionOut, errText, gitErr := adoptGit(store.runner, store.Root, "show", ref+":VERSION")
		if gitErr != nil {
			fmt.Fprintf(
				stderr,
				"pfm update adopt: %v\n",
				adoptGitFailure(
					"resolve --at "+ref+": a blueprint ref without VERSION is not a blueprint",
					gitErr,
					errText,
				),
			)
			return 1
		}
		version = strings.TrimSpace(versionOut)
	} else {
		sha = store.SHA
		version = store.Version
	}
	kept, absent, absentAtRef, pinned := 0, 0, 0, 0
	pinnedAt := clock.Real.Now().Format(time.DateOnly)
	for _, entry := range plan {
		if _, exists := baseline.Files[entry.local]; exists {
			kept++
			continue
		}
		localPath := filepath.Join(root, filepath.FromSlash(entry.local))
		if _, statErr := os.Stat(localPath); errors.Is(statErr, fs.ErrNotExist) {
			absent++
			continue
		}
		if err := requireReadableLocal(root, entry.local); err != nil {
			fmt.Fprintf(stderr, "pfm update adopt: %v\n", err)
			return 1
		}
		var hash string
		if usingAt {
			raw, missing, gitErr := adoptGitShowTemplate(store.runner, store.Root, ref, entry.template)
			if gitErr != nil {
				fmt.Fprintf(stderr, "pfm update adopt: %v\n", gitErr)
				return 1
			}
			if missing {
				absentAtRef++
				continue
			}
			hash = fmt.Sprintf("sha256:%x", sha256.Sum256(raw))
		} else {
			hash, err = HashTemplate(entry.source)
			if err != nil {
				fmt.Fprintf(stderr, "pfm update adopt: %v\n", err)
				return 1
			}
		}
		baseline.Files[entry.local] = FilePin{
			Template:     entry.template,
			TemplateHash: hash,
			PinnedSHA:    sha,
			PinnedAt:     pinnedAt,
		}
		baseline.Ignored = removeIgnored(baseline.Ignored, entry.template)
		pinned++
	}
	if pinned > 0 {
		baseline.Blueprint = BlueprintPin{Version: version, SHA: sha}
	}
	if err := Save(root, baseline); err != nil {
		fmt.Fprintf(stderr, "pfm update adopt: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "professor: %s  blueprint %s\n", root, store.Root)
	if pinned > 0 {
		fmt.Fprintf(stdout, "adopted %d file(s) at %s\n", pinned, sha)
	} else {
		unchanged := baseline.Blueprint.SHA
		if unchanged == "" {
			unchanged = emptySummary
		}
		fmt.Fprintf(stdout, "adopted 0 file(s); blueprint pin unchanged (%s)\n", unchanged)
	}
	fmt.Fprintf(stdout, "  %-13s %-3d (%s)\n", "kept", kept, "already pinned, untouched")
	fmt.Fprintf(stdout, "  %-13s %-3d (%s)\n", "absent", absent, "mapped template, no local file — check reports NEW")
	if usingAt {
		fmt.Fprintf(
			stdout,
			"  %-13s %-3d (template did not exist at %s; check reports NEW)\n",
			"absent-at-ref",
			absentAtRef,
			ref,
		)
	}
	fmt.Fprintln(stdout, "next: pfm update check")
	return 0
}

func runProjectIgnore(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet("update ignore", "usage: pfm update ignore <template>... [--undo] [--root DIR]", stderr)
	rootFlag := flags.String("root", "", "project root")
	undo := flags.Bool("undo", false, "remove templates from the ignore list")
	values, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if len(values) == 0 {
		flags.Usage()
		return 2
	}
	root, found, err := resolveProjectRoot(*rootFlag)
	if err != nil || !found {
		if err == nil {
			err = errBaselineNotFound
		}
		fmt.Fprintf(stderr, "pfm update ignore: %v\n", err)
		return 1
	}
	baseline, err := Load(root)
	if err != nil {
		fmt.Fprintf(stderr, "pfm update ignore: %v\n", err)
		return 1
	}
	templates := make([]string, 0, len(values))
	for _, value := range values {
		template, err := safeTemplateRelative(value)
		if err != nil {
			fmt.Fprintf(stderr, "pfm update ignore: %v\n", err)
			return 1
		}
		templates = append(templates, template)
	}
	ignored := make(map[string]bool, len(baseline.Ignored))
	for _, template := range baseline.Ignored {
		ignored[template] = true
	}
	changed := 0
	if *undo {
		for _, template := range templates {
			if !ignored[template] {
				fmt.Fprintf(stderr, "pfm update ignore: %s is not ignored\n", template)
				return 1
			}
		}
		changed = len(templates)
		for _, template := range templates {
			delete(ignored, template)
		}
	} else {
		store, err := ResolveStore(root, runtime.Paths.Home)
		if err != nil {
			fmt.Fprintf(stderr, "pfm update ignore: %v\n", err)
			return 1
		}
		for _, template := range templates {
			templatePath := filepath.Join(store.Templates, filepath.FromSlash(template))
			info, statErr := os.Stat(templatePath)
			if errors.Is(statErr, fs.ErrNotExist) {
				fmt.Fprintf(stderr, "pfm update ignore: %s does not exist upstream\n", template)
				return 1
			} else if statErr != nil {
				fmt.Fprintf(stderr, "pfm update ignore: UNREADABLE %s: %v\n", templatePath, statErr)
				return 1
			}
			if info.IsDir() {
				fmt.Fprintf(stderr, "pfm update ignore: %s is a directory, not a template file\n", template)
				return 1
			}
			if locals := findPinsByTemplate(baseline, template); len(locals) != 0 {
				joined := strings.Join(locals, ", ")
				fmt.Fprintf(
					stderr,
					"pfm update ignore: %s is pinned by %s; pfm update drop %s first\n",
					template,
					joined,
					joined,
				)
				return 1
			}
		}
		for _, template := range templates {
			if !ignored[template] {
				changed++
			}
			ignored[template] = true
		}
	}
	result := make([]string, 0, len(ignored))
	for template := range ignored {
		result = append(result, template)
	}
	sort.Strings(result)
	baseline.Ignored = result
	if err := Save(root, baseline); err != nil {
		fmt.Fprintf(stderr, "pfm update ignore: %v\n", err)
		return 1
	}
	if *undo {
		fmt.Fprintf(stdout, "un-ignored %d template(s)\n", changed)
	} else {
		fmt.Fprintf(stdout, "ignored %d template(s)", changed)
		if already := len(templates) - changed; already > 0 {
			fmt.Fprintf(stdout, " (%d already ignored)", already)
		}
		fmt.Fprintln(stdout)
	}
	return 0
}

func findPinsByTemplate(b Baseline, template string) []string {
	var locals []string
	for local, pin := range b.Files {
		if pin.Template == template {
			locals = append(locals, local)
		}
	}
	sort.Strings(locals)
	return locals
}

func removeIgnored(ignored []string, template string) []string {
	if len(ignored) == 0 {
		return ignored
	}
	result := make([]string, 0, len(ignored))
	for _, value := range ignored {
		if value != template {
			result = append(result, value)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// PrintDoctor prints the project-side doctor row.
func PrintDoctor(stdout io.Writer, start, home string) int {
	root, found, err := resolveProjectRoot(start)
	if err != nil {
		fmt.Fprintf(stdout, "professor: UNREADABLE %v\n", err)
		return 1
	}
	if !found {
		return 0
	}
	report, err := buildProjectReport(root, home)
	if err != nil {
		fmt.Fprintf(stdout, "professor: UNREADABLE %v\n", err)
		return 1
	}
	reviewRequired := report.reviewRequired()
	fmt.Fprintf(
		stdout,
		"professor: current %d · review-required %d\n",
		report.Counts[projectCurrent],
		reviewRequired,
	)
	return reviewRequired
}

func resolveProjectRoot(rootFlag string) (string, bool, error) {
	start := strings.TrimSpace(rootFlag)
	if start == "" {
		var err error
		start, err = os.Getwd()
		if err != nil {
			return "", false, fmt.Errorf("resolve current directory: %w", err)
		}
	}
	absolute, err := filepath.Abs(start)
	if err != nil {
		return "", false, fmt.Errorf("resolve project root %s: %w", start, err)
	}
	for {
		path := BaselinePath(absolute)
		if _, err := os.Stat(path); err == nil {
			return absolute, true, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", false, fmt.Errorf("UNREADABLE %s: %w", path, err)
		}
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return "", false, nil
		}
		absolute = parent
	}
}

func safeProjectRelative(value string) (string, error) { return safeRelative(value, "local path", "") }

func safeTemplateRelative(value string) (string, error) {
	return safeRelative(value, "template path", "project/")
}

func safeRelative(value, label, prefix string) (string, error) {
	value = filepath.ToSlash(strings.TrimSpace(value))
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if value == "" || clean == "." || filepath.IsAbs(filepath.FromSlash(value)) || clean == ".." ||
		strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("invalid %s %q", label, value)
	}
	if prefix != "" && !strings.HasPrefix(clean, prefix) {
		return "", fmt.Errorf("invalid %s %q: expected %s**", label, value, prefix)
	}
	return clean, nil
}

func requireReadableLocal(root, local string) error {
	path := filepath.Join(root, filepath.FromSlash(local))
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("UNREADABLE %s: %w", path, err)
	}
	if info.IsDir() || info.Mode().Perm()&0o444 == 0 {
		return fmt.Errorf("UNREADABLE %s: %w", path, fs.ErrPermission)
	}
	return nil
}
