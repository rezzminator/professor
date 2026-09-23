package harvestcli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/ask"
	"github.com/rezzminator/professor/pfm/internal/cli"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/doctor"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/harvest"
)

// askAlias keeps the originally attempted `pfm harvest --ask -p ...`
// spelling useful while `pfm harvest ask -p ...` remains the canonical form.
func askAlias(args []string) ([]string, bool) {
	for index, argument := range args {
		if argument != "--ask" {
			continue
		}
		result := make([]string, 0, len(args)-1)
		result = append(result, args[:index]...)
		result = append(result, args[index+1:]...)
		return result, true
	}
	return nil, false
}

func runAsk(args []string, stdout, stderr io.Writer, runtime config.Runtime) int {
	flags := cli.NewFlagSet(
		"harvest ask",
		"usage: pfm harvest ask -p <prompt> [--engine claude|codex] [--model MODEL] [--effort EFFORT] [--refresh] <url|doi|path>...",
		stderr,
	)
	prompt := flags.String("prompt", "", "question answered only from the harvested sources")
	flags.StringVar(prompt, "p", "", "question answered only from the harvested sources")
	engineName := flags.String("engine", "", "ask engine (claude or codex; default from config)")
	model := flags.String("model", "", "override the configured ask model")
	effort := flags.String("effort", "", "override the configured reasoning effort")
	refresh := flags.Bool("refresh", false, "bypass the harvest cache")
	sources, code, ok := cli.ParseFlagsAnywhere(flags, args)
	if !ok {
		return code
	}
	if strings.TrimSpace(*prompt) == "" || len(sources) == 0 || len(sources) > maxSources {
		flags.Usage()
		return 2
	}

	engineID := pfmengine.ID("")
	var err error
	if strings.TrimSpace(*engineName) != "" {
		engineID, err = pfmengine.Parse(*engineName)
	} else {
		engineID, err = runtime.Config.DefaultEngine()
	}
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: resolve engine: %v\n", err)
		return 2
	}
	runner, err := ask.ResolveEngine(engineID, runtime.Config)
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: resolve %s engine: %v\n", engineID, err)
		return 1
	}

	harvester, err := newHarvester(harvesterRuntime(runtime))
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: configure harvester: %v\n", err)
		return 1
	}
	files := make([]string, 0, len(sources))
	labels := make([]string, 0, len(sources))
	receiptDir := ""
	for index, source := range sources {
		result := harvester.FetchPublic(
			context.Background(),
			source,
			harvest.FetchOptions{Refresh: *refresh, SizeOnly: true},
		)
		path := result.Path
		if result.Error == "" && path != "" {
			path, err = filepath.Abs(path)
			if err != nil {
				result.Error = fmt.Sprintf("resolve cache path %s: %v", result.Path, err)
				result.ErrorKind = "cache_path"
			}
		} else if result.Error == "" {
			result.Error = "harvester returned success without a full cache path"
			result.ErrorKind = "cache_path"
		}
		if result.Error != "" {
			path, receiptDir, err = writeAskReceipt(runtime.Paths.Home, receiptDir, index, source, result)
			if err != nil {
				fmt.Fprintf(stderr, "pfm harvest ask: prepare receipt for %q: %v\n", source, err)
				if receiptDir != "" {
					if cleanupErr := os.RemoveAll(receiptDir); cleanupErr != nil {
						fmt.Fprintf(stderr, "pfm harvest ask: cleanup receipts %s: %v\n", receiptDir, cleanupErr)
					}
				}
				return 1
			}
		}
		files = append(files, path)
		labels = append(labels, source)
	}

	input, err := ask.ResolveInput(ask.AskInput{
		ContentFiles: files,
		SourceLabels: labels,
		Prompt:       *prompt,
		Engine:       engineID,
		Model:        *model,
		Effort:       *effort,
	}, runtime.Config)
	if err != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: prepare model input: %v\n", err)
		if receiptDir != "" {
			if cleanupErr := os.RemoveAll(receiptDir); cleanupErr != nil {
				fmt.Fprintf(stderr, "pfm harvest ask: cleanup receipts %s: %v\n", receiptDir, cleanupErr)
			}
		}
		return 1
	}
	answer, runErr := runner.Run(context.Background(), input)
	cleanupErr := error(nil)
	if receiptDir != "" {
		cleanupErr = os.RemoveAll(receiptDir)
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: %v\n", runErr)
		if cleanupErr != nil {
			fmt.Fprintf(stderr, "pfm harvest ask: cleanup receipts %s: %v\n", receiptDir, cleanupErr)
		}
		return 1
	}
	if cleanupErr != nil {
		fmt.Fprintf(stderr, "pfm harvest ask: cleanup receipts %s: %v\n", receiptDir, cleanupErr)
		return 1
	}
	fmt.Fprintln(stdout, answer.Answer)
	if answer.Usage != nil {
		fmt.Fprintf(
			stderr,
			"pfm harvest ask: usage input=%d cached_input=%d output=%d\n",
			answer.Usage.Input,
			answer.Usage.CachedInput,
			answer.Usage.Output,
		)
	}
	return 0
}

func writeAskReceipt(
	home, receiptDir string,
	index int,
	source string,
	result harvest.Result,
) (string, string, error) {
	if receiptDir == "" {
		root := filepath.Join(home, ".local", "state", "pfm", "harvest-ask")
		if err := os.MkdirAll(root, 0o700); err != nil {
			return "", "", fmt.Errorf("create receipt root %s: %w", root, err)
		}
		var err error
		receiptDir, err = os.MkdirTemp(root, "run-")
		if err != nil {
			return "", "", fmt.Errorf("create receipt directory under %s: %w", root, err)
		}
	}
	payload, err := json.MarshalIndent(struct {
		Status string         `json:"status"`
		Input  string         `json:"input"`
		Result harvest.Result `json:"result"`
	}{Status: doctor.StateUnavailable, Input: source, Result: harvest.PublicFailure(source, result)}, "", "  ")
	if err != nil {
		return "", receiptDir, fmt.Errorf("encode receipt: %w", err)
	}
	path := filepath.Join(receiptDir, fmt.Sprintf("source-%03d.json", index+1))
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		return "", receiptDir, fmt.Errorf("write receipt %s: %w", path, err)
	}
	return path, receiptDir, nil
}
