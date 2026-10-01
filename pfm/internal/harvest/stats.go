package harvest

// Fetch-outcome scoreboard (parity with harvester stats.py): an append-only
// JSONL of every terminal dispatch outcome under <CacheDir>/stats.jsonl.
// Aggregated, it answers what the per-artifact `rungs:` trace cannot: WHICH
// sources win, which failure kinds dominate, and a source's real success rate
// over time — so a dying OA provider or a newly walled publisher is VISIBLE
// instead of silently rotting.
//
// Nothing here raises into the fetch path: a failed append is logged and dropped.

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
)

const statsFilename = "stats.jsonl"

type statRecord struct {
	TS     string `json:"ts"`
	Item   string `json:"item"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (h *Harvester) recordStat(item string, result Result) {
	dir := h.options.CacheDir
	if dir == "" {
		return
	}
	rec := statRecord{
		TS:   h.nowClock().Now().UTC().Format("2006-01-02T15:04:05Z"),
		Item: truncateOutputRunes(item, 500),
		OK:   result.Error == "",
	}
	switch {
	case rec.OK:
		rec.Detail = truncateOutputRunes(result.Method, 200)
	case result.ErrorKind != "":
		rec.Detail = truncateOutputRunes(result.ErrorKind, 200)
	default:
		rec.Detail = resultDetailError
	}
	line, err := json.Marshal(rec)
	if err != nil {
		log.Printf("harvest: stats marshal failed: %v", err)
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Printf("harvest: create stats directory %s: %v", dir, err)
		return
	}
	file, err := os.OpenFile(filepath.Join(dir, statsFilename), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		log.Printf("harvest: stats append failed: %v", err)
		return
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			log.Printf("harvest: stats close failed: %v", closeErr)
		}
	}()
	if _, err := file.Write(append(line, '\n')); err != nil {
		log.Printf("harvest: stats write failed: %v", err)
	}
}
