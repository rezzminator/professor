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
	// The default cache lives in exactly ONE place: <home>/.professor/.cache
	// (beside pfm's other home state such as ~/.professor/agents). It must
	// never follow the process's working directory — the cwd-walking default
	// this replaces grew a stray .cache in whatever project a chat happened
	// to fetch from. A home that cannot be resolved is an error, never a
	// fallback to some other directory.
	home, err := paths.Home()
	if err != nil {
		return "", fmt.Errorf("resolve harvester cache home: %w", err)
	}
	return filepath.Join(home, ".professor", ".cache"), nil
}

// CacheRoot is the cache directory New uses: the configured dir
// (harvester.config.json cache.dir) when set, else the one default
// <home>/.professor/.cache. Doctor and the MCP adapter resolve through it so
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
	meta, body = parseCacheFrontmatter(string(raw))
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

func (c *Cache) save(source, kind, method, body string, rungs []string) (path string, returnErr error) {
	path = c.path(source, kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return path, fmt.Errorf("create cache directory: %w", err)
	}
	// Frontmatter is line-oriented. Escape hostile source/method values so a
	// URL cannot inject a second metadata key into a cache artifact.
	safe := func(value string) string {
		return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(value)
	}
	meta := fmt.Sprintf("---\nurl: %s\nfetched_at: %s\nsource: harvester\nmethod: %s\ntoken_count: %d\n",
		safe(source), c.clock.Now().UTC().Format(time.RFC3339), safe(method), EstimateTokens(body))
	if len(rungs) > 0 {
		meta += "rungs: " + strings.Join(rungs, ", ") + "\n"
	}
	meta += "---\n\n"
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

func parseCacheFrontmatter(raw string) (map[string]string, string) {
	meta := map[string]string{}
	if !strings.HasPrefix(raw, "---\n") {
		return meta, raw
	}
	end := strings.Index(raw[4:], "\n---\n")
	if end < 0 {
		return meta, raw
	}
	head := raw[4 : 4+end]
	for _, line := range strings.Split(head, "\n") {
		key, value, found := strings.Cut(line, ":")
		if found {
			value = strings.TrimSpace(value)
			value = strings.NewReplacer("%0D", "\r", "%0A", "\n", "%25", "%").Replace(value)
			meta[strings.TrimSpace(key)] = value
		}
	}
	body := raw[4+end+6:]
	body = strings.TrimPrefix(body, "\n")
	return meta, body
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

func truncateInline(body string, limit int) string {
	if limit <= 0 {
		return body
	}
	runes := []rune(body)
	if len(runes) <= limit {
		return body
	}
	return string(runes[:limit]) + "\n\n[content truncated; read the cached path for the complete artifact]"
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

func (c *negativeCache) put(key string, result Result) {
	c.mu.Lock()
	ttl := c.ttl
	if result.HTTPStatus == http.StatusTooManyRequests || result.ErrorKind == errorKindTimeout ||
		result.ErrorKind == errorKindConnect ||
		result.ErrorKind == errorKindDNS {
		ttl = c.transient
	}
	c.entries[key] = negativeEntry{at: c.clock.Now(), ttl: ttl, result: result}
	c.mu.Unlock()
}
