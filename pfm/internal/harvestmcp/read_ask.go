package harvestmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/ask"
	"github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/harvest"
	"github.com/rezzminator/professor/pfm/internal/pricing"
)

// The read tool's ask: one question answered over every item of the call by
// a local engine run through the ask façade (internal/ask), the same answer
// step `pfm harvest ask` takes (AskOver). The answer follows the item blocks:
//
//	=== answer ({engine} {model})
//	{the engine's answer, its EVIDENCE section citing [file N] = item N}
//
// or an `error:` line under the same header. The answer is not cached: it
// depends on the question, the engine and the model, and a re-ask is the
// caller's own request.

// defaultAskModel answers ask on claude when the call names no model (the
// user's ruling: claude + haiku); another engine takes its configured ask
// model (ask.<engine>.model).
const defaultAskModel = "haiku"

// askReceiptStatus is a failure receipt's status, the word doctor's
// StateUnavailable spells (harvestmcp cannot import doctor: doctor → mcpserv
// → harvestmcp).
const askReceiptStatus = "unavailable"

// readAsk is one read call's question and the engine that answers it,
// resolved before any item is read.
type readAsk struct {
	question string
	engine   pfmengine.ID
	model    string // "" leaves the engine's configured ask model to ask.ResolveInput
	runner   ask.Engine
	machine  config.Config
}

// parseAsk is read's ask, engine and model checked as a whole call: nil when
// the call asks nothing, else the engine resolved (accounts, binary). Every
// refusal names its field, before an item is read or an engine starts.
func (service *Service) parseAsk(input ReadInput) (*readAsk, error) {
	if input.Ask == "" {
		if input.Engine != "" || input.Model != "" {
			return nil, errors.New("engine and model require ask: they choose what answers it")
		}
		return nil, nil
	}
	if service.runtime.Remote {
		return nil, errors.New("ask is not available on the remote server: read the items here and ask your own model")
	}
	if strings.TrimSpace(input.Ask) == "" {
		return nil, errors.New("ask is blank: send the question to answer")
	}
	if service.runtime.Machine == nil {
		return nil, errors.New(
			"ask is not available on this harvester: it was started without the pfm config an engine runs under",
		)
	}
	machine := *service.runtime.Machine
	engine := pfmengine.Claude
	if input.Engine != "" {
		parsed, err := pfmengine.Parse(input.Engine)
		if err != nil {
			return nil, fmt.Errorf("engine: %w", err)
		}
		engine = parsed
	}
	model := strings.TrimSpace(input.Model)
	switch {
	case model != "":
		prices, err := pricing.LoadPrices(service.runtime.Home, machine.Path)
		if err != nil {
			return nil, fmt.Errorf("model %q: load the price table that names the models: %w", model, err)
		}
		model, err = ask.CheckModel(engine, model, prices.Table)
		if err != nil {
			return nil, err
		}
	case engine == pfmengine.Claude:
		model = defaultAskModel
	default:
		model = machine.Ask.PrefsFor(engine).Model
	}
	runner, err := ask.ResolveEngine(engine, machine)
	if err != nil {
		return nil, fmt.Errorf("engine %s: %w", pfmengine.MustLookup(engine).LongName, err)
	}
	return &readAsk{question: input.Ask, engine: engine, model: model, runner: runner, machine: machine}, nil
}

// answerBlock is the answer to question over the call's items: its header,
// then the answer or an error line. failed[i] reports that item i's block is
// a failure; every item failed asks nothing. It reports whether the answer
// failed.
func (service *Service) answerBlock(
	ctx context.Context, question *readAsk, sources []string, results []harvest.Result, failed []bool,
) (string, bool) {
	header := strings.TrimSpace(fmt.Sprintf("=== answer (%s %s", pfmengine.MustLookup(question.engine).LongName,
		question.model)) + ")"
	asked := false
	for _, itemFailed := range failed {
		asked = asked || !itemFailed
	}
	if !asked {
		return header + "\nerror: not asked: no item was read", true
	}
	answer, err := AskOver(ctx, question.machine, service.runtime.Home, question.runner, ask.AskInput{
		Prompt: question.question, Engine: question.engine, Model: question.model,
	}, sources, results)
	if err != nil {
		return header + "\nerror: " + err.Error(), true
	}
	return header + "\n" + strings.TrimRight(answer.Answer, " \t\r\n"), false
}

// AskOver answers input.Prompt with runner over one read's items: item i is
// the prompt's file i+1 — its artifact, or for an item that failed (an error,
// or no artifact path) a receipt of its public failure under
// {home}/.local/state/pfm/harvest-ask/, removed once the engine answered — so
// the answer's [file N] is item N. ask.ResolveInput fills what input leaves
// empty (model, effort) from machine's ask config. The read tool's ask and
// `pfm harvest ask` both answer through it.
func AskOver(
	ctx context.Context,
	machine config.Config,
	home string,
	runner ask.Engine,
	input ask.AskInput,
	sources []string,
	results []harvest.Result,
) (ask.AskResult, error) {
	files := make([]string, 0, len(sources))
	receiptDir := ""
	cleanup := func(err error) error {
		if receiptDir == "" {
			return err
		}
		if cleanupErr := os.RemoveAll(receiptDir); cleanupErr != nil {
			return errors.Join(err, fmt.Errorf("cleanup receipts %s: %w", receiptDir, cleanupErr))
		}
		return err
	}
	for index, source := range sources {
		result := results[index]
		path := result.Path
		if result.Error == "" && path != "" {
			absolute, err := filepath.Abs(path)
			if err != nil {
				result.Error = fmt.Sprintf("resolve cache path %s: %v", result.Path, err)
				result.ErrorKind = "cache_path"
			}
			path = absolute
		} else if result.Error == "" {
			result.Error = "harvester returned success without a full cache path"
			result.ErrorKind = "cache_path"
		}
		if result.Error != "" {
			var err error
			path, receiptDir, err = writeAskReceipt(home, receiptDir, index, source, result)
			if err != nil {
				return ask.AskResult{}, cleanup(fmt.Errorf("prepare receipt for %q: %w", source, err))
			}
		}
		files = append(files, path)
	}
	input.ContentFiles, input.SourceLabels = files, sources
	resolved, err := ask.ResolveInput(input, machine)
	if err != nil {
		return ask.AskResult{}, cleanup(fmt.Errorf("prepare model input: %w", err))
	}
	answer, runErr := runner.Run(ctx, resolved)
	if err := cleanup(runErr); err != nil {
		return ask.AskResult{}, err
	}
	return answer, nil
}

// writeAskReceipt writes item index's public failure as the file the engine
// reads in its place, creating the run's receipt directory on first use.
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
	}{Status: askReceiptStatus, Input: source, Result: harvest.PublicFailure(source, result)}, "", "  ")
	if err != nil {
		return "", receiptDir, fmt.Errorf("encode receipt: %w", err)
	}
	path := filepath.Join(receiptDir, fmt.Sprintf("source-%03d.json", index+1))
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		return "", receiptDir, fmt.Errorf("write receipt %s: %w", path, err)
	}
	return path, receiptDir, nil
}
