package stats

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

type limitsSamplerContextKey struct{}

func withLimitsSampler(ctx context.Context, sampler *LimitsSampler) context.Context {
	return context.WithValue(ctx, limitsSamplerContextKey{}, sampler)
}

func samplerFromContext(ctx context.Context) (*LimitsSampler, error) {
	sampler, ok := ctx.Value(limitsSamplerContextKey{}).(*LimitsSampler)
	if !ok || sampler == nil {
		return nil, errors.New("usage source called without limits sampler")
	}
	return sampler, nil
}

// FetchClaude is the adapter target for Claude's registered UsageSource. It
// carries the sampler's own confirmation time (never "now"), the single live
// retry a successful credential ACK authorizes, and last-good windows through
// a transient provider failure — an outage renders cached quota under a
// warning, never an empty tab that reads like "no limits".
func FetchClaude(ctx context.Context, account LimitAccount) (AccountLimits, error) {
	sampler, err := samplerFromContext(ctx)
	if err != nil {
		return AccountLimits{}, err
	}
	now := sampler.now()
	label := account.Label
	if label == "" {
		label = fmt.Sprintf("%s account %d", pfmengine.MustLookup(account.Engine).Short, account.ID)
	}
	result := AccountLimits{}
	usage, confirmedAt, fetchErr := sampler.fetchClaude(ctx, account)
	if fetchErr != nil && usagehook.IsCredentialUnavailable(fetchErr) {
		statuslineUsage, statuslineConfirmedAt, found, statuslineErr := sampler.fetchClaudeStatusline(account)
		switch {
		case statuslineErr != nil:
			fetchErr = statuslineErr
		case found:
			usage, confirmedAt, fetchErr = statuslineUsage, statuslineConfirmedAt, nil
		}
	}
	// Neither a credentials file nor a statusline snapshot: the account is
	// completely dark, and nothing about rendering the tab changes that on its
	// own — the seat historically stayed blank until someone sent it a prompt
	// by hand, which is what made its CLI mint a token and publish a snapshot.
	// Spend the same hidden one-turn Haiku probe the credential-rejection path
	// below already uses (tryAck is headless and fires at most once per account
	// per sampler), then re-read BOTH sources: the probe may write the
	// credential file, and its session may publish the snapshot.
	// A SIGNED-OUT account is the one shape the probe cannot repair: its
	// refresh token is empty or expired, so the headless turn can only fail
	// with the same diagnosis we already hold. Spending ~10s spawning a CLI to
	// re-derive a known answer would also bury it under a probe-failure
	// suffix, when the account needs the plain instruction instead.
	if fetchErr != nil && usagehook.IsCredentialUnavailable(fetchErr) && !errors.Is(fetchErr, usagehook.ErrSignedOut) {
		if ackErr := sampler.tryAck(ctx, account); ackErr == nil {
			usage, confirmedAt, fetchErr = sampler.fetchClaudeAfterCredentialRefresh(ctx, account)
			if fetchErr != nil && usagehook.IsCredentialUnavailable(fetchErr) {
				probedUsage, probedConfirmedAt, found, probedErr := sampler.fetchClaudeStatusline(account)
				switch {
				case probedErr != nil:
					fetchErr = probedErr
				case found:
					usage, confirmedAt, fetchErr = probedUsage, probedConfirmedAt, nil
				}
			}
		}
		// The probe is the only automatic repair for this state, so when it
		// fails the card must say why — an OAuth session too old to refresh
		// needs an interactive re-login, which "credentials file missing"
		// does not convey.
		if fetchErr != nil {
			if reason := sampler.probeFailure(account); reason != "" {
				fetchErr = fmt.Errorf("%w; credential probe failed: %s", fetchErr, reason)
			}
		}
	}
	if fetchErr != nil && !usagehook.IsCredentialUnavailable(fetchErr) && needsCredentialRefresh(fetchErr) {
		if sampler.tryAck(ctx, account) == nil {
			usage, confirmedAt, fetchErr = sampler.fetchClaudeAfterCredentialRefresh(ctx, account)
		}
	}
	if fetchErr != nil {
		windows := usageWindows(usage, now)
		if !confirmedAt.IsZero() && len(windows) > 0 && staleEligible(fetchErr) {
			result.ConfirmedAt = confirmedAt
			result.Windows = windows
			result.Status = staleStatus(fetchErr)
			return result, fmt.Errorf(
				"%s limits refresh failed; showing cache confirmed %s: %v",
				label, confirmedAt.Format(time.RFC3339), fetchErr,
			)
		}
		if isCredentialRejection(fetchErr) {
			result.Status = fmt.Sprintf("skipped %s: credentials rejected", label)
			return result, nil
		}
		result.Status = fmt.Sprintf("account %d limits unavailable: %v", account.ID, fetchErr)
		return result, errors.New(result.Status)
	}
	result.ConfirmedAt = confirmedAt
	result.Windows = usageWindows(usage, now)
	if len(result.Windows) == 0 {
		result.Status = fmt.Sprintf("account %d limits unavailable: empty usage response", account.ID)
		return result, errors.New(result.Status)
	}
	return result, nil
}

// FetchCodex is the adapter target for Codex's registered UsageSource, with
// the same stale-cache honesty as Claude's: a refresh that fails over a
// last-good payload reports WHY under the cached windows.
func FetchCodex(ctx context.Context, account LimitAccount) (AccountLimits, error) {
	sampler, err := samplerFromContext(ctx)
	if err != nil {
		return AccountLimits{}, err
	}
	label := account.Label
	if label == "" {
		label = fmt.Sprintf("%s account %d", pfmengine.MustLookup(account.Engine).Short, account.ID)
	}
	result := AccountLimits{}
	usage, confirmedAt, fetchErr := sampler.fetchCodexForAccount(ctx, account)
	if fetchErr != nil {
		windows := codexWindows(usage)
		if !confirmedAt.IsZero() && len(windows) > 0 && staleEligible(fetchErr) {
			result.Plan = usage.PlanType
			result.ConfirmedAt = confirmedAt
			result.Windows = windows
			result.Status = staleStatus(fetchErr)
			return result, fmt.Errorf(
				"%s limits refresh failed; showing cache confirmed %s: %v",
				label, confirmedAt.Format(time.RFC3339), fetchErr,
			)
		}
		result.Status = fetchErr.Error()
		return result, fmt.Errorf("%s limits unavailable: %w", label, fetchErr)
	}
	result.Plan = usage.PlanType
	result.ConfirmedAt = confirmedAt
	result.Windows = codexWindows(usage)
	if len(result.Windows) == 0 {
		result.Status = "Codex payload unreadable"
		return result, errors.New(result.Status)
	}
	if usage.Warning != "" {
		result.Status = usage.Warning
		return result, errors.New(usage.Warning)
	}
	return result, nil
}

func applyFetchedLimits(target *AccountLimits, fetched AccountLimits) {
	target.Plan = fetched.Plan
	target.ConfirmedAt = fetched.ConfirmedAt
	target.Status = fetched.Status
	target.Windows = fetched.Windows
}

type UsageSource interface {
	Fetch(ctx context.Context, account LimitAccount) (AccountLimits, error)
}

var usageSources = map[pfmengine.ID]UsageSource{}

func RegisterUsageSource(id pfmengine.ID, source UsageSource) {
	if _, duplicate := usageSources[id]; duplicate {
		panic(fmt.Sprintf("stats: usage source for engine %q registered twice", id))
	}
	usageSources[id] = source
}

func UsageSourceFor(id pfmengine.ID) (UsageSource, error) {
	source, ok := usageSources[id]
	if !ok {
		return nil, fmt.Errorf("engine %s: no usage source registered", id)
	}
	return source, nil
}

func RegisteredUsageSources() []pfmengine.ID {
	ids := make([]pfmengine.ID, 0, len(usageSources))
	for id := range usageSources {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
	return ids
}
