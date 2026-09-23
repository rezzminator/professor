// Package main is corpussan, the command that runs the callmeter test-corpus sanitizer over one session
// transcript (see package corpussan):
//
//	go -C pfm run ./internal/callmeter/corpussan/cmd/corpussan \
//	  -session CONFIG/projects/DIR/SESSION.jsonl -out OUTDIR \
//	  -project /abs/project/root -home /abs/home [-tree DIR] [-allow a,b] [-deny a,b]
//
// It writes OUTDIR/SESSION.jsonl and OUTDIR/SESSION/subagents/…, refuses to
// overwrite a file, and prints each path it wrote. Its stderr is its
// user-facing output, as cmd/pfm's is: a failure prints one line and exits 1.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/callmeter/corpussan"
)

func main() { os.Exit(sanitizeExit(os.Args[1:], os.Stdout, os.Stderr)) }

// sanitizeExit runs runSanitize and reports its failure on stderr: 0 or 1.
func sanitizeExit(args []string, stdout, stderr io.Writer) int {
	if err := runSanitize(args, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "corpussan:", err)
		return 1
	}
	return 0
}

func runSanitize(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("corpussan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	session := fs.String("session", "", "the session transcript {session}.jsonl")
	out := fs.String("out", "", "the directory the sanitized files are written to")
	project := fs.String("project", "", "absolute project root; becomes /tmp/demo-proj")
	home := fs.String("home", "", "absolute home dir; paths outside the project land under /tmp/demo-home")
	tree := fs.String("tree", "", "a directory whose file and directory names are kept (default: -project)")
	allow := fs.String("allow", "", "comma-separated words kept in paths and commands")
	deny := fs.String("deny", "", "comma-separated words never kept")
	if err := fs.Parse(args); err != nil {
		return err
	}
	switch {
	case *session == "":
		return errors.New("-session is required")
	case *out == "":
		return errors.New("-out is required")
	case *project == "" || *home == "":
		return errors.New("-project and -home are required")
	}
	if *tree == "" {
		*tree = *project
	}
	words, err := corpussan.AllowFromTree(*tree)
	if err != nil {
		return err
	}
	s, err := corpussan.NewSanitizer(corpussan.Options{
		Project: *project,
		Home:    *home,
		Allow:   append(words, splitList(*allow)...),
		Deny:    splitList(*deny),
	})
	if err != nil {
		return err
	}
	written, err := s.Session(*session, *out)
	for _, path := range written {
		if _, werr := fmt.Fprintln(stdout, path); werr != nil {
			return errors.Join(err, fmt.Errorf("print %s: %w", path, werr))
		}
	}
	return err
}

func splitList(v string) []string {
	var words []string
	for _, w := range strings.Split(v, ",") {
		if w = strings.TrimSpace(w); w != "" {
			words = append(words, w)
		}
	}
	return words
}
