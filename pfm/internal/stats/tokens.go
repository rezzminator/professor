package stats

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/pricing"
	"github.com/rezzminator/professor/pfm/internal/transcript"
)

const (
	tokenRateWindow   = time.Minute
	tokenRewriteGuard = 256
	// usageSparkPoints caps the per-chat usage chart: 12 samples at the Stats
	// tab's 2s cadence is a ~24s burn window, wide enough to read a pulse and
	// narrow enough to stay live.
	usageSparkPoints = 12
)

type tokenCacheEntry struct {
	offset       int64
	modifiedNS   int64
	claudeTokens int64
	codexTokens  int64
	codexPeak    int64
	codexClosed  int64
	known        bool
	generation   uint64
	fileInfo     os.FileInfo
	rewriteGuard []byte
	// countedMessages retains each response's latest contribution so later
	// content-block records can revise its partial usage without double counting.
	// A short ring is enough: a message's records are written together.
	countedMessages []tokenMessageContribution
}

type tokenMessageContribution struct {
	key    string
	tokens int64
}

// countedMessageRing bounds countedMessages; a message never spans more
// records than this between its first and last block.
const countedMessageRing = 16

type tokenMeasure struct {
	total      int64
	known      bool
	identity   string
	generation uint64
}

type tokenRateSample struct {
	timeNS int64
	total  int64
}

type tokenRateState struct {
	identity string
	samples  []tokenRateSample
}

// tokenHistoryState keeps the per-socket usage chart baseline: the transcript
// identity it tracks, the last cumulative total seen, and the capped delta ring.
type tokenHistoryState struct {
	identity    string
	last        int64
	hasBaseline bool
	deltas      []int64
}

type tokenRecord struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	RequestID string `json:"requestId"`
	Message   struct {
		ID    string              `json:"id"`
		Usage *pricing.UsageBlock `json:"usage"`
	} `json:"message"`
	Payload struct {
		Type string                     `json:"type"`
		Info *transcript.CodexTokenInfo `json:"info"`
	} `json:"payload"`
}

func (sampler *Sampler) attachTokenUsage(rows []compose.Row, chats []Chat, now int64) []string {
	pathsBySocket := make(map[string]map[string]bool)
	sessionsBySocket := make(map[string]map[string]bool)
	for index := range rows {
		row := &rows[index]
		if row.Socket == "" || row.Path == "" || !liveKind(row.Kind) {
			continue
		}
		paths := pathsBySocket[row.Socket]
		if paths == nil {
			paths = make(map[string]bool)
			pathsBySocket[row.Socket] = paths
		}
		paths[row.Path] = true
		sessions := sessionsBySocket[row.Socket]
		if sessions == nil {
			sessions = make(map[string]bool)
			sessionsBySocket[row.Socket] = sessions
		}
		sessions[fmt.Sprintf("%d:%s", row.Kind, row.ID)] = true
	}

	sampler.tokenMu.Lock()
	defer sampler.tokenMu.Unlock()
	if sampler.tokenCache == nil {
		sampler.tokenCache = make(map[string]*tokenCacheEntry)
	}
	usedPaths := make(map[string]bool)
	usageBySocket := make(map[string]tokenMeasure)
	var warnings []string
	for socket, paths := range pathsBySocket {
		combined := tokenMeasure{}
		identities := make([]string, 0, len(paths))
		for path := range paths {
			usedPaths[path] = true
			measure, pathWarnings := sampler.readTokenUsageLocked(path)
			warnings = append(warnings, pathWarnings...)
			identities = append(identities, fmt.Sprintf("%s\x00%d", path, measure.generation))
			if measure.known {
				combined.total += measure.total
				combined.known = true
			}
		}
		for session := range sessionsBySocket[socket] {
			identities = append(identities, "session:"+session)
		}
		sort.Strings(identities)
		combined.identity = strings.Join(identities, "\x1e")
		usageBySocket[socket] = combined
	}
	for path := range sampler.tokenCache {
		if !usedPaths[path] {
			delete(sampler.tokenCache, path)
		}
	}
	if sampler.tokenRates == nil {
		sampler.tokenRates = make(map[string]*tokenRateState)
	}
	if sampler.tokenHistory == nil {
		sampler.tokenHistory = make(map[string]*tokenHistoryState)
	}
	usedSockets := make(map[string]bool)
	for index := range chats {
		socket := chats[index].Socket
		measure := usageBySocket[socket]
		chats[index].TokenCount = measure.total
		chats[index].TokensKnown = measure.known
		if !measure.known || measure.identity == "" {
			delete(sampler.tokenRates, socket)
			continue
		}
		usedSockets[socket] = true
		sampler.attachTokenRateLocked(&chats[index], measure, now)
		sampler.attachUsageSparkLocked(&chats[index], measure)
	}
	for socket := range sampler.tokenRates {
		if !usedSockets[socket] {
			delete(sampler.tokenRates, socket)
		}
	}
	for socket := range sampler.tokenHistory {
		if !usedSockets[socket] {
			delete(sampler.tokenHistory, socket)
		}
	}
	return warnings
}

// attachUsageSparkLocked folds this sample's cumulative total into the chat's
// usage chart: a ring of per-sample token deltas, oldest first. The first
// sample only sets the baseline — a chart needs two points to draw a step.
func (sampler *Sampler) attachUsageSparkLocked(chat *Chat, measure tokenMeasure) {
	if !measure.known || measure.identity == "" {
		delete(sampler.tokenHistory, chat.Socket)
		return
	}
	state := sampler.tokenHistory[chat.Socket]
	if state == nil || state.identity != measure.identity || measure.total < state.last {
		sampler.tokenHistory[chat.Socket] = &tokenHistoryState{
			identity:    measure.identity,
			last:        measure.total,
			hasBaseline: true,
		}
		return
	}
	state.deltas = append(state.deltas, measure.total-state.last)
	if len(state.deltas) > usageSparkPoints {
		state.deltas = state.deltas[len(state.deltas)-usageSparkPoints:]
	}
	state.last = measure.total
	chat.Spark = append([]int64(nil), state.deltas...)
}

func (sampler *Sampler) attachTokenRateLocked(chat *Chat, measure tokenMeasure, now int64) {
	state := sampler.tokenRates[chat.Socket]
	reset := func() {
		sampler.tokenRates[chat.Socket] = &tokenRateState{
			identity: measure.identity,
			samples:  []tokenRateSample{{timeNS: now, total: measure.total}},
		}
	}
	if state == nil || state.identity != measure.identity || len(state.samples) == 0 {
		reset()
		return
	}
	last := state.samples[len(state.samples)-1]
	if now <= last.timeNS || now-last.timeNS > int64(tokenRateWindow) || measure.total < last.total {
		reset()
		return
	}
	state.samples = append(state.samples, tokenRateSample{timeNS: now, total: measure.total})
	cutoff := now - int64(tokenRateWindow)
	oldest := 0
	for oldest+1 < len(state.samples) && state.samples[oldest+1].timeNS <= cutoff {
		oldest++
	}
	if oldest > 0 {
		state.samples = append([]tokenRateSample(nil), state.samples[oldest:]...)
	}
	baseline := state.samples[0]
	elapsed := now - baseline.timeNS
	if elapsed <= 0 || measure.total < baseline.total {
		reset()
		return
	}
	chat.TokensPerMinute = float64(measure.total-baseline.total) /
		(float64(elapsed) / float64(time.Minute))
	chat.TokenRateValid = true
}

func (sampler *Sampler) readTokenUsageLocked(path string) (tokenMeasure, []string) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			delete(sampler.tokenCache, path)
			return tokenMeasure{}, nil
		}
		return tokenMeasure{}, []string{fmt.Sprintf("read chat token transcript %s: %v", path, err)}
	}
	if !info.Mode().IsRegular() {
		return tokenMeasure{}, []string{fmt.Sprintf("read chat token transcript %s: not a regular file", path)}
	}
	entry := sampler.tokenCache[path]
	if entry == nil || entry.fileInfo == nil || !os.SameFile(entry.fileInfo, info) || info.Size() < entry.offset ||
		(info.Size() == entry.offset && info.ModTime().UnixNano() != entry.modifiedNS) {
		entry = sampler.newTokenCacheEntryLocked(path, info)
	}
	if info.Size() == entry.offset {
		return tokenMeasure{
			total: entry.claudeTokens + entry.codexTokens,
			known: entry.known, generation: entry.generation,
		}, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			delete(sampler.tokenCache, path)
			return tokenMeasure{}, nil
		}
		return tokenMeasure{}, []string{fmt.Sprintf("open chat token transcript %s: %v", path, err)}
	}
	openedInfo, statErr := file.Stat()
	if statErr != nil {
		if closeErr := file.Close(); closeErr != nil {
			return tokenMeasure{}, []string{
				fmt.Sprintf(
					"stat chat token transcript %s before read: %v; close after stat failure: %v",
					path,
					statErr,
					closeErr,
				),
			}
		}
		return tokenMeasure{}, []string{fmt.Sprintf("stat chat token transcript %s before read: %v", path, statErr)}
	}
	if entry.fileInfo == nil || !os.SameFile(entry.fileInfo, openedInfo) || openedInfo.Size() < entry.offset {
		entry = sampler.newTokenCacheEntryLocked(path, openedInfo)
	}
	if matches, guardErr := tokenRewriteGuardMatches(file, entry); guardErr != nil {
		if closeErr := file.Close(); closeErr != nil {
			return tokenMeasure{}, []string{
				fmt.Sprintf(
					"verify chat token transcript %s rewrite guard: %v; close after guard failure: %v",
					path,
					guardErr,
					closeErr,
				),
			}
		}
		return tokenMeasure{}, []string{
			fmt.Sprintf("verify chat token transcript %s rewrite guard: %v", path, guardErr),
		}
	} else if !matches {
		entry = sampler.newTokenCacheEntryLocked(path, openedInfo)
	}
	if _, err := file.Seek(entry.offset, io.SeekStart); err != nil {
		if closeErr := file.Close(); closeErr != nil {
			return tokenMeasure{}, []string{
				fmt.Sprintf("seek chat token transcript %s: %v; close after seek failure: %v", path, err, closeErr),
			}
		}
		return tokenMeasure{}, []string{fmt.Sprintf("seek chat token transcript %s: %v", path, err)}
	}

	// A torn last line is left unread: the offset stops before it and the next
	// sample reads it whole.
	var warnings []string
	parsedOffset, _, readErr := transcript.ScanLines(file, entry.offset, func(line []byte, complete bool) {
		if !complete {
			return
		}
		if parseErr := applyTokenRecord(entry, line); parseErr != nil {
			warnings = append(warnings, fmt.Sprintf("parse chat token transcript %s: %v", path, parseErr))
		}
	})
	entry.offset = parsedOffset
	if readErr != nil {
		warnings = append(warnings, fmt.Sprintf("read chat token transcript %s: %v", path, readErr))
	}
	postInfo, statErr := file.Stat()
	if statErr != nil {
		warnings = append(warnings, fmt.Sprintf("stat chat token transcript %s after read: %v", path, statErr))
		entry.modifiedNS = openedInfo.ModTime().UnixNano()
		entry.fileInfo = openedInfo
	} else {
		entry.modifiedNS = postInfo.ModTime().UnixNano()
		entry.fileInfo = postInfo
	}
	if guardErr := updateTokenRewriteGuard(file, entry); guardErr != nil {
		warnings = append(warnings, fmt.Sprintf("record chat token transcript %s rewrite guard: %v", path, guardErr))
	}
	if closeErr := file.Close(); closeErr != nil {
		warnings = append(warnings, fmt.Sprintf("close chat token transcript %s: %v", path, closeErr))
	}
	return tokenMeasure{
		total: entry.claudeTokens + entry.codexTokens,
		known: entry.known, generation: entry.generation,
	}, warnings
}

func (sampler *Sampler) newTokenCacheEntryLocked(path string, info os.FileInfo) *tokenCacheEntry {
	sampler.tokenGeneration++
	entry := &tokenCacheEntry{
		generation: sampler.tokenGeneration,
		fileInfo:   info,
		modifiedNS: info.ModTime().UnixNano(),
	}
	sampler.tokenCache[path] = entry
	return entry
}

func tokenRewriteGuardMatches(file *os.File, entry *tokenCacheEntry) (bool, error) {
	if entry.offset == 0 || len(entry.rewriteGuard) == 0 {
		return true, nil
	}
	guard := make([]byte, len(entry.rewriteGuard))
	read, err := file.ReadAt(guard, entry.offset-int64(len(guard)))
	if err != nil && err != io.EOF {
		return false, err
	}
	if read != len(guard) {
		return false, nil
	}
	return bytes.Equal(guard, entry.rewriteGuard), nil
}

func updateTokenRewriteGuard(file *os.File, entry *tokenCacheEntry) error {
	size := int64(tokenRewriteGuard)
	if entry.offset < size {
		size = entry.offset
	}
	if size == 0 {
		entry.rewriteGuard = nil
		return nil
	}
	guard := make([]byte, int(size))
	read, err := file.ReadAt(guard, entry.offset-size)
	if err != nil && err != io.EOF {
		return err
	}
	if read != len(guard) {
		return io.ErrUnexpectedEOF
	}
	entry.rewriteGuard = guard
	return nil
}

func applyTokenRecord(entry *tokenCacheEntry, line []byte) error {
	if strings.TrimSpace(string(line)) == "" {
		return nil
	}
	var record tokenRecord
	if err := json.Unmarshal(line, &record); err != nil {
		return err
	}
	if record.Timestamp != "" {
		if _, err := time.Parse(time.RFC3339Nano, record.Timestamp); err != nil {
			return fmt.Errorf("parse timestamp %q: %w", record.Timestamp, err)
		}
	}
	if record.Type == "assistant" && record.Message.Usage != nil {
		usage := *record.Message.Usage
		if negativeUsage(usage) {
			return fmt.Errorf("assistant usage contains a negative token count")
		}
		total := claudeTokens(usage)
		entry.claudeTokens += entry.messageTokenDelta(record.Message.ID, record.RequestID, total)
		if total > 0 {
			entry.known = true
		}
	}
	if info := record.Payload.Info; record.Payload.Type == "token_count" && info != nil {
		total := info.TotalTokenUsage.Total()
		if total < 0 {
			return fmt.Errorf("lifetime usage for Codex contains a negative token count")
		}
		if info.TotalTokenUsage != nil {
			if total < entry.codexPeak {
				entry.codexClosed += entry.codexPeak
			}
			entry.codexPeak = total
			entry.codexTokens = entry.codexClosed + entry.codexPeak
			if total > 0 {
				entry.known = true
			}
		}
	}
	return nil
}

// claudeTokens is every token the response billed: the executor's and each
// advisor consultation's, which the executor's top level never carries.
func claudeTokens(usage pricing.UsageBlock) int64 {
	billed, _ := usage.Billed()
	total := billed.Context() + billed.Output
	for _, advisor := range usage.Advisors() {
		total += advisor.Usage.Context() + advisor.Usage.Output
	}
	return total
}

// negativeUsage reports a negative count at the top level or in any
// iteration: either would subtract from the chat's tokens.
func negativeUsage(usage pricing.UsageBlock) bool {
	if usage.Input < 0 || usage.Output < 0 || usage.CacheRead < 0 || usage.CacheCreation < 0 {
		return true
	}
	for _, iteration := range usage.Iterations {
		if negativeUsage(iteration) {
			return true
		}
	}
	return false
}

// messageTokenDelta records this response's latest usage and returns only its
// change from the previously counted contribution. Identical repeats add zero.
// A record naming neither a message id nor a request id is counted on its own —
// the shape every pre-id fixture has.
func (entry *tokenCacheEntry) messageTokenDelta(messageID, requestID string, total int64) int64 {
	key := transcript.ResponseKey(messageID, requestID, "", "")
	if key == "" {
		return total
	}
	for index := range entry.countedMessages {
		seen := &entry.countedMessages[index]
		if seen.key == key {
			delta := total - seen.tokens
			seen.tokens = total
			return delta
		}
	}
	if total == 0 {
		return 0 // An unseen zero contribution must not evict a billable response.
	}
	entry.countedMessages = append(entry.countedMessages, tokenMessageContribution{key: key, tokens: total})
	if len(entry.countedMessages) > countedMessageRing {
		entry.countedMessages = entry.countedMessages[len(entry.countedMessages)-countedMessageRing:]
	}
	return total
}
