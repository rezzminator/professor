package doctor

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	harnessprompts "github.com/rezzminator/professor/pfm/harness-prompts"
	"github.com/rezzminator/professor/pfm/internal/installer"
)

// The harness-prompt tree exists once, in pfm/harness-prompts/, and the
// binary carries a COPY of it taken at build time. Those two drift the moment
// a template is edited: the clone has one prompt, every managed launch gets
// the one compiled into the installed binary, and nothing says so. This row
// says so — and it keeps its outcomes apart, because "the trees match", "the
// trees differ", "the clone's tree could not be read" and "this host has no
// blueprint clone at all" are four different facts, and only the first is a
// clean bill.
//
// A hash difference carries NO chronology. Equal bytes prove sameness;
// different bytes prove only difference — the clone may hold the newer
// prompts, or it may be checked out to a revision older than this binary was
// built from, in which case the binary is the newer one and rebuilding from
// that clone would replace newer prompts with older ones. The row therefore
// reports MISMATCH and states both directions, and never prescribes a rebuild
// as though it knew which side moved.

// harnessPromptEmbedReport is one comparison of the binary's embedded tree
// against the blueprint clone's. Err is set ONLY when the comparison could
// not be made; a comparison that ran and found differences reports them in
// Differing, never as an error.
type harnessPromptEmbedReport struct {
	Tree      string
	Files     int
	Differing []string
	Err       error
}

// printHarnessPromptEmbedDoctor resolves the blueprint clone the installer
// already links the machine-global templates out of, and compares its
// harness-prompt tree against the one this binary carries. It returns the
// warning count: trees that differ and a clone whose tree could not be read
// are each one warning, never a silent pass; a host with no clone is named
// and warns nothing, because there is nothing there to differ from.
func printHarnessPromptEmbedDoctor(stdout io.Writer, home string) int {
	clone, err := installer.GlobalSourceRepo(home)
	if err != nil {
		return printHarnessPromptEmbedReport(stdout, harnessPromptEmbedReport{
			Err: fmt.Errorf("resolve blueprint clone: %w", err),
		})
	}
	// pfm installs without the blueprint clone, so a host with none has no
	// tree to differ from — named, never warned, and never called ok either.
	// A clone that IS there but whose tree cannot be read is the opposite
	// case and belongs to inspectHarnessPromptEmbed's error.
	if _, err := os.Stat(clone); errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(
			stdout,
			"doctor: harness-prompts embed=no-clone repo=%s — no blueprint clone on this host, "+
				"so nothing was compared\n",
			clone,
		)
		return 0
	} else if err != nil {
		return printHarnessPromptEmbedReport(stdout, harnessPromptEmbedReport{
			Tree: clone,
			Err:  fmt.Errorf("inspect blueprint clone %s: %w", clone, err),
		})
	}
	return printHarnessPromptEmbedReport(
		stdout,
		inspectHarnessPromptEmbed(filepath.Join(clone, "pfm", harnessprompts.DirName)),
	)
}

func printHarnessPromptEmbedReport(stdout io.Writer, report harnessPromptEmbedReport) int {
	tree := report.Tree
	if tree == "" {
		tree = "(unresolved)"
	}
	switch {
	case report.Err != nil:
		fmt.Fprintf(
			stdout,
			"doctor: harness-prompts embed=CHECK FAILED tree=%s error=%v — the clone's tree could not be read, "+
				"so whether the two trees agree is UNKNOWN, not clean\n",
			tree,
			report.Err,
		)
		return 1
	case len(report.Differing) == 0:
		fmt.Fprintf(stdout, "doctor: harness-prompts embed=ok tree=%s files=%d\n", tree, report.Files)
		return 0
	default:
		fmt.Fprintf(
			stdout,
			"doctor: harness-prompts embed=MISMATCH tree=%s differ=%s — this binary's embedded prompts differ "+
				"from the clone's tree; if the clone holds the newer prompts, rebuild pfm (make host-install "+
				"from pfm/) then run pfm install, and if the clone is checked out to an older revision than "+
				"this binary, the binary is the newer one and nothing needs rebuilding\n",
			tree,
			strings.Join(report.Differing, ","),
		)
		return 1
	}
}

// inspectHarnessPromptEmbed hashes both trees file by file and names every
// file whose bytes differ, is only in the clone, or is only in the binary.
func inspectHarnessPromptEmbed(tree string) harnessPromptEmbedReport {
	report := harnessPromptEmbedReport{Tree: tree}
	info, err := os.Stat(tree)
	if err != nil {
		report.Err = fmt.Errorf("inspect harness prompt tree %s: %w", tree, err)
		return report
	}
	if !info.IsDir() {
		report.Err = fmt.Errorf("harness prompt tree %s is not a directory", tree)
		return report
	}
	embedded, err := hashHarnessPromptTree(harnessprompts.FS(), nil)
	if err != nil {
		report.Err = fmt.Errorf("hash the embedded harness prompt tree: %w", err)
		return report
	}
	onDisk, err := hashHarnessPromptTree(os.DirFS(tree), harnessPromptTreeExcluded)
	if err != nil {
		report.Err = fmt.Errorf("hash harness prompt tree %s: %w", tree, err)
		return report
	}
	report.Files = len(embedded)
	report.Differing = harnessPromptTreeDifferences(embedded, onDisk)
	return report
}

// harnessPromptTreeExcluded reports whether a file found in the clone's tree
// is outside the comparison. The embed package's own sources sit in that
// directory and are never part of the prompt tree; a name beginning with "."
// or "_" is one `go:embed` skips over a directory, so counting it here would
// report every macOS .DS_Store as a difference between the trees.
func harnessPromptTreeExcluded(name string) bool {
	if strings.HasSuffix(name, ".go") {
		return true
	}
	for _, element := range strings.Split(name, "/") {
		if strings.HasPrefix(element, ".") || strings.HasPrefix(element, "_") {
			return true
		}
	}
	return false
}

// hashHarnessPromptTree maps every file in a tree to the hex sha256 of its
// bytes. A file that cannot be read is an error, never a missing entry — an
// unreadable template must not read as one side simply not having it.
func hashHarnessPromptTree(tree fs.FS, excluded func(string) bool) (map[string]string, error) {
	hashes := map[string]string{}
	err := fs.WalkDir(tree, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk %s: %w", name, err)
		}
		if entry.IsDir() || (excluded != nil && excluded(name)) {
			return nil
		}
		content, err := fs.ReadFile(tree, name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		sum := sha256.Sum256(content)
		hashes[name] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(hashes) == 0 {
		return nil, errors.New("tree holds no files")
	}
	return hashes, nil
}

// harnessPromptTreeDifferences names each file the two trees disagree about,
// sorted, with which side has it when only one does.
func harnessPromptTreeDifferences(embedded, onDisk map[string]string) []string {
	var differing []string
	for name, sum := range embedded {
		switch other, found := onDisk[name]; {
		case !found:
			differing = append(differing, name+"(only-in-binary)")
		case other != sum:
			differing = append(differing, name)
		}
	}
	for name := range onDisk {
		if _, found := embedded[name]; !found {
			differing = append(differing, name+"(only-in-clone)")
		}
	}
	sort.Strings(differing)
	return differing
}
