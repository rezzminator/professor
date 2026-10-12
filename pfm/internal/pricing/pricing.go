// Package pricing owns the model price table: prices.json, the one file the
// clone tracks, pfm embeds and `pfm model-cost` refreshes; the optional
// pfm.prices.json override merged into it by key; and the resolution of a model
// id to its row. Rates are USD per million tokens.
package pricing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/engine"
)

var (
	// EngineClaude rows carry hit, w5m and w1h; EngineCodex rows carry cached.
	EngineClaude = engine.MustLookup(engine.Claude).LongName
	EngineCodex  = engine.MustLookup(engine.Codex).LongName
)

const (
	// Version is the only prices.json and pfm.prices.json version read.
	Version = 2
	// SourcePublished marks a served row of the published table, SourceOverride
	// one that came from pfm.prices.json.
	SourcePublished = "published"
	SourceOverride  = "override"
)

// Source is one publisher page the table was derived from.
type Source struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
}

// Rates are one tier's prices; nil is a rate the publisher does not list,
// never a zero price.
type Rates struct {
	In     *float64 `json:"in,omitempty"`
	Out    *float64 `json:"out,omitempty"`
	Hit    *float64 `json:"hit,omitempty"`    // claude: cache read
	W5m    *float64 `json:"w5m,omitempty"`    // claude: 5-minute cache write
	W1h    *float64 `json:"w1h,omitempty"`    // claude: 1-hour cache write
	Cached *float64 `json:"cached,omitempty"` // codex: cached input, a subset of input
}

// Long is the publisher's upper tier: its rates price a call whose context is
// greater than Above tokens.
type Long struct {
	Above int64 `json:"above"`
	Rates
}

// Row is one model's prices. In and Out are always present on a valid row.
type Row struct {
	Key    string `json:"key"`
	Engine string `json:"engine"`
	Rates
	Long   *Long  `json:"long,omitempty"`
	Source string `json:"source,omitempty"`
}

// Table is a prices.json document.
type Table struct {
	Version   int      `json:"version"`
	FetchedAt string   `json:"fetched_at"`
	Sources   []Source `json:"sources"`
	Rows      []Row    `json:"rows"`
}

// ClaudeUsage is one Claude response's billed token counts: cache writes split
// by TTL, a write with no TTL breakdown counted at the 5-minute rate.
type ClaudeUsage struct {
	Input, Output, CacheRead, Write5m, Write1h int64
}

// Context is the request's prompt tokens: uncached input, cache read and cache
// writes. It selects the request's price tier.
func (c ClaudeUsage) Context() int64 { return c.Input + c.CacheRead + c.Write5m + c.Write1h }

func (c ClaudeUsage) plus(other ClaudeUsage) ClaudeUsage {
	return ClaudeUsage{
		Input:     c.Input + other.Input,
		Output:    c.Output + other.Output,
		CacheRead: c.CacheRead + other.CacheRead,
		Write5m:   c.Write5m + other.Write5m,
		Write1h:   c.Write1h + other.Write1h,
	}
}

// UsageBlock is a Claude response's usage block as a transcript records it,
// the one decoding every transcript reader shares.
type UsageBlock struct {
	Input         int64           `json:"input_tokens"`
	Output        int64           `json:"output_tokens"`
	CacheRead     int64           `json:"cache_read_input_tokens"`
	CacheCreation int64           `json:"cache_creation_input_tokens"`
	Breakdown     *CacheBreakdown `json:"cache_creation"`
	Iterations    []UsageBlock    `json:"iterations"`
	Type          string          `json:"type"`
	Model         string          `json:"model"`
	OutputDetails *struct {
		Thinking int64 `json:"thinking_tokens"`
	} `json:"output_tokens_details"`
}

// CacheBreakdown is a cache write split by TTL.
type CacheBreakdown struct {
	Write5m int64 `json:"ephemeral_5m_input_tokens"`
	Write1h int64 `json:"ephemeral_1h_input_tokens"`
}

// Billed is what the response's own model billed. A response can carry zeros
// in every top-level count and its real counts only in Iterations (seen on
// claude-opus-5-5, 2026-09): then the iterations' counts are summed; otherwise
// the top level already equals their sum. An advisor iteration is never part
// of either: it is billed to the advisor model (see Advisors). A cache write
// splits by TTL, and the part of it the breakdown does not cover (none, or a
// short one) is the default 5-minute TTL. split reports a write that carried a
// TTL breakdown.
func (u UsageBlock) Billed() (usage ClaudeUsage, split bool) {
	folded := u
	if u.Input+u.Output+u.CacheRead+u.CacheCreation == 0 && len(u.Iterations) > 0 {
		folded = UsageBlock{Breakdown: &CacheBreakdown{}}
		for _, iteration := range u.Iterations {
			if iteration.IsAdvisor() {
				continue
			}
			folded.Input += iteration.Input
			folded.Output += iteration.Output
			folded.CacheRead += iteration.CacheRead
			folded.CacheCreation += iteration.CacheCreation
			if iteration.Breakdown != nil {
				folded.Breakdown.Write5m += iteration.Breakdown.Write5m
				folded.Breakdown.Write1h += iteration.Breakdown.Write1h
			}
		}
	}
	var write5m, write1h int64
	if folded.Breakdown != nil {
		write5m, write1h = folded.Breakdown.Write5m, folded.Breakdown.Write1h
	}
	return ClaudeUsage{
		Input:     folded.Input,
		Output:    folded.Output,
		CacheRead: folded.CacheRead,
		Write5m:   write5m + max(folded.CacheCreation-write5m-write1h, 0),
		Write1h:   write1h,
	}, write5m+write1h > 0
}

// AdvisorUsage is one advisor consultation inside a response: the counts the
// advisor model billed, and the model that billed them.
type AdvisorUsage struct {
	Model string
	Usage ClaudeUsage
	Split bool
}

// IterationAdvisor is the type of an iteration the advisor tool billed to the
// advisor's own model.
const IterationAdvisor = "advisor_message"

// IsAdvisor reports whether this iteration was billed to the advisor model.
func (u UsageBlock) IsAdvisor() bool { return u.Type == IterationAdvisor }

// Advisors is the response's advisor consultations, one per advisor iteration
// that billed a token, in order. The executor's top-level counts never include
// them; the model is the advisor's, empty when the iteration names none.
func (u UsageBlock) Advisors() []AdvisorUsage {
	var advisors []AdvisorUsage
	for _, iteration := range u.Iterations {
		if !iteration.IsAdvisor() {
			continue
		}
		usage, split := iteration.Billed()
		if usage == (ClaudeUsage{}) {
			continue
		}
		advisors = append(advisors, AdvisorUsage{Model: iteration.Model, Usage: usage, Split: split})
	}
	return advisors
}

// IterationMessage is the type of an iteration the response's own model
// billed: one request of the executor.
const IterationMessage = "message"

// isExecutor reports an iteration the response's own model billed: a message,
// or an iteration naming no type (the shape seen on claude-opus-5-5, 2026-09).
func (u UsageBlock) isExecutor() bool { return u.Type == "" || u.Type == IterationMessage }

// Requests is the executor's requests the response billed, in order, each with
// its own context: a response that consulted an advisor sums two or more
// requests at its top level. A response whose top level is empty bills each
// non-advisor iteration that billed a token; one whose message iterations sum
// exactly to its top level bills those; any other response is one request, its
// top level. A response that billed only its advisor made no request: none.
func (u UsageBlock) Requests() []ClaudeUsage {
	billed, _ := u.Billed()
	empty := u.Input+u.Output+u.CacheRead+u.CacheCreation == 0
	var requests []ClaudeUsage
	var sum ClaudeUsage
	for _, iteration := range u.Iterations {
		if iteration.IsAdvisor() || !empty && !iteration.isExecutor() {
			continue
		}
		request, _ := iteration.Billed()
		if request == (ClaudeUsage{}) {
			continue
		}
		requests = append(requests, request)
		sum = sum.plus(request)
	}
	switch {
	case empty:
		return requests
	case len(requests) > 0 && sum == billed:
		return requests
	}
	return []ClaudeUsage{billed}
}

// UnknownIterations is the type of each iteration that is neither the
// executor's nor an advisor's, in order: pfm cannot tell how its counts relate
// to the top level, so it prices none of them apart.
func (u UsageBlock) UnknownIterations() []string {
	var unknown []string
	for _, iteration := range u.Iterations {
		if !iteration.isExecutor() && !iteration.IsAdvisor() {
			unknown = append(unknown, iteration.Type)
		}
	}
	return unknown
}

// Thinking is the response's thinking tokens, a part of Output.
func (u UsageBlock) Thinking() int64 {
	if u.OutputDetails == nil {
		return 0
	}
	return u.OutputDetails.Thinking
}

// ClaudeParts is one Claude response's USD by billing column.
type ClaudeParts struct {
	Input, Output, Write5m, Write1h, CacheRead float64
}

// Total is the response's USD: the columns summed in one fixed order.
func (p ClaudeParts) Total() float64 {
	return p.Input + p.Output + p.Write5m + p.Write1h + p.CacheRead
}

func (p ClaudeParts) plus(other ClaudeParts) ClaudeParts {
	return ClaudeParts{
		Input:     p.Input + other.Input,
		Output:    p.Output + other.Output,
		Write5m:   p.Write5m + other.Write5m,
		Write1h:   p.Write1h + other.Write1h,
		CacheRead: p.CacheRead + other.CacheRead,
	}
}

// CodexUsage is a Codex thread's billed counts: Input includes Cached, Output
// includes reasoning, and Codex bills no cache write.
type CodexUsage struct {
	Input, Cached, Output int64
}

var (
	bedrockPrefix  = regexp.MustCompile(`^(?:[a-z0-9-]+\.)?anthropic\.`)
	bedrockVersion = regexp.MustCompile(`-v\d+(?::\d+)?$`)
	snapshotDate   = regexp.MustCompile(`-(?:\d{8}|\d{4}-\d{2}-\d{2})$`)
)

// strictUnmarshal decodes one JSON value, refusing unknown fields and any
// second value after it.
func strictUnmarshal(content []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func checkVersion(version *int) error {
	if version == nil {
		return fmt.Errorf("version must be %d, got missing", Version)
	}
	if *version != Version {
		return fmt.Errorf("version must be %d, got %d", Version, *version)
	}
	return nil
}

// DecodeTable reads and validates one prices.json document; name labels every error.
func DecodeTable(content []byte, name string) (Table, error) {
	var doc struct {
		Version   *int      `json:"version"`
		FetchedAt *string   `json:"fetched_at"`
		Sources   *[]Source `json:"sources"`
		Rows      *[]Row    `json:"rows"`
	}
	if err := strictUnmarshal(content, &doc); err != nil {
		return Table{}, fmt.Errorf("prices %s: %w", name, err)
	}
	if err := checkVersion(doc.Version); err != nil {
		return Table{}, fmt.Errorf("prices %s: %w", name, err)
	}
	table := Table{Version: Version}
	switch {
	case doc.FetchedAt == nil:
		return Table{}, fmt.Errorf("prices %s: fetched_at is missing", name)
	case doc.Sources == nil:
		return Table{}, fmt.Errorf("prices %s: sources must be an array, got missing", name)
	case doc.Rows == nil:
		return Table{}, fmt.Errorf("prices %s: rows must be an array, got missing", name)
	}
	table.FetchedAt, table.Sources, table.Rows = *doc.FetchedAt, *doc.Sources, *doc.Rows
	if err := validate(table); err != nil {
		return Table{}, fmt.Errorf("prices %s: %w", name, err)
	}
	return table, nil
}

// decodeOverride reads and validates a pfm.prices.json: version 2 and rows of
// the prices.json shape, each keyed as resolution keys a model id, so no
// override row sits in the table matching nothing.
func decodeOverride(content []byte, name string) ([]Row, error) {
	var doc struct {
		Version *int   `json:"version"`
		Rows    *[]Row `json:"rows"`
	}
	if err := strictUnmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("prices %s: %w", name, err)
	}
	if err := checkVersion(doc.Version); err != nil {
		return nil, fmt.Errorf("prices %s: %w", name, err)
	}
	if doc.Rows == nil {
		return nil, fmt.Errorf("prices %s: rows must be an array, got missing", name)
	}
	if err := validateRows(*doc.Rows); err != nil {
		return nil, fmt.Errorf("prices %s: %w", name, err)
	}
	for _, row := range *doc.Rows {
		if id, ok := NormalizeID(row.Key); !ok || id != row.Key {
			return nil, fmt.Errorf("prices %s: %s: no model id resolves to this key; write it as %q", name, row.Key, id)
		}
	}
	return *doc.Rows, nil
}

func validate(table Table) error {
	if table.Version != Version {
		return fmt.Errorf("version must be %d, got %d", Version, table.Version)
	}
	if table.FetchedAt == "" {
		return errors.New("fetched_at is missing")
	}
	if _, err := time.Parse(time.RFC3339, table.FetchedAt); err != nil {
		return fmt.Errorf("fetched_at %q is not an RFC 3339 time", table.FetchedAt)
	}
	if len(table.Sources) == 0 {
		return errors.New("sources must name at least one publisher page")
	}
	for i, source := range table.Sources {
		if source.Provider == "" || !strings.HasPrefix(source.URL, "https://") {
			return fmt.Errorf("source %d: a provider and an https url are required", i)
		}
	}
	return validateRows(table.Rows)
}

func validateRows(rows []Row) error {
	seen := make(map[string]bool, len(rows))
	for i := range rows {
		row := &rows[i]
		switch {
		case row.Key == "":
			return fmt.Errorf("row %d: key is missing", i)
		case row.Key != strings.ToLower(strings.TrimSpace(row.Key)):
			return fmt.Errorf("row %d: key %q must be lowercase without surrounding spaces", i, row.Key)
		case seen[row.Key]:
			return fmt.Errorf("duplicate key %s", row.Key)
		case row.Engine != EngineClaude && row.Engine != EngineCodex:
			return fmt.Errorf("%s: engine %q is not %s or %s", row.Key, row.Engine, EngineClaude, EngineCodex)
		case row.Source != "":
			return fmt.Errorf("%s: source is set by pfm, never written in a file", row.Key)
		}
		seen[row.Key] = true
		if err := checkRates(row.Key, "", row.Engine, row.Rates, true); err != nil {
			return err
		}
		if row.Long == nil {
			continue
		}
		if row.Long.Above <= 0 {
			return fmt.Errorf("%s: long above must be a positive token count", row.Key)
		}
		if err := checkRates(row.Key, "long ", row.Engine, row.Long.Rates, false); err != nil {
			return err
		}
	}
	return nil
}

// checkRates refuses a missing required rate, a rate foreign to the engine and
// a negative one; tier prefixes the column name in an error.
func checkRates(key, tier, rowEngine string, rates Rates, required bool) error {
	for _, column := range []struct {
		name   string
		value  *float64
		engine string
	}{
		{"in", rates.In, ""},
		{"out", rates.Out, ""},
		{"hit", rates.Hit, EngineClaude},
		{"w5m", rates.W5m, EngineClaude},
		{"w1h", rates.W1h, EngineClaude},
		{"cached", rates.Cached, EngineCodex},
	} {
		switch {
		case column.value == nil && required && column.engine == "":
			return fmt.Errorf("%s: %s%s is required", key, tier, column.name)
		case column.value == nil:
		case column.engine != "" && column.engine != rowEngine:
			return fmt.Errorf("%s: %s%s is not a %s column", key, tier, column.name, rowEngine)
		case *column.value < 0 || math.IsInf(*column.value, 0) || math.IsNaN(*column.value):
			return fmt.Errorf("%s: %s%s must be a non-negative number", key, tier, column.name)
		}
	}
	return nil
}

// sortedRows is a copy of rows, claude first, then by key.
func sortedRows(rows []Row) []Row {
	sorted := append([]Row(nil), rows...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if (sorted[i].Engine == EngineClaude) != (sorted[j].Engine == EngineClaude) {
			return sorted[i].Engine == EngineClaude
		}
		return sorted[i].Key < sorted[j].Key
	})
	return sorted
}

func compactJSON(value any) (string, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return "", err
	}
	return strings.TrimSuffix(buffer.String(), "\n"), nil
}

// Encode is the canonical prices.json bytes: rows sorted claude first, then by
// key, one compact row per line.
func Encode(table Table) ([]byte, error) {
	if err := validate(table); err != nil {
		return nil, fmt.Errorf("encode prices: %w", err)
	}
	fetchedAt, err := compactJSON(table.FetchedAt)
	if err != nil {
		return nil, fmt.Errorf("encode prices fetched_at: %w", err)
	}
	var out strings.Builder
	fmt.Fprintf(&out, "{\n  \"version\": %d,\n  \"fetched_at\": %s,\n  \"sources\": [\n", Version, fetchedAt)
	if err := writeLines(&out, table.Sources); err != nil {
		return nil, err
	}
	out.WriteString("  ],\n  \"rows\": [\n")
	if err := writeLines(&out, sortedRows(table.Rows)); err != nil {
		return nil, err
	}
	out.WriteString("  ]\n}\n")
	return []byte(out.String()), nil
}

func writeLines[T any](out *strings.Builder, values []T) error {
	for i, value := range values {
		line, err := compactJSON(value)
		if err != nil {
			return fmt.Errorf("encode prices: %w", err)
		}
		out.WriteString("    " + line)
		if i < len(values)-1 {
			out.WriteString(",")
		}
		out.WriteString("\n")
	}
	return nil
}

// SameRates reports whether two tables carry the same sources and rows,
// whatever their fetch times and row order.
func SameRates(a, b Table) bool {
	if !reflect.DeepEqual(a.Sources, b.Sources) {
		return false
	}
	return reflect.DeepEqual(unsourced(a.Rows), unsourced(b.Rows))
}

func unsourced(rows []Row) []Row {
	sorted := sortedRows(rows)
	for i := range sorted {
		sorted[i].Source = ""
	}
	return sorted
}

// NormalizeID is a model id as the table keys it, before the snapshot rule;
// ok is false for an id no row can price.
func NormalizeID(modelID string) (string, bool) {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if rest, found := strings.CutPrefix(id, "anthropic/"); found {
		if !strings.HasPrefix(rest, "claude-") {
			return "", false
		}
		id = rest
	} else if rest, found := strings.CutPrefix(id, "openai/"); found {
		if strings.HasPrefix(rest, "claude-") {
			return "", false
		}
		id = rest
	}
	id = bedrockPrefix.ReplaceAllString(id, "")
	if open := strings.IndexByte(id, '['); open > 0 && strings.HasSuffix(id, "]") {
		id = id[:open]
	}
	if at := strings.IndexByte(id, '@'); at > 0 {
		id = id[:at]
	}
	if strings.HasPrefix(id, "claude-") {
		id = strings.ReplaceAll(bedrockVersion.ReplaceAllString(id, ""), ".", "-")
	}
	return id, id != ""
}

// Resolve returns the row that prices modelID: the row keyed by its normalized
// id, else by that id without a trailing snapshot date. ok is false for an
// unpriced model.
func (t Table) Resolve(modelID string) (Row, bool) {
	id, ok := NormalizeID(modelID)
	if !ok {
		return Row{}, false
	}
	if row, found := t.row(id); found {
		return row, true
	}
	if undated := snapshotDate.ReplaceAllString(id, ""); undated != id {
		return t.row(undated)
	}
	return Row{}, false
}

func (t Table) row(key string) (Row, bool) {
	for _, row := range t.Rows {
		if row.Key == key {
			return row, true
		}
	}
	return Row{}, false
}

// RatesAt is the tier that prices a call of context tokens.
func (r Row) RatesAt(context int64) Rates {
	if r.Long != nil && context > r.Long.Above {
		return r.Long.Rates
	}
	return r.Rates
}

// ClaudeCost prices one response in USD at the tier its context selects
// (input + cache read + cache write tokens). ok is false when the row is not
// a Claude row or a nonzero count has no published rate.
func (r Row) ClaudeCost(usage ClaudeUsage) (float64, bool) {
	parts, ok := r.ClaudeCostParts(usage)
	return parts.Total(), ok
}

// ClaudeCostParts is ClaudeCost by billing column, each count times its rate
// per million; an unpublished rate on a zero count costs nothing.
func (r Row) ClaudeCostParts(usage ClaudeUsage) (ClaudeParts, bool) {
	if r.Engine != EngineClaude {
		return ClaudeParts{}, false
	}
	rates := r.RatesAt(usage.Context())
	var parts ClaudeParts
	for _, part := range []struct {
		count int64
		rate  *float64
		usd   *float64
	}{
		{usage.Input, rates.In, &parts.Input},
		{usage.Output, rates.Out, &parts.Output},
		{usage.Write5m, rates.W5m, &parts.Write5m},
		{usage.Write1h, rates.W1h, &parts.Write1h},
		{usage.CacheRead, rates.Hit, &parts.CacheRead},
	} {
		if part.count == 0 {
			continue
		}
		if part.rate == nil {
			return ClaudeParts{}, false
		}
		*part.usd = float64(part.count) * *part.rate / 1e6
	}
	return parts, true
}

// CodexCost prices a Codex thread's usage in USD at the tier context selects:
// uncached input, cached input and output, each at its rate per million. ok
// is false when the row is not a Codex row or a nonzero count has no
// published rate.
func (r Row) CodexCost(usage CodexUsage, context int64) (float64, bool) {
	parts, ok := r.codexCostParts(usage, context)
	// Summed in the order the Codex reports always have: uncached, cached, output.
	return parts.Input + parts.CacheRead + parts.Output, ok
}

// codexCostParts is CodexCost by billing column: the uncached input in Input,
// the cached input in CacheRead, the output in Output, no cache write.
func (r Row) codexCostParts(usage CodexUsage, context int64) (ClaudeParts, bool) {
	if r.Engine != EngineCodex {
		return ClaudeParts{}, false
	}
	rates := r.RatesAt(context)
	uncached := max(usage.Input-usage.Cached, 0)
	perMillion := [3]float64{}
	for i, part := range []struct {
		count int64
		rate  *float64
	}{{uncached, rates.In}, {usage.Cached, rates.Cached}, {usage.Output, rates.Out}} {
		if part.rate != nil {
			perMillion[i] = *part.rate / 1e6
		} else if part.count > 0 {
			return ClaudeParts{}, false
		}
	}
	// Each product rounds before the sum (float64): a fused multiply-add
	// would move the last bits by architecture.
	return ClaudeParts{
		Input:     float64(float64(uncached) * perMillion[0]),
		CacheRead: float64(float64(usage.Cached) * perMillion[1]),
		Output:    float64(float64(usage.Output) * perMillion[2]),
	}, true
}

// ResponseCostParts prices one response of a Claude Code transcript, whichever
// model wrote it, by billing column. A Claude row prices it as ClaudeCostParts.
// A Codex row is a model the model-gateway's chatgpt route served: the gateway
// writes its usage Anthropic-shaped (input_tokens = total - cached - written,
// cache read = cached, cache creation = written), so the request's whole input
// is Context() and selects the tier, the input and cache read price as
// CodexCost's columns, and a written token, Codex having no cache-write
// premium, bills at in in its own write column, beside the tokens it prices.
func (r Row) ResponseCostParts(usage ClaudeUsage) (ClaudeParts, bool) {
	if r.Engine != EngineCodex {
		return r.ClaudeCostParts(usage)
	}
	context := usage.Context()
	parts, ok := r.codexCostParts(
		CodexUsage{Input: usage.Input + usage.CacheRead, Cached: usage.CacheRead, Output: usage.Output}, context)
	if !ok || usage.Write5m+usage.Write1h == 0 {
		return parts, ok
	}
	in := r.RatesAt(context).In
	if in == nil {
		return ClaudeParts{}, false
	}
	perMillion := *in / 1e6
	parts.Write5m = float64(float64(usage.Write5m) * perMillion)
	parts.Write1h = float64(float64(usage.Write1h) * perMillion)
	return parts, true
}

// ResponseRequestsCostParts is ResponseCostParts request by request, each at
// the tier its own context selects, the columns summed; ok is false when any
// request cannot be priced.
func (r Row) ResponseRequestsCostParts(requests []ClaudeUsage) (ClaudeParts, bool) {
	var parts ClaudeParts
	for _, request := range requests {
		priced, ok := r.ResponseCostParts(request)
		if !ok {
			return ClaudeParts{}, false
		}
		parts = parts.plus(priced)
	}
	return parts, true
}
