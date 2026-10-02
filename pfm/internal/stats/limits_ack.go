package stats

import (
	"context"
	"errors"
	"fmt"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	headlessrun "github.com/rezzminator/professor/pfm/internal/headless/run"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/usagehook"
)

// errAckAlreadyAttempted marks the probe gate's refusal, which is bookkeeping
// rather than a diagnosis: it must never be reported as the reason an account
// is blank.
var errAckAlreadyAttempted = errors.New("credential refresh already attempted")

// tryAck runs the credential probe (Ack) only when the cross-process gate
// grants it (usagehook.ClaimProbe): at most one probe per credential
// fingerprint per usagehook.ProbeCooldown across every pfm process, so a
// picker reopened every minute, the TUI and a statusline sampler never each
// launch their own session in the account.
func (sampler *LimitsSampler) tryAck(ctx context.Context, account LimitAccount) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if sampler.Ack == nil {
		return fmt.Errorf("credential refresh unavailable for account %d", account.ID)
	}
	logger := obs.Logger(ctx).With("acct", account.ID, "config_dir", account.ConfigDir)
	cacheDir := usagehook.CacheDirFor(sampler.Env)
	claim, err := usagehook.ClaimProbe(ctx, cacheDir, account.ID, account.ConfigDir, sampler.now())
	if err != nil {
		logger.Warn("usage.probe.gated", "reason", "the probe gate could not be read or written", "err", err.Error())
		return fmt.Errorf("credential probe gate for account %d: %w", account.ID, err)
	}
	if !claim.Allowed {
		logger.Info("usage.probe.gated", "credential", claim.Credential, "reason", claim.Reason,
			"failure", claim.Previous.Failure)
		if claim.Previous.Failure != "" {
			sampler.rememberProbeFailure(account, claim.Previous.Failure)
		}
		return fmt.Errorf("%w for account %d: %s", errAckAlreadyAttempted, account.ID, claim.Reason)
	}
	logger.Warn("usage.probe.fired", "credential", claim.Credential)
	err = sampler.Ack(ctx, account)
	outcome, failure := "ok", ""
	switch {
	case ctx.Err() != nil:
		// A cancelled probe already launched its session, so its claim
		// stands: re-arming here is how a picker opened and closed every
		// minute would launch one session per opening.
		outcome = "cancelled"
	case err != nil:
		// The probe's combined output can carry the account's own hook chatter,
		// newlines included, and this string lands in a single TUI row. Collapse
		// every whitespace run so the card stays one line without discarding
		// any of the reason.
		outcome, failure = "failed", strings.Join(strings.Fields(err.Error()), " ")
		sampler.rememberProbeFailure(account, failure)
	}
	if settleErr := usagehook.SettleProbe(cacheDir, account.ID, claim, outcome, failure); settleErr != nil {
		// The claim already gates its credential; only the outcome went unrecorded.
		logger.Warn("usage.probe.settle", "credential", claim.Credential, "err", settleErr.Error())
	}
	logger.Info("usage.probe.done", "credential", claim.Credential, "outcome", outcome, "failure", failure)
	return err
}

func (sampler *LimitsSampler) rememberProbeFailure(account LimitAccount, failure string) {
	sampler.mu.Lock()
	defer sampler.mu.Unlock()
	if sampler.ackFailure == nil {
		sampler.ackFailure = make(map[string]string)
	}
	sampler.ackFailure[account.cacheKey()] = failure
}

// probeFailure is the remembered reason this account's credential probe
// failed, or "" if one never ran or ran successfully.
func (sampler *LimitsSampler) probeFailure(account LimitAccount) string {
	sampler.mu.Lock()
	failure := sampler.ackFailure[account.cacheKey()]
	sampler.mu.Unlock()
	if failure != "" || account.Engine != pfmengine.Claude {
		return failure
	}
	// This process never probed: the cross-process gate's record still says
	// why another process's probe for this account failed.
	recorded, err := usagehook.RecentProbeFailure(
		usagehook.CacheDirFor(sampler.Env), account.ID, account.ConfigDir, sampler.now())
	if err != nil {
		return "the credential probe record could not be read: " + err.Error()
	}
	return recorded
}

func needsCredentialRefresh(err error) bool {
	return isCredentialRejection(err)
}

func defaultAck(ctx context.Context, account LimitAccount) error {
	result, err := headlessrun.Run(ctx, headlessrun.Request{
		Engine: pfmengine.Claude, Account: account.ID,
		Model: "claude-haiku-4-5", Prompt: "ACK", Native: true,
		Args:     []string{"--max-turns", "1"},
		Settings: map[string]any{"systemPrompt": "lean"},
		Config: pfmconfig.Config{
			Claude:   pfmconfig.ClaudePrefs{Binary: account.ClaudeBinary},
			Accounts: []pfmconfig.Account{{ID: account.ID, ConfigDir: account.ConfigDir}},
		},
	})
	if err != nil {
		return fmt.Errorf(
			"refresh account %d OAuth token: %w (%s)",
			account.ID,
			err,
			strings.TrimSpace(result.Stdout+result.Stderr),
		)
	}
	return nil
}
