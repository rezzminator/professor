package digest

import (
	"errors"
	"slices"
	"testing"
)

func TestArgsReadTheArgparseForms(t *testing.T) {
	req, err := parseDigestArgs([]string{
		"--tool", "Bash", "show", "--width=90", "abc123", "--since", "-15m",
		"--tool=Read", "--first", "-3", "-i",
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.verb != "show" || req.target != "abc123" || req.width != 90 || req.since != "-15m" || req.first != -3 ||
		!req.ignoreCase || !slices.Equal(req.tools, []string{"Bash", "Read"}) {
		t.Errorf("request = %+v", req)
	}
	if req.only != defaultOnly || req.results != "brief" || req.errLines != 12 || req.text != 500 {
		t.Errorf("defaults = only %q results %q err-lines %d text %d", req.only, req.results, req.errLines, req.text)
	}
	refusals := []struct {
		args []string
		want string
	}{
		{[]string{"list", "x"}, "argument verb: invalid choice: 'list' (choose from 'show', 'counts', 'types', " +
			"'locate', 'agents')"},
		{[]string{"--", "list", "x"}, "argument verb: invalid choice: 'list' (choose from 'show', 'counts', " +
			"'types', 'locate', 'agents')"},
		{[]string{"show"}, "the following arguments are required: target"},
		{[]string{"show", "x", "--grep", "-v"}, "argument --grep: expected one argument"},
		{[]string{"show", "x", "--width", "nope"}, "argument --width: invalid int value: 'nope'"},
		{[]string{"show", "x", "--wid", "3"}, "unrecognized arguments: --wid 3"},
		{[]string{"show", "x", "--results", "half"}, "--results 'half': brief, none, full or tail:N"},
	}
	for _, row := range refusals {
		_, err := parseDigestArgs(row.args)
		var failure *Failure
		if !errors.As(err, &failure) || !failure.Usage || failure.Message != row.want {
			t.Errorf("parseDigestArgs(%q) = %v, want usage %q", row.args, err, row.want)
		}
	}
	for _, row := range []struct {
		arg  string
		want bool
	}{{"--grep", true}, {"-", false}, {"-3", false}, {"-1.5", false}, {"-x y", false}, {"-v", true}, {"word", false}} {
		if got := isOptionArg(row.arg); got != row.want {
			t.Errorf("isOptionArg(%q) = %v, want %v", row.arg, got, row.want)
		}
	}
}
