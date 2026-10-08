package command

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/pricing/modelcost"
)

type modelCostTransport func(*http.Request) (*http.Response, error)

func (f modelCostTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const (
	modelCostClaudePage = "# Pricing\nAll prices are in USD.\n## Model pricing\n| Model | Base input tokens | Output tokens |\n| --- | --- | --- |\n| Claude Opus 5.5 | $4 / MTok | $20 / MTok |\n"
	modelCostOpenAIPage = "# Pricing\nPrices per 1M tokens. All prices USD.\n### Standard pricing data\n| Model | Input | Output |\n| --- | --- | --- |\n| gpt-6.1-sol | $2.00 | $10.00 |\n"
)

func modelCostFetcher(fail error) *modelcost.Fetcher {
	return modelcost.NewFetcher(
		&http.Client{Transport: modelCostTransport(func(r *http.Request) (*http.Response, error) {
			if fail != nil {
				return nil, fail
			}
			body := modelCostOpenAIPage
			if r.URL.Host == "platform.claude.com" {
				body = modelCostClaudePage
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
		})},
		nil,
	)
}

func TestModelCostCommandUsage(t *testing.T) {
	var out, errs bytes.Buffer
	if code := ModelCost([]string{"--help"}, &out, &errs); code != 0 || !strings.Contains(out.String(), "--all") {
		t.Fatalf("help code=%d out=%s err=%s", code, out.String(), errs.String())
	}
	for _, args := range [][]string{{}, {"--all", "gpt-6.1-sol"}, {"a", "b"}, {"--bogus"}, {"--refresh", "gpt-6.1-sol"}} {
		if code := ModelCost(args, &out, &errs); code != 2 {
			t.Fatalf("args=%v code=%d", args, code)
		}
	}
}

func TestModelCostCommandPrintsLivePrices(t *testing.T) {
	var out, errs bytes.Buffer
	if code := runModelCost([]string{"--json", "gpt-6.1-sol"}, &out, &errs, modelCostFetcher(nil)); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, errs.String())
	}
	if !strings.Contains(out.String(), `"model":"gpt-6.1-sol"`) || !strings.Contains(out.String(), `"$2.00"`) {
		t.Fatalf("stdout lacks the published row: %s", out.String())
	}
}

func TestModelCostCommandFailsWithoutPrices(t *testing.T) {
	var out, errs bytes.Buffer
	code := runModelCost([]string{"--all"}, &out, &errs, modelCostFetcher(errors.New("fixture network failure")))
	if code != 1 || out.Len() != 0 || !strings.Contains(errs.String(), "fixture network failure") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, out.String(), errs.String())
	}
}
