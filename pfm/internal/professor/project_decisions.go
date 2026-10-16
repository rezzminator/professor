package professor

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/pyvalue"
)

// projectKept is a pin whose file the session deliberately left apart from
// the upstream change the pin now acknowledges (`pfm update pin --keep`). It
// is a recorded decision, never review.
const projectKept projectStatus = "KEPT"

// RetiredAccepted is one recorded rule and the hits and incomplete coverage
// it accepts; a rule covering neither is stale and says so.
type RetiredAccepted struct {
	Rule       RetiredRule          `json:"rule"`
	Hits       int                  `json:"hits"`
	Incomplete []RetiredNameFailure `json:"incomplete,omitempty"`
}

// keepPin is pin acknowledged at the store's current template with the
// session's reason recorded. The deferred change starts at the earliest
// commit still untaken: a pin kept before keeps its FromSHA.
func keepPin(pin FilePin, hash, storeSHA, reason, today string) FilePin {
	from := pin.PinnedSHA
	if pin.Kept != nil && pin.Kept.FromSHA != "" {
		from = pin.Kept.FromSHA
	}
	pin.Kept = &KeptNote{Reason: reason, FromSHA: from, At: today}
	pin.TemplateHash = hash
	pin.PinnedSHA = storeSHA
	pin.PinnedAt = today
	return pin
}

// partitionRetired moves every hit a recorded rule covers out of scan.Hits
// into scan.Accepted's counts. An accepted hit is a decision, so it no longer
// counts as review. Each rule counts every hit it covers, so a narrower rule
// inside a wider one is never reported as matching nothing; AcceptedHits
// counts each accepted hit once. Path-wide rules also accept incomplete coverage,
// preserving its diagnostic on the rule. Failures are never partitioned.
func partitionRetired(scan *RetiredNameScan, rules []RetiredRule) {
	if len(rules) == 0 {
		return
	}
	counts := make([]int, len(rules))
	partials := make([][]RetiredNameFailure, len(rules))
	open := make([]RetiredNameHit, 0, len(scan.Hits))
	for _, hit := range scan.Hits {
		accepted := false
		for index, rule := range rules {
			if rule.Covers(hit.Path, hit.Name) {
				counts[index]++
				accepted = true
			}
		}
		if accepted {
			scan.AcceptedHits++
		} else {
			open = append(open, hit)
		}
	}
	scan.Hits = open
	incomplete := make([]RetiredNameFailure, 0, len(scan.Incomplete))
	for _, partial := range scan.Incomplete {
		accepted := false
		for index, rule := range rules {
			// A named rule says nothing about names in unscanned text.
			if rule.Covers(partial.Path, "") {
				partials[index] = append(partials[index], partial)
				accepted = true
			}
		}
		if !accepted {
			incomplete = append(incomplete, partial)
		}
	}
	scan.Incomplete = incomplete
	scan.Accepted = make([]RetiredAccepted, len(rules))
	for index, rule := range rules {
		scan.Accepted[index] = RetiredAccepted{Rule: rule, Hits: counts[index], Incomplete: partials[index]}
	}
}

// retiredScanPinned is the set of local files the retired-name scan skips: the
// ones whose report row already covers them. A KEPT file is a current pin, so
// its own edits are scanned like any current file's.
func retiredScanPinned(report *projectReport) map[string]bool {
	pinned := make(map[string]bool, len(report.Items))
	for index := range report.Items {
		item := report.Items[index]
		if item.Local != "" && item.Status != projectKept {
			pinned[item.Local] = true
		}
	}
	return pinned
}

func writeKeptHuman(stdout io.Writer, r projectReport) {
	if r.Counts[projectKept] == 0 {
		return
	}
	fmt.Fprintf(
		stdout,
		"  %-13s %d   (deliberate local divergence, recorded; not review)\n",
		projectKept,
		r.Counts[projectKept],
	)
	for index := range r.Items {
		item := &r.Items[index]
		if item.Status != projectKept {
			continue
		}
		fmt.Fprintf(
			stdout,
			"    %s   %s  kept %s: %s\n",
			item.Local,
			item.Template,
			pyvalue.OneLine(item.Pin.Kept.At),
			pyvalue.OneLine(item.Pin.Kept.Reason),
		)
		if gitObjectName.MatchString(item.Pin.Kept.FromSHA) && gitObjectName.MatchString(item.Pin.PinnedSHA) {
			fmt.Fprintf(
				stdout,
				"      untaken upstream change: git -C %s diff %s %s -- templates/%s\n",
				r.Store.Root, item.Pin.Kept.FromSHA, item.Pin.PinnedSHA, item.Template,
			)
		} else {
			fmt.Fprintln(
				stdout,
				"      untaken upstream change unavailable: no recorded git history; compare the current template and local file by hand",
			)
			fmt.Fprintf(stdout, "      review: diff -u %s %s\n",
				shellWord(filepath.Join(r.Store.Root, "templates", filepath.FromSlash(item.Template))),
				shellWord(filepath.Join(r.Root, filepath.FromSlash(item.Local))))
		}
		fmt.Fprintf(stdout, "      ported the untaken change after all? pfm update pin %s --unkeep\n", item.Local)
	}
}

func writeAcceptedHuman(stdout io.Writer, scan *RetiredNameScan) {
	if scan == nil || len(scan.Accepted) == 0 {
		return
	}
	fmt.Fprintf(
		stdout, "  %-13s %d   (retired-name hits accepted by %d recorded rule(s); not review)\n",
		"ACCEPTED", scan.AcceptedHits, len(scan.Accepted),
	)
	for _, accepted := range scan.Accepted {
		name := "any retired name"
		if accepted.Rule.Name != "" {
			name = accepted.Rule.Name
		}
		stale := ""
		if accepted.Hits == 0 && len(accepted.Incomplete) == 0 {
			stale = " — matches nothing now: pfm update accept --undo " + shellWord(accepted.Rule.Path)
			if accepted.Rule.Name != "" {
				stale += " --name " + shellWord(accepted.Rule.Name)
			}
		}
		fmt.Fprintf(
			stdout,
			"    %s   %s   %d hit(s) — %s%s\n",
			pyvalue.OneLine(accepted.Rule.Path),
			pyvalue.OneLine(name),
			accepted.Hits,
			pyvalue.OneLine(accepted.Rule.Reason),
			stale,
		)
		for _, partial := range accepted.Incomplete {
			fmt.Fprintf(stdout, "      accepted incomplete coverage: %s — %s\n",
				pyvalue.OneLine(partial.Path), pyvalue.OneLine(partial.Error))
		}
	}
}

// writeSessionGuide is the report's own close-out contract. It is printed
// whenever rows are open, so a session that starts from any prompt still
// reads what ends each row. Each line leads with "·", never a bare status
// word: a reader that takes an indented first field after a status header as
// a row (infra/fence/lanes/adopt.sh) must not read the guide as rows.
func writeSessionGuide(stdout io.Writer) {
	fmt.Fprintln(
		stdout,
		"  every row ends in a verb; re-run this report until it ends `clean` — a row left open is not a decision:",
	)
	fmt.Fprintln(stdout, "    · UPDATED         port the intent of each upstream commit, then pfm update pin <local>; "+
		"keeping your version on purpose: pfm update pin <local> --keep \"<what you did not take, and why>\"")
	fmt.Fprintln(
		stdout,
		"    · NEW             adopt, then pfm update pin --template <template> <local>; not for this project: pfm update ignore <template>",
	)
	fmt.Fprintln(stdout, "    · GONE-UPSTREAM   pfm update drop <local>")
	fmt.Fprintln(
		stdout,
		"    · LOCAL-DELETED   restore the file, or pfm update drop <local> then pfm update ignore <template> (a dropped pin's template reads NEW); MOVED: the verbs on the row",
	)
	fmt.Fprintln(stdout, "    · RETIRED-NAME    edit the line to its successor; a mention that must stay: "+
		"pfm update accept <path> --reason \"<why>\" [--name <name>]")
	fmt.Fprintln(stdout, "    · INCOMPLETE      inspect the unscanned text; accept reviewed coverage: "+
		"pfm update accept <path> --reason \"<why>\" (no --name); read failures must be fixed")
	fmt.Fprintln(stdout, "  run the verbs one at a time: each rewrites .professor/baseline.json")
}

func runProjectAccept(
	args []string,
	stdout, stderr io.Writer,
	runtime config.Runtime,
	scan RetiredNameScanner,
) int {
	flags := cli.NewFlagSet(
		"update accept",
		"usage: pfm update accept <path>... --reason TEXT [--name RETIRED-NAME] [--undo] [--root DIR]",
		stderr,
	)
	rootFlag := flags.String("root", "", "project root")
	reason := flags.String("reason", "", "why these mentions or incomplete coverage are accepted (required)")
	name := flags.String("name", "", "accept only this retired name at the path")
	undo := flags.Bool("undo", false, "remove the recorded rule")
	paths, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	text := strings.TrimSpace(*reason)
	if len(paths) == 0 || (!*undo && text == "") {
		flags.Usage()
		return 2
	}
	root, found, err := ResolveProjectRoot(*rootFlag)
	if err != nil || !found {
		if err == nil {
			err = errBaselineNotFound
		}
		fmt.Fprintf(stderr, "pfm update accept: %v\n", err)
		return 1
	}
	report, err := buildProjectReport(root, runtime.Paths.Home)
	if err != nil {
		fmt.Fprintf(stderr, "pfm update accept: %v\n", err)
		return 1
	}
	rules := report.Baseline.RetiredAccepted
	var hits []RetiredNameHit
	var incomplete []RetiredNameFailure
	if !*undo {
		if scan == nil {
			fmt.Fprintln(
				stderr,
				"pfm update accept: the retired-name scan is unavailable here, so no hit can be checked",
			)
			return 1
		}
		result := scan(report.Root, retiredScanPinned(&report))
		if len(result.Failures) != 0 {
			fmt.Fprintf(stderr, "pfm update accept: %d file(s) could not be scanned (%s); fix that first\n",
				len(result.Failures), result.Failures[0].Path)
			return 1
		}
		hits = result.Hits
		incomplete = result.Incomplete
	}
	changed := 0
	for _, value := range paths {
		path, err := safeProjectRelative(value)
		if err != nil {
			fmt.Fprintf(stderr, "pfm update accept: %v\n", err)
			return 1
		}
		if *undo {
			kept := make([]RetiredRule, 0, len(rules))
			for _, rule := range rules {
				if rule.Path == path && rule.Name == *name {
					changed++
					continue
				}
				kept = append(kept, rule)
			}
			if len(kept) == len(rules) {
				fmt.Fprintf(stderr, "pfm update accept: no recorded rule for %s\n", path)
				return 1
			}
			rules = kept
			continue
		}
		rule := RetiredRule{Path: path, Name: *name, Reason: text}
		covered := 0
		for _, hit := range hits {
			if rule.Covers(hit.Path, hit.Name) {
				covered++
			}
		}
		for _, partial := range incomplete {
			if rule.Covers(partial.Path, "") {
				covered++
			}
		}
		if covered == 0 {
			fmt.Fprintf(
				stderr,
				"pfm update accept: %s covers no retired-name hit or incomplete coverage; nothing to accept\n",
				path,
			)
			return 1
		}
		rules = append(rules, rule)
		changed++
	}
	report.Baseline.RetiredAccepted = rules
	if err := Save(root, report.Baseline); err != nil {
		fmt.Fprintf(stderr, "pfm update accept: %v\n", err)
		return 1
	}
	if *undo {
		fmt.Fprintf(stdout, "removed %d retired-name rule(s)\n", changed)
	} else {
		fmt.Fprintf(stdout, "accepted retired-name hits or incomplete coverage under %d path(s)\n", changed)
	}
	return 0
}

// keepSelected applies --keep to the named UPDATED locals: each pin
// advances like `pfm update pin` and records the reason. It returns the
// locals changed, or the first refusal.
func keepSelected(report *projectReport, locals []string, reason string) ([]string, error) {
	today := clock.Real.Now().Format(time.DateOnly)
	updated := make(map[string]bool, len(report.Items))
	for index := range report.Items {
		if report.Items[index].Status == projectUpdated {
			updated[report.Items[index].Local] = true
		}
	}
	kept := make([]string, 0, len(locals))
	for _, value := range locals {
		local, err := safeProjectRelative(value)
		if err != nil {
			return nil, err
		}
		pin, exists := report.Baseline.Files[local]
		switch {
		case !exists:
			return nil, fmt.Errorf("%s has no pin", local)
		case !updated[local]:
			return nil, fmt.Errorf("%s is not UPDATED: there is no upstream change to keep apart from", local)
		}
		hash, err := HashTemplate(filepath.Join(report.Store.Templates, filepath.FromSlash(pin.Template)))
		if err != nil {
			return nil, err
		}
		report.Baseline.Files[local] = keepPin(pin, hash, report.Store.SHA, reason, today)
		kept = append(kept, local)
	}
	sort.Strings(kept)
	return kept, nil
}

// addKept records a CURRENT pin carrying a keep as a KEPT row beside its
// current count.
func (r *projectReport) addKept(status projectStatus, local, template string, pin FilePin) {
	if status != projectCurrent || pin.Kept == nil {
		return
	}
	r.Counts[projectKept]++
	r.Items = append(r.Items, projectReportItem{Status: projectKept, Local: local, Template: template, Pin: pin})
}

// runProjectKeep is `pfm update pin <local>... --keep REASON`: the named
// UPDATED files advance like a plain pin with the reason on record.
func runProjectKeep(
	report *projectReport,
	root string,
	locals []string,
	reason string,
	stdout, stderr io.Writer,
) int {
	kept, err := keepSelected(report, locals, reason)
	if err == nil {
		report.Baseline.Blueprint = BlueprintPin{Version: report.Store.Version, SHA: report.Store.SHA}
		err = Save(root, report.Baseline)
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm update pin: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "kept %d file(s) apart from upstream at %s: %s\n", len(kept), report.Store.SHA, reason)
	return 0
}

func writeUpdatedCommits(stdout io.Writer, item projectReportItem) {
	if len(item.Commits) == 0 {
		return
	}
	fmt.Fprintln(stdout, "      upstream commits that changed it since the pin, oldest first — port each one's intent:")
	for _, commit := range item.Commits {
		fmt.Fprintf(stdout, "        %s\n", commit)
	}
}

func writeKeptEarlier(stdout io.Writer, item projectReportItem) {
	if item.Pin.Kept == nil {
		return
	}
	provenance := "untaken change has no recorded git history"
	if gitObjectName.MatchString(item.Pin.Kept.FromSHA) {
		provenance = "untaken since " + item.Pin.Kept.FromSHA
	}
	fmt.Fprintf(
		stdout,
		"      kept earlier (%s): %s — %s\n",
		pyvalue.OneLine(item.Pin.Kept.At),
		pyvalue.OneLine(item.Pin.Kept.Reason),
		provenance,
	)
}

// writePortInstruction closes an UPDATED row with its two ways out.
func writePortInstruction(stdout io.Writer, local string) {
	fmt.Fprintf(
		stdout,
		"      port what applies into %s, keep the project's own edits, then: pfm update pin %s\n",
		local, local,
	)
	fmt.Fprintf(
		stdout,
		"      or keep your version on purpose, on record: pfm update pin %s --keep \"<what you did not take, and why>\"\n",
		local,
	)
}

// pinUsageInvalid reports a `pfm update pin` argument mix no verb form allows.
// keepGiven is whether --keep appeared at all: `--keep ""` is a keep with no
// reason, refused, never a plain pin.
func pinUsageInvalid(locals []string, pinAll, unkeep bool, template string, keepGiven bool, keepReason string) bool {
	return (pinAll && (len(locals) != 0 || template != "")) || (template != "" && len(locals) != 1) ||
		(!pinAll && template == "" && len(locals) == 0) ||
		(keepGiven && (keepReason == "" || pinAll || template != "" || len(locals) != 1 || unkeep)) ||
		(unkeep && (pinAll || template != ""))
}

// flagGiven reports whether the named flag appeared on the command line,
// whatever its value.
func flagGiven(flags *flag.FlagSet, name string) bool {
	given := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			given = true
		}
	})
	return given
}

// dropKeep clears a pin's recorded keep: its untaken change was ported after
// all. A pin with no keep is an error, never a silent no-op.
func dropKeep(pin *FilePin, local string) error {
	if pin.Kept == nil {
		return fmt.Errorf("%s has no recorded keep to drop", local)
	}
	pin.Kept = nil
	return nil
}

// shellWord is value as one POSIX-shell word for a printed command line: bare
// when it holds only safe bytes, else single-quoted with a newline written as
// \n, so a recorded path never splits or forges a report line.
func shellWord(value string) string {
	safe := value != ""
	for _, b := range []byte(value) {
		if !isRetiredSafeByte(b) {
			safe = false
			break
		}
	}
	if safe {
		return value
	}
	value = strings.NewReplacer("\n", "\\n", "\r", "\\r").Replace(value)
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func isRetiredSafeByte(b byte) bool {
	return b == '/' || b == '.' || b == '_' || b == '-' || b == ':' ||
		('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9')
}
