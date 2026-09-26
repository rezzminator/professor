// Package cli holds the flag-set and exit-code conventions every pfm command
// shares: usage on stderr, --help exits 0, flags before or after positionals,
// and a close that records the first failure as exit 1.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

func NewFlagSet(name, usage string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	flags.Usage = func() {
		fmt.Fprintln(stderr, usage)
	}
	return flags
}

func ParseFlags(flags *flag.FlagSet, args []string) (int, bool) {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, false
		}
		return 2, false
	}
	return 0, true
}

// ParseFlagsAnywhere accepts flags before or after positional arguments.
func ParseFlagsAnywhere(flags *flag.FlagSet, args []string) ([]string, int, bool) {
	positional := make([]string, 0, 2)
	for {
		if code, ok := ParseFlags(flags, args); !ok {
			return nil, code, false
		}
		rest := flags.Args()
		if len(rest) == 0 {
			return positional, 0, true
		}
		positional = append(positional, rest[0])
		args = rest[1:]
	}
}

func CloseResource(closer io.Closer, label string, stderr io.Writer, exitCode *int) {
	if err := closer.Close(); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", label, err)
		if *exitCode == 0 {
			*exitCode = 1
		}
	}
}

// StringList collects a repeated flag in command-line order.
type StringList []string

func (list *StringList) String() string {
	return fmt.Sprintf("%v", []string(*list))
}

func (list *StringList) Set(value string) error {
	if value == "" {
		return fmt.Errorf("a then steer must be non-empty")
	}
	*list = append(*list, value)
	return nil
}

var _ flag.Value = (*StringList)(nil)
