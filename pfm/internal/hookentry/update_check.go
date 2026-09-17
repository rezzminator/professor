package hookentry

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"hostops/pfm/internal/cli"
	"hostops/pfm/internal/updatecheck"
)

const professorLatestReleaseURL = "https://github.com/" + updatecheck.ProfessorRepo + "/releases/latest"

// UpdateCheck refreshes the release notice cache for the picker.
func UpdateCheck(args []string, stderr io.Writer) int {
	flags := cli.NewFlagSet(
		"internal update-check",
		"usage: pfm internal update-check --cache PATH --current vX.Y.Z --url URL",
		stderr,
	)
	cache := flags.String("cache", "", "update cache path")
	current := flags.String("current", "", "installed pfm version")
	latestURL := flags.String("url", professorLatestReleaseURL, "latest release redirect")
	if code, ok := cli.ParseFlags(flags, args); !ok {
		return code
	}
	if flags.NArg() != 0 || *cache == "" || *current == "" || *latestURL == "" {
		flags.Usage()
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 12 * time.Second}
	if err := updatecheck.CheckForUpdate(ctx, *cache, *current, *latestURL, client); err != nil {
		fmt.Fprintf(stderr, "pfm internal update-check: %v\n", err)
		return 1
	}
	return 0
}
