package harvest

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

const (
	maxLocalizedImages = 50
	maxImageBytes      = 10 * 1024 * 1024
)

var markdownImageRE = regexp.MustCompile(`!\[[^\]]*\]\(\s*([^\s)]+)`)

// LocalizeImages downloads article images referenced by Markdown and rewrites
// successful links to immutable cache paths. It intentionally skips data URIs,
// favicons, sprites, non-image responses, and anything beyond the oracle's
// fifty-image/ten-megabyte limits. Failed links remain untouched.
func (h *Harvester) LocalizeImages(ctx context.Context, markdown, baseSource string) (string, error) {
	if markdown == "" {
		return markdown, nil
	}
	base, err := url.Parse(baseSource)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return markdown, fmt.Errorf("invalid image base URL %q", baseSource)
	}
	matches := markdownImageRE.FindAllStringSubmatchIndex(markdown, -1)
	if len(matches) == 0 {
		return markdown, nil
	}
	seen := map[string]bool{}
	replacements := map[string]string{}
	count := 0
	var targets []string
	for _, match := range matches {
		if count >= maxLocalizedImages {
			break
		}
		remote := markdown[match[2]:match[3]]
		if remote == "" || strings.HasPrefix(strings.ToLower(remote), "data:") || seen[remote] || isImageIcon(remote) {
			continue
		}
		seen[remote] = true
		count++
		u, err := base.Parse(remote)
		if err != nil || (u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS) {
			continue
		}
		targets = append(targets, remote+"\x00"+u.String())
	}
	// Image links are independent. A small semaphore keeps a page with many
	// figures from opening an unbounded number of sockets while preserving the
	// deterministic replacement pass below.
	sem := make(chan struct{}, 6)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, target := range targets {
		parts := strings.SplitN(target, "\x00", 2)
		remote, full := parts[0], parts[1]
		wg.Add(1)
		go func() {
			defer wg.Done()
			// A recovered panic (F1) is swallowed exactly like every other
			// failure this loop already answers silently (fetchErr, a bad
			// status, a non-image kind — none of them log either): the C23
			// activity-log ratchet (arch-check.sh) refuses a new bare
			// log call, and this function has no per-item error
			// report to attach one to either way.
			defer recoverItem(func(error) {})
			sem <- struct{}{}
			defer func() { <-sem }()
			// This image was REACHED from baseSource — the harvested page — so
			// it carries that page's own URL as Referer (F-referer), never the
			// Google provenance one: a hotlink-protected host allows a same-site
			// Referer and 403s a foreign one.
			body, status, contentType, fetchErr := getBodyWithHeaders(
				ctx,
				h.client,
				full,
				h.userAgent,
				map[string]string{headerReferer: baseSource},
				maxImageBytes+1,
			)
			if fetchErr != nil || status >= 400 || len(body) == 0 || len(body) > maxImageBytes {
				return
			}
			kind := classifyKind(full, contentType, body)
			if !isImageKind(kind) {
				return
			}
			stored := h.storeBinary(full, kind, "image-localize", body, false)
			if stored.Error == "" && stored.Path != "" {
				mu.Lock()
				replacements[remote] = stored.Path
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(replacements) == 0 {
		return markdown, nil
	}
	return markdownImageRE.ReplaceAllStringFunc(markdown, func(fragment string) string {
		sub := markdownImageRE.FindStringSubmatch(fragment)
		if len(sub) < 2 {
			return fragment
		}
		if path := replacements[sub[1]]; path != "" {
			return strings.Replace(fragment, sub[1], path, 1)
		}
		return fragment
	}), nil
}

func isImageIcon(raw string) bool {
	u, err := url.Parse(raw)
	path := strings.ToLower(raw)
	if err == nil {
		path = strings.ToLower(u.Path)
	}
	base := path
	if slash := strings.LastIndexByte(base, '/'); slash >= 0 {
		base = base[slash+1:]
	}
	return strings.HasSuffix(base, ".ico") || strings.Contains(base, "favicon") || strings.Contains(base, "sprite")
}
