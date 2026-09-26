package cli

import (
	"bytes"
	"errors"
	"flag"
	"io"
	"reflect"
	"testing"
)

func TestParseFlagsAnywhere(t *testing.T) {
	var stderr bytes.Buffer
	flags := NewFlagSet("test", "usage: test", &stderr)
	jsonOutput := flags.Bool("json", false, "json output")
	positional, code, ok := ParseFlagsAnywhere(flags, []string{"seat", "--json", "tail"})
	if !ok || code != 0 || !*jsonOutput || !reflect.DeepEqual(positional, []string{"seat", "tail"}) {
		t.Fatalf("ParseFlagsAnywhere() = (%v, %d, %t, json=%t)", positional, code, ok, *jsonOutput)
	}
}

func TestCloseResourceRecordsFirstFailure(t *testing.T) {
	exitCode := 0
	var stderr bytes.Buffer
	CloseResource(failingCloser{}, "close resource", &stderr, &exitCode)
	if exitCode != 1 || stderr.String() != "close resource: failed\n" {
		t.Fatalf("CloseResource() = code %d, stderr %q", exitCode, stderr.String())
	}
}

type failingCloser struct{}

func (failingCloser) Close() error { return errors.New("failed") }

var (
	_ io.Closer = failingCloser{}
	_           = flag.ErrHelp
)
