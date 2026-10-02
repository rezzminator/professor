// Package pricing owns the model price table: the embedded prices.json, the
// optional pfm.prices.json override that merges into it by model key, and the
// resolution of a model id to the row of its longest matching pattern. Rates
// are USD per million tokens.
package pricing

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/engine"
)

var (
	// EngineClaude rows carry hit, w5m and w1h; EngineCodex rows carry cached.
	EngineClaude = engine.MustLookup(engine.Claude).LongName
	EngineCodex  = engine.MustLookup(engine.Codex).LongName
)

const (
	// SourceShipped marks a row of the embedded table, SourceOverride one that
	// came from pfm.prices.json.
	SourceShipped  = "shipped"
	SourceOverride = "override"

	tableVersion = 1
)

// Row is one model's rates. A column foreign to the row's engine stays 0.
type Row struct {
	Key, Engine string
	Match       []string
	In, Out     float64
	Hit         float64 // claude: cache read
	W5m, W1h    float64 // claude: 5-minute and 1-hour cache write
	Cached      float64 // codex: cached input, a subset of input
	LongIn      float64 // multiplier on a call whose context passes 200K tokens
	LongOut     float64
	Source      string
}

// Override reports the pfm.prices.json that was merged: its absolute path and
// how many rows the file holds.
type Override struct {
	Path string
	Rows int
}

// Table is the effective price table; Override is nil when no override file
// exists.
type Table struct {
	Rows     []Row
	Override *Override
}

// rawRow is a row as the file spells it. A pointer rate tells a missing column
// from a zero one.
type rawRow struct {
	Key     string   `json:"key"`
	Engine  string   `json:"engine"`
	Match   []string `json:"match"`
	In      *float64 `json:"in"`
	Out     *float64 `json:"out"`
	Hit     *float64 `json:"hit"`
	W5m     *float64 `json:"w5m"`
	W1h     *float64 `json:"w1h"`
	Cached  *float64 `json:"cached"`
	LongIn  *float64 `json:"long_in"`
	LongOut *float64 `json:"long_out"`
}

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

// decodeTable decodes and validates one table file. source names the file in
// every error; rowSource is the Source of the rows it yields.
func decodeTable(content []byte, source, rowSource string) ([]Row, error) {
	var doc struct {
		Version *int               `json:"version"`
		Rows    *[]json.RawMessage `json:"rows"`
	}
	if err := strictUnmarshal(content, &doc); err != nil {
		return nil, fmt.Errorf("prices %s: %w", source, err)
	}
	switch {
	case doc.Version == nil:
		return nil, fmt.Errorf("prices %s: version must be %d, got missing", source, tableVersion)
	case *doc.Version != tableVersion:
		return nil, fmt.Errorf("prices %s: version must be %d, got %d", source, tableVersion, *doc.Version)
	case doc.Rows == nil:
		return nil, fmt.Errorf("prices %s: rows must be an array, got missing", source)
	}

	rows := make([]Row, 0, len(*doc.Rows))
	firstRow := map[string]int{}
	for i, message := range *doc.Rows {
		row, err := decodeRow(message, rowSource)
		if err == nil {
			if first, dup := firstRow[row.Key]; dup {
				err = fmt.Errorf("key must be unique, got a repeat of row %d", first)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("prices %s: row %d (%s): %w", source, i, peekKey(message), err)
		}
		firstRow[row.Key] = i
		rows = append(rows, row)
	}
	return rows, nil
}

// peekKey is the key of a row that may not decode, for the error that names it.
func peekKey(message json.RawMessage) string {
	var peek struct {
		Key string `json:"key"`
	}
	_ = json.Unmarshal(message, &peek)
	return peek.Key
}

type column struct {
	name  string
	value *float64
	// own is true for a column the engine carries; a column of another engine
	// must be absent.
	own bool
}

// decodeRow decodes one row and checks it against its engine.
func decodeRow(message json.RawMessage, rowSource string) (Row, error) {
	var raw rawRow
	if err := strictUnmarshal(message, &raw); err != nil {
		return Row{}, err
	}
	if raw.Key == "" {
		return Row{}, errors.New(`key must be non-empty, got ""`)
	}
	if raw.Engine != EngineClaude && raw.Engine != EngineCodex {
		return Row{}, fmt.Errorf("engine must be claude or codex, got %q", raw.Engine)
	}
	if len(raw.Match) == 0 {
		return Row{}, errors.New("match must hold at least one pattern, got none")
	}
	for i, pattern := range raw.Match {
		if pattern == "" {
			return Row{}, fmt.Errorf(`match[%d] must be a non-empty pattern, got ""`, i)
		}
	}

	claude := raw.Engine == EngineClaude
	columns := []column{
		{"in", raw.In, true},
		{"out", raw.Out, true},
		{"hit", raw.Hit, claude},
		{"w5m", raw.W5m, claude},
		{"w1h", raw.W1h, claude},
		{"cached", raw.Cached, !claude},
		{"long_in", raw.LongIn, true},
		{"long_out", raw.LongOut, true},
	}
	for _, c := range columns {
		switch {
		case c.own && c.value == nil:
			return Row{}, fmt.Errorf("%s must be present on a %s row, got missing", c.name, raw.Engine)
		case !c.own && c.value != nil:
			return Row{}, fmt.Errorf("%s must be absent on a %s row, got %s", c.name, raw.Engine, formatRate(*c.value))
		case c.value != nil && *c.value < 0:
			return Row{}, fmt.Errorf("%s must be >= 0, got %s", c.name, formatRate(*c.value))
		}
	}
	return Row{
		Key: raw.Key, Engine: raw.Engine, Match: raw.Match,
		In: rate(raw.In), Out: rate(raw.Out),
		Hit: rate(raw.Hit), W5m: rate(raw.W5m), W1h: rate(raw.W1h), Cached: rate(raw.Cached),
		LongIn: rate(raw.LongIn), LongOut: rate(raw.LongOut),
		Source: rowSource,
	}, nil
}

func rate(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

func formatRate(value float64) string { return strconv.FormatFloat(value, 'g', -1, 64) }

// checkPatterns rejects a pattern that two keys share, compared
// case-insensitively. A pattern repeated inside one row is harmless.
func checkPatterns(rows []Row, source string) error {
	owner := map[string]string{}
	for i := range rows {
		key := rows[i].Key
		for _, pattern := range rows[i].Match {
			folded := strings.ToLower(pattern)
			if other, taken := owner[folded]; taken && other != key {
				return fmt.Errorf("prices %s: pattern %q is on both key %q and key %q", source, folded, other, key)
			}
			owner[folded] = key
		}
	}
	return nil
}

// ClaudeUsage is one Claude response's billed token counts: cache writes split
// by TTL, a write with no TTL breakdown counted at the 5-minute rate.
type ClaudeUsage struct {
	Input, Output, CacheRead, Write5m, Write1h int64
}

// ClaudeCost prices one response at the row's base rates, in USD. The
// long-context premium is not applied, as the /tokens headline does not apply it.
func (r Row) ClaudeCost(usage ClaudeUsage) float64 {
	return (float64(usage.Input)*r.In + float64(usage.Output)*r.Out + float64(usage.CacheRead)*r.Hit +
		float64(usage.Write5m)*r.W5m + float64(usage.Write1h)*r.W1h) / 1e6
}

// Resolve returns the row that prices modelID: of every pattern, of every row,
// whose lowercased form is a substring of the lowercased id, the longest wins,
// and of two of equal length the one starting earlier in the id. Row order never
// changes the result. ok is false for an unpriced model. The returned Match
// shares storage with the table.
func (t Table) Resolve(modelID string) (Row, bool) {
	id := strings.ToLower(modelID)
	best, bestLen, bestAt := -1, 0, 0
	for i := range t.Rows {
		for _, pattern := range t.Rows[i].Match {
			pattern = strings.ToLower(pattern)
			if pattern == "" {
				continue
			}
			at := strings.Index(id, pattern)
			if at < 0 {
				continue
			}
			if best < 0 || len(pattern) > bestLen || (len(pattern) == bestLen && at < bestAt) {
				best, bestLen, bestAt = i, len(pattern), at
			}
		}
	}
	if best < 0 {
		return Row{}, false
	}
	return t.Rows[best], true
}

type jsonOverride struct {
	Path string `json:"path"`
	Rows int    `json:"rows"`
}

type jsonClaudeRow struct {
	Key     string   `json:"key"`
	Engine  string   `json:"engine"`
	Match   []string `json:"match"`
	In      float64  `json:"in"`
	Out     float64  `json:"out"`
	Hit     float64  `json:"hit"`
	W5m     float64  `json:"w5m"`
	W1h     float64  `json:"w1h"`
	LongIn  float64  `json:"long_in"`
	LongOut float64  `json:"long_out"`
	Source  string   `json:"source"`
}

type jsonCodexRow struct {
	Key     string   `json:"key"`
	Engine  string   `json:"engine"`
	Match   []string `json:"match"`
	In      float64  `json:"in"`
	Out     float64  `json:"out"`
	Cached  float64  `json:"cached"`
	LongIn  float64  `json:"long_in"`
	LongOut float64  `json:"long_out"`
	Source  string   `json:"source"`
}

func claudeDoc(row *Row, match []string) jsonClaudeRow {
	return jsonClaudeRow{
		Key: row.Key, Engine: row.Engine, Match: match, In: row.In, Out: row.Out,
		Hit: row.Hit, W5m: row.W5m, W1h: row.W1h, LongIn: row.LongIn, LongOut: row.LongOut,
		Source: row.Source,
	}
}

func codexDoc(row *Row, match []string) jsonCodexRow {
	return jsonCodexRow{
		Key: row.Key, Engine: row.Engine, Match: match, In: row.In, Out: row.Out,
		Cached: row.Cached, LongIn: row.LongIn, LongOut: row.LongOut, Source: row.Source,
	}
}

// JSON is the `pfm price --json` document, indented and ending in a newline.
// A row emits only the columns of its engine.
func (t Table) JSON() ([]byte, error) {
	doc := struct {
		Version  int           `json:"version"`
		Override *jsonOverride `json:"override"`
		Rows     []any         `json:"rows"`
	}{Version: tableVersion, Rows: make([]any, 0, len(t.Rows))}
	if t.Override != nil {
		doc.Override = &jsonOverride{Path: t.Override.Path, Rows: t.Override.Rows}
	}
	for i := range t.Rows {
		row := &t.Rows[i]
		match := row.Match
		if match == nil {
			match = []string{}
		}
		switch row.Engine {
		case EngineClaude:
			doc.Rows = append(doc.Rows, claudeDoc(row, match))
		case EngineCodex:
			doc.Rows = append(doc.Rows, codexDoc(row, match))
		default:
			return nil, fmt.Errorf(
				"prices: row %d (%s): engine must be claude or codex, got %q",
				i,
				row.Key,
				row.Engine,
			)
		}
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(doc); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
