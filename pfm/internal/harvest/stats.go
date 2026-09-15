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
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
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
		TS:   time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Item: truncateRunes(item, 500),
		OK:   result.Error == "",
	}
	switch {
	case rec.OK:
		rec.Detail = truncateRunes(result.Method, 200)
	case result.ErrorKind != "":
		rec.Detail = truncateRunes(result.ErrorKind, 200)
	default:
		rec.Detail = "error"
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

// StatBucket aggregates one detail's outcomes.
type StatBucket struct {
	Total int     `json:"total"`
	OK    int     `json:"ok"`
	Rate  float64 `json:"rate"`
}

// SummarizeStats reads the scoreboard tail from *cacheDir* and buckets the last
// records by detail → {total, ok, rate}. Missing/empty data is a healthy empty
// map; malformed records are counted under _corrupt, and an all-corrupt file
// returns an error so failed enumeration never renders as absence.
func SummarizeStats(cacheDir string, lastN int) (summary map[string]*StatBucket, returnErr error) {
	path := filepath.Join(cacheDir, statsFilename)
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*StatBucket{}, nil
		}
		return nil, fmt.Errorf("open stats file: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("close stats file: %w", err))
		}
	}()
	if lastN <= 0 {
		lastN = 5000
	}
	// Tail read: skip to near the end, drop the possibly partial line.
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat stats file: %w", err)
	}
	if size := info.Size(); size > int64(lastN)*512 {
		if _, err := file.Seek(size-int64(lastN)*512, 0); err == nil {
			reader := bufio.NewReader(file)
			_, _ = reader.ReadString('\n')
		} else {
			if _, seekErr := file.Seek(0, 0); seekErr != nil {
				return nil, fmt.Errorf("seek stats file: %w", seekErr)
			}
		}
	}
	buckets := map[string]*StatBucket{}
	corrupt := 0
	parsed := 0
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var rec statRecord
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			corrupt++
			continue
		}
		parsed++
		detail := rec.Detail
		if detail == "" {
			if rec.OK {
				detail = "ok"
			} else {
				detail = "unknown"
			}
		}
		bucket, found := buckets[detail]
		if !found {
			bucket = &StatBucket{}
			buckets[detail] = bucket
		}
		bucket.Total++
		if rec.OK {
			bucket.OK++
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan stats file: %w", err)
	}
	if corrupt > 0 {
		buckets["_corrupt"] = &StatBucket{Total: corrupt}
	}
	if parsed == 0 && corrupt > 0 {
		return buckets, fmt.Errorf("stats file contains %d malformed record(s) and no valid records", corrupt)
	}
	for _, bucket := range buckets {
		if bucket.Total > 0 {
			bucket.Rate = float64(bucket.OK) / float64(bucket.Total)
		}
	}
	return buckets, nil
}
