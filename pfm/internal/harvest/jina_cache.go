package harvest

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
)

// jinaNoCacheHeader is the jina reader's caching opt-out: the page is rendered
// fresh instead of answered from the reader's cache.
const (
	jinaNoCacheHeader = "X-No-Cache"
	jinaNoCacheValue  = "true"
)

// jinaCachedSnapshotWarning is the envelope line the jina reader answers a
// cached snapshot with: "Warning: This is a cached snapshot of the original
// page, consider retry with caching opt-out." A snapshot can be stale, or
// another page's copy cached under this address.
var jinaCachedSnapshotWarning = []byte("cached snapshot")

// jinaAnswer is the jina reader's answer for one page: headers are the extra
// request headers it was asked with, which the page's HTML check repeats
// (readerPageChecked); stale names why a cached snapshot stands, "" when the
// answer is a fresh render or no snapshot.
type jinaAnswer struct {
	body    []byte
	status  int
	err     error
	headers map[string]string
	stale   string
}

// jinaRead asks the jina reader for target: fresh from the start when the
// caller refreshes, else its default answer, asked again fresh when that
// answer is a cached snapshot. A fresh render that fails, or answers no
// usable content, keeps the snapshot, its gap naming why; a fresh answer
// still declaring a cached snapshot is named one too (snapshotDespiteOptOut).
func (h *Harvester) jinaRead(ctx context.Context, target string, refresh bool) jinaAnswer {
	fresh := map[string]string{jinaNoCacheHeader: jinaNoCacheValue}
	if refresh {
		body, status, _, err := getBodyWithHeaders(ctx, h.jina, target, h.userAgent, fresh, h.options.MaxBytes)
		return jinaAnswer{body: body, status: status, err: err, headers: fresh, stale: snapshotDespiteOptOut(body)}
	}
	body, status, _, err := getBody(ctx, h.jina, target, h.userAgent, h.options.MaxBytes)
	if err != nil || status >= http.StatusBadRequest || !jinaCachedSnapshot(body) {
		return jinaAnswer{body: body, status: status, err: err}
	}
	again, againStatus, _, againErr := getBodyWithHeaders(ctx, h.jina, target, h.userAgent, fresh, h.options.MaxBytes)
	why := ""
	switch {
	case againErr != nil:
		why = "the request failed: " + errorReasonClass(againErr, "fetch error")
	case againStatus >= http.StatusBadRequest:
		why = fmt.Sprintf("it answered HTTP %d", againStatus)
	case isChallenge(again, againStatus):
		why = "it was served a challenge"
	case jinaTargetError(again) != 0:
		why = fmt.Sprintf("the origin answered it HTTP %d", jinaTargetError(again))
	case !usableContent(pageText(stripJinaEnvelope(string(again))), kindHTML):
		why = "it answered no content"
	}
	if why == "" {
		return jinaAnswer{body: again, status: againStatus, headers: fresh, stale: snapshotDespiteOptOut(again)}
	}
	return jinaAnswer{
		body:   body,
		status: status,
		stale: "the jina reader answered a cached snapshot and its fresh render failed (" + why +
			"): the copy may be out of date or of another page",
	}
}

// snapshotDespiteOptOut is the gap of a jina reader answer asked with its
// caching opt-out: asking fresh is no proof of a fresh render, so an answer
// still declaring a cached snapshot is named one; "" for any other answer.
func snapshotDespiteOptOut(body []byte) string {
	if !jinaCachedSnapshot(body) {
		return ""
	}
	return "the jina reader answered a cached snapshot despite its caching opt-out: " +
		"the copy may be out of date or of another page"
}

// jinaCachedSnapshot reports whether a jina reader answer's envelope, the
// header lines above its "Markdown Content:" line, warns it is a cached
// snapshot.
func jinaCachedSnapshot(body []byte) bool {
	envelope := body
	if end := bytes.Index(body, []byte("Markdown Content:")); end >= 0 {
		envelope = body[:end]
	} else if len(envelope) > jinaEnvelopeScan {
		envelope = envelope[:jinaEnvelopeScan]
	}
	for _, line := range bytes.Split(envelope, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("Warning:")) &&
			bytes.Contains(line, jinaCachedSnapshotWarning) {
			return true
		}
	}
	return false
}

// jinaEnvelopeScan bounds where an answer with no "Markdown Content:" line is
// searched for its envelope's warning.
const jinaEnvelopeScan = 2048

func stripJinaEnvelope(text string) string {
	if idx := strings.Index(text, "Markdown Content:"); idx >= 0 {
		return strings.TrimLeft(text[idx+len("Markdown Content:"):], "\r\n")
	}
	lines := strings.Split(text, "\n")
	i := 0
	for i < len(lines) {
		key, _, ok := strings.Cut(lines[i], ":")
		if !ok {
			break
		}
		switch key {
		case "Title", "URL Source", "Published Time", "Markdown Content", "Warning":
			i++
		default:
			return text
		}
	}
	return strings.TrimLeft(strings.Join(lines[i:], "\n"), "\r\n")
}
