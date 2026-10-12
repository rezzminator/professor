package harvest

import (
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rezzminator/professor/pfm/internal/clock"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

// Cache is a type-partitioned markdown cache. Images and archives do not
// expire; publication-like kinds do.
type Cache struct {
	root  string
	ttl   time.Duration
	clock clock.Clock
}

func newCache(root string, ttl time.Duration, clocks ...clock.Clock) *Cache {
	watch := clock.Real
	if len(clocks) > 0 && clocks[0] != nil {
		watch = clocks[0]
	}
	return &Cache{root: root, ttl: ttl, clock: watch}
}

func defaultHarvestCacheDir() (string, error) {
	// The default cache lives in exactly ONE place: paths.HarvesterCacheDir,
	// <home>/.professor/.harvester-cache (beside pfm's other home state such as
	// ~/.professor/agents; `pfm install` moves a pre-rename .cache there). It must
	// never follow the process's working directory — the cwd-walking default
	// this replaces grew a stray .cache in whatever project a chat happened
	// to fetch from. A home that cannot be resolved is an error, never a
	// fallback to some other directory.
	home, err := paths.Home()
	if err != nil {
		return "", fmt.Errorf("resolve harvester cache home: %w", err)
	}
	return paths.HarvesterCacheDir(home), nil
}

// CacheRoot is the cache directory New uses: the configured dir
// (harvester.config.json cache.dir) when set, else the one default
// <home>/.professor/.harvester-cache. Doctor and the MCP adapter resolve through it so
// every consumer agrees on one directory.
func CacheRoot(configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return filepath.Clean(configured), nil
	}
	return defaultHarvestCacheDir()
}

// CacheKey returns a stable type-specific filesystem key.
func CacheKey(source, kind string) string {
	base := regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*://`).ReplaceAllString(source, "")
	base = regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(base, "_")
	base = strings.Trim(regexp.MustCompile(`_+`).ReplaceAllString(base, "_"), "_")
	if len(base) > 150 {
		base = base[:150]
	}
	sum := sha1.Sum([]byte(source))
	return filepath.Join(kind, base+"__"+hex.EncodeToString(sum[:])[:10]+".md")
}

func (c *Cache) path(source, kind string) string {
	return filepath.Join(c.root, CacheKey(source, kind))
}

func (c *Cache) load(source, kind string) (body string, meta map[string]string, path string, ok bool) {
	path = c.path(source, kind)
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nil, path, false
	}
	meta, body = readFrontmatter(string(raw))
	if c.stale(path, kind, meta) {
		return "", meta, path, false
	}
	if kind == kindHTML && contentChars(body) < 200 {
		return "", meta, path, false
	}
	return body, meta, path, true
}

func (c *Cache) loadAny(
	source string,
	kinds []string,
) (body, kind string, meta map[string]string, path string, ok bool) {
	for _, candidate := range kinds {
		body, meta, path, ok = c.load(source, candidate)
		if ok {
			return body, candidate, meta, path, true
		}
	}
	return "", "", nil, "", false
}

func (c *Cache) stale(path, kind string, meta map[string]string) bool {
	if !volatileKinds[kind] || c.ttl <= 0 {
		return false
	}
	stamp, err := time.Parse(time.RFC3339, meta["fetched_at"])
	if err != nil {
		info, statErr := os.Stat(path)
		if statErr != nil {
			return false
		}
		return c.clock.Now().Sub(info.ModTime()) > c.ttl
	}
	return c.clock.Now().Sub(stamp) > c.ttl
}

// save stores body with its provenance and facts (the converter's metadata and
// gaps, splitArtifact) as frontmatter. status is the HTTP status of the rung
// that delivered it; 0 (a local document, or a status never learned) writes no
// status line, so a later hit reports none rather than a made-up one.
func (c *Cache) save(
	source, kind, method, body string,
	status int,
	rungs []string,
	facts map[string]string,
) (path string, returnErr error) {
	path = c.path(source, kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return path, fmt.Errorf("create cache directory: %w", err)
	}
	fields := map[string]string{}
	for key, value := range facts {
		fields[key] = value
	}
	fields[keySource] = frontmatterSourceHarvester
	fields["url"] = source
	fields[keyKind] = kind
	fields["method"] = method
	fields["rungs"] = strings.Join(rungs, ", ")
	fields["fetched_at"] = c.clock.Now().UTC().Format(time.RFC3339)
	fields["chars"] = strconv.Itoa(contentChars(body))
	fields["token_count"] = strconv.Itoa(EstimateTokens(body))
	if status > 0 {
		fields["http_status"] = strconv.Itoa(status)
	}
	meta := renderFrontmatter(fields)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".harvest-*")
	if err != nil {
		return path, fmt.Errorf("create cache temp: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		if err := os.Remove(tmpName); err != nil && !errors.Is(err, fs.ErrNotExist) {
			returnErr = errors.Join(returnErr, fmt.Errorf("remove cache temp %s: %w", tmpName, err))
		}
	}()
	if _, err := tmp.WriteString(meta + body); err != nil {
		_ = tmp.Close()
		return path, fmt.Errorf("write cache: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return path, fmt.Errorf("protect cache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return path, fmt.Errorf("close cache: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return path, fmt.Errorf("install cache: %w", err)
	}
	return path, nil
}

func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	// Weighted by share, not by the presence of one character: CJK/kana/Hangul
	// runes cost 1.3x each; the REMAINING (non-CJK) runes cost 1/1.8 when
	// symbols are >=5% of THOSE runes (code), else 1/2 (prose) - always
	// rounded up. A pure-CJK, pure-prose or pure-code text reduces to its
	// old whole-text rate exactly; only mixed text changes.
	runes := []rune(text)
	cjk := 0
	symbols := 0
	for _, r := range runes {
		if (r >= 0x3040 && r <= 0x30ff) || (r >= 0x3400 && r <= 0x9fff) || (r >= 0xf900 && r <= 0xfaff) ||
			(r >= 0xac00 && r <= 0xd7af) ||
			(r >= 0xff00 && r <= 0xffef) {
			cjk++
			continue
		}
		switch r {
		case '{',
			'}',
			'[',
			']',
			'(',
			')',
			'<',
			'>',
			';',
			'=',
			'+',
			'-',
			'*',
			'/',
			'\\',
			'|',
			'&',
			'^',
			'%',
			'$',
			'#',
			'@',
			'~',
			'`',
			'_':
			symbols++
		}
	}
	n := len(runes)
	nonCJK := n - cjk
	total := float64(cjk) * 1.3
	if nonCJK > 0 {
		if float64(symbols)/float64(nonCJK) >= 0.05 {
			total += float64(nonCJK) / 1.8
		} else {
			total += float64(nonCJK) / 2
		}
	}
	return int(total + 0.999999)
}

// InlineTruncationNote ends inline content truncateInline cut short; a
// renderer that states the truncation itself cuts it off (strings.CutSuffix).
const InlineTruncationNote = "\n\n[content truncated; read the cached path for the complete artifact]"

func truncateInline(body string, limit int) string {
	if limit <= 0 {
		return body
	}
	runes := []rune(body)
	if len(runes) <= limit {
		return body
	}
	return string(runes[:limit]) + InlineTruncationNote
}

var volatileKinds = map[string]bool{
	kindHTML: true, kindPDF: true, kindDOCX: true, kindXLSX: true,
	kindPPTX: true, kindCSV: true, kindJSON: true, kindTXT: true,
}

type negativeCache struct {
	mu        sync.Mutex
	ttl       time.Duration
	transient time.Duration
	clock     clock.Clock
	entries   map[string]negativeEntry
	sweepAt   int // put sweeps expired entries once the cache holds this many
}
type negativeEntry struct {
	at     time.Time
	ttl    time.Duration
	result Result
}

func newNegativeCache(ttl, transient time.Duration, clocks ...clock.Clock) *negativeCache {
	watch := clock.Real
	if len(clocks) > 0 && clocks[0] != nil {
		watch = clocks[0]
	}
	return &negativeCache{ttl: ttl, transient: transient, clock: watch, entries: map[string]negativeEntry{}}
}

func (c *negativeCache) get(key string) (Result, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || c.clock.Now().Sub(e.at) >= e.ttl {
		if ok {
			delete(c.entries, key)
		}
		return Result{}, false
	}
	// Return a shallow copy with the retry window annotation. The stored result
	// stays byte-for-byte unchanged so its diagnostic class and first receipt
	// remain stable while a repeated request learns when retrying is worthwhile.
	result := e.result
	if result.Error != "" {
		remaining := e.ttl - c.clock.Now().Sub(e.at)
		seconds := int((remaining + 500*time.Millisecond) / time.Second)
		if seconds < 0 {
			seconds = 0
		}
		result.Error = fmt.Sprintf("%s (recently failed; cached — retry after %ds)", result.Error, seconds)
	}
	return result, true
}

// negativeSweepFloor is the entry count below which put never sweeps expired
// failures; past it a sweep runs each time the cache doubles.
const negativeSweepFloor = 64

// drop forgets key's failure once a read of its source has succeeded.
func (c *negativeCache) drop(key string) {
	c.mu.Lock()
	delete(c.entries, key)
	c.mu.Unlock()
}

func (c *negativeCache) put(key string, result Result) {
	c.mu.Lock()
	ttl := c.ttl
	if result.HTTPStatus == http.StatusTooManyRequests || result.ErrorKind == errorKindTimeout ||
		result.ErrorKind == errorKindConnect ||
		result.ErrorKind == errorKindDNS {
		ttl = c.transient
	}
	now := c.clock.Now()
	if len(c.entries) >= c.sweepAt { // a key never read again would otherwise stay for good
		for stale := range c.entries {
			if now.Sub(c.entries[stale].at) >= c.entries[stale].ttl {
				delete(c.entries, stale)
			}
		}
		c.sweepAt = max(2*len(c.entries), negativeSweepFloor)
	}
	c.entries[key] = negativeEntry{at: now, ttl: ttl, result: result}
	c.mu.Unlock()
}
