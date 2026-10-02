package usagehook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/clock"
)

// ErrSignedOut marks an account whose credential EXISTS but carries no usable
// token — the shape Claude Code leaves behind after a refresh token expires.
// It is deliberately distinct from os.ErrNotExist: "there is no credential
// here" sends a reader hunting for a missing file, while this condition has
// exactly one repair, an interactive `claude /login`, and no automatic probe
// can perform it. Callers that must tell the two apart at the visible surface
// use IsCredentialUnavailable to cover both and errors.Is for the difference.
var ErrSignedOut = errors.New("account is signed out; run `claude /login` for it")

// KeychainService derives the macOS Keychain generic-password service name
// Claude Code stores an account's OAuth credential under. Claude Code keys the
// entry by the account's config directory — the first four bytes of the
// SHA-256 of the directory path, hex — so the mapping is per-account and this
// derivation is the only thing that ties a config dir to its secret. Deriving
// it (rather than scanning the keychain for any "Claude Code-credentials-*"
// entry) is what keeps one account's quota from ever being attributed to
// another: a host accumulates one entry per config dir it has ever used.
func KeychainService(configDir string) string {
	sum := sha256.Sum256([]byte(configDir))
	return "Claude Code-credentials-" + hex.EncodeToString(sum[:])[:8]
}

// keychainReader is the seam tests replace; the real implementation is
// per-GOOS in keychain_darwin.go / keychain_other.go.
var keychainReader = readKeychain

// loadCredential resolves one account's OAuth credential, file first and
// keychain second. The file is authoritative where it exists because a jail or
// a test can plant one; the keychain is where a real macOS Claude Code install
// actually keeps it, which is why an account can be fully logged in and still
// have no .credentials.json anywhere on disk.
//
// The returned error distinguishes the three outcomes a caller must render
// differently: neither source holds anything (wraps os.ErrNotExist, so the
// statusline-snapshot fallback and the credential probe still engage), a
// source holds a signed-out credential (ErrSignedOut), or a source holds
// something unreadable (a decode error, which is a real failure to look and
// must never be reported as absence).
func loadCredential(ctx context.Context, configDir string) (credentials, error) {
	var credential credentials
	body, fileErr := os.ReadFile(CredentialPath(configDir))
	if fileErr != nil && !errors.Is(fileErr, os.ErrNotExist) {
		return credential, fmt.Errorf("read usage credentials: %w", fileErr)
	}
	source := CredentialPath(configDir)
	if fileErr != nil {
		service := KeychainService(configDir)
		keychainBody, keychainErr := keychainReader(ctx, service)
		if keychainErr != nil {
			if errors.Is(keychainErr, os.ErrNotExist) {
				// Name BOTH doors. A message naming only the file sends a
				// keychain host hunting for a file it is never supposed to
				// have, which is the exact confusion this path exists to end.
				return credential, fmt.Errorf(
					"read usage credentials: no %s and no keychain item %q: %w",
					source, service, os.ErrNotExist,
				)
			}
			return credential, fmt.Errorf("read keychain item %q: %w", service, keychainErr)
		}
		body = keychainBody
		source = "keychain item " + service
	}
	if err := json.Unmarshal(body, &credential); err != nil {
		return credential, fmt.Errorf("decode usage credentials from %s: %w", source, err)
	}
	if credential.OAuth.AccessToken == "" {
		return credential, fmt.Errorf("%s holds no access token: %w", source, ErrSignedOut)
	}
	return credential, nil
}

// IsCredentialUnavailable reports whether err means "this account has no
// usable credential for us to spend", covering both the absent and the
// signed-out shape. It is the gate for every fallback that exists because an
// account cannot be queried directly — the statusline snapshot above all —
// since a signed-out account is exactly as unqueryable as a missing one.
func IsCredentialUnavailable(err error) bool {
	return errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrSignedOut)
}

// CredentialAvailable reports whether an account has a usable credential in
// either source, returning the same distinguishable error loadCredential does.
func CredentialAvailable(ctx context.Context, configDir string) error {
	_, err := loadCredential(ctx, configDir)
	return err
}

// CredentialExpired reports whether the account's access token is past the
// expiresAt Claude Code stamped on it. The usage endpoint refuses an expired
// token — as a 401, or as a 429 whose Retry-After every wait renews — and only
// a refresh repairs it, so a caller holding a failure for an expired token
// runs the credential probe whatever the status said. A credential with no
// expiresAt, or none at all, is not expired: the other doors name those
// states. An unreadable credential is an error, never "not expired".
func CredentialExpired(ctx context.Context, configDir string, now time.Time) (bool, error) {
	credential, err := loadCredential(ctx, configDir)
	if err != nil {
		if IsCredentialUnavailable(err) {
			return false, nil
		}
		return false, err
	}
	expiresAt := credential.OAuth.ExpiresAt
	return expiresAt > 0 && !now.Before(time.UnixMilli(expiresAt)), nil
}

// CredentialFingerprint names the credential an account holds without
// carrying it: the first 16 hex digits of the SHA-256 of its access token,
// "signed-out" for a credential with no token, "absent" when neither the file
// nor the keychain holds one. The raw token is never stored; an unreadable
// credential is an error, never absence.
func CredentialFingerprint(ctx context.Context, configDir string) (string, error) {
	credential, err := loadCredential(ctx, configDir)
	switch {
	case err == nil:
		return tokenFingerprint(credential.OAuth.AccessToken), nil
	case errors.Is(err, ErrSignedOut):
		return "signed-out", nil
	case errors.Is(err, os.ErrNotExist):
		return "absent", nil
	}
	return "", err
}

func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return "sha256:" + hex.EncodeToString(sum[:])[:16]
}

// ProbeCooldown is how long one credential stays gated after its credential
// probe (the headless one-turn session internal/stats runs to make the CLI
// refresh or mint a token). Twelve hours: the probe repairs only what a token
// refresh repairs, so a credential it already failed to repair stays broken
// until something outside pfm changes it, and the fingerprint sees every such
// change on this host at once. The cooldown only catches what the fingerprint
// cannot see (the provider reinstating the account), and caps stray probe
// sessions at two a day per account.
const ProbeCooldown = 12 * time.Hour

// ProbeRecord is the on-disk gate beside an account's usage cache: the one
// credential probe claimed for a credential, and how it ended.
type ProbeRecord struct {
	ConfigDir   string    `json:"config_dir"`
	Credential  string    `json:"credential"`
	AttemptedAt time.Time `json:"attempted_at"`
	// Outcome is "running" from the claim until SettleProbe, then "ok",
	// "failed" or "cancelled".
	Outcome string `json:"outcome"`
	Failure string `json:"failure,omitempty"`
}

// ProbeClaim is ClaimProbe's answer. Allowed is the right to run one probe;
// otherwise Reason says why not, and Previous is the record that gated it.
type ProbeClaim struct {
	Allowed     bool
	Credential  string
	AttemptedAt time.Time
	Reason      string
	Previous    ProbeRecord
}

// ProbePath is the probe gate beside CachePath.
func ProbePath(cacheDir string, account int) string {
	return filepath.Join(cacheDir, fmt.Sprintf("acct-%d.probe.json", account))
}

// ClaimProbe is the cross-process gate on the credential probe: at most one
// probe per credential fingerprint per ProbeCooldown, across every pfm
// process. The claim is read and written under the account's refresh lock
// (RefreshLockPath); a peer holding that lock defers the probe, and a gate
// that cannot be read or written refuses it, since a probe launches a
// session in the account.
func ClaimProbe(
	ctx context.Context, cacheDir string, account int, configDir string, now time.Time,
) (claim ProbeClaim, returnErr error) {
	fingerprint, err := CredentialFingerprint(ctx, configDir)
	if err != nil {
		return ProbeClaim{}, fmt.Errorf("fingerprint account %d credential: %w", account, err)
	}
	claim.Credential = fingerprint
	release, held, err := holdRefreshLock(cacheDir, account)
	if err != nil || !held {
		claim.Reason = "a peer pfm process holds this account's refresh lock"
		return claim, err
	}
	defer func() { returnErr = errors.Join(returnErr, release()) }()
	path := ProbePath(cacheDir, account)
	previous, found, err := readProbeRecord(path)
	if err != nil {
		return claim, err
	}
	if found && filepath.Clean(previous.ConfigDir) == filepath.Clean(configDir) &&
		previous.Credential == fingerprint && !previous.AttemptedAt.After(now) &&
		now.Sub(previous.AttemptedAt) < ProbeCooldown {
		claim.Previous = previous
		claim.Reason = fmt.Sprintf(
			"this credential was probed at %s (%s); the next probe waits for a credential change or %s",
			previous.AttemptedAt.Format(time.RFC3339), previous.Outcome,
			previous.AttemptedAt.Add(ProbeCooldown).Format(time.RFC3339),
		)
		return claim, nil
	}
	record := ProbeRecord{ConfigDir: configDir, Credential: fingerprint, AttemptedAt: now, Outcome: "running"}
	if err := writeProbeRecord(path, record); err != nil {
		return claim, err
	}
	claim.Allowed, claim.AttemptedAt = true, now
	return claim, nil
}

// SettleProbe records how a claimed probe ended. It replaces the record only
// while it is still this claim, so a later claim (a changed credential) is
// never overwritten; a peer holding the refresh lock is only ever reading or
// claiming, so the compare-and-replace runs without it then.
func SettleProbe(cacheDir string, account int, claim ProbeClaim, outcome, failure string) (returnErr error) {
	release, held, err := holdRefreshLock(cacheDir, account)
	if err != nil {
		return err
	}
	if held {
		defer func() { returnErr = errors.Join(returnErr, release()) }()
	}
	path := ProbePath(cacheDir, account)
	current, found, err := readProbeRecord(path)
	if err != nil {
		return err
	}
	if !found || current.Credential != claim.Credential || !current.AttemptedAt.Equal(claim.AttemptedAt) {
		return nil
	}
	current.Outcome, current.Failure = outcome, failure
	return writeProbeRecord(path, current)
}

// holdRefreshLock takes the account's refresh lock; release removes it.
func holdRefreshLock(cacheDir string, account int) (release func() error, held bool, err error) {
	if err := EnsurePrivateDir(cacheDir); err != nil {
		return nil, false, fmt.Errorf("usage cache directory %s: %w", cacheDir, err)
	}
	lockPath := RefreshLockPath(cacheDir, account)
	held, err = takeRefreshLock(lockPath, clock.Real.Now())
	if err != nil || !held {
		return nil, false, err
	}
	return func() error {
		if err := os.Remove(lockPath); err != nil {
			return fmt.Errorf("release usage refresh lock %s: %w", lockPath, err)
		}
		return nil
	}, true, nil
}

// RecentProbeFailure is the failure the last settled credential probe for
// this account and config dir recorded, while it is younger than
// ProbeCooldown: a picker opened after another process's probe failed still
// names why, instead of only the symptom it holds. "" when no failed probe is
// on record; an unreadable record is an error, never "".
func RecentProbeFailure(cacheDir string, account int, configDir string, now time.Time) (string, error) {
	record, found, err := readProbeRecord(ProbePath(cacheDir, account))
	if err != nil || !found {
		return "", err
	}
	if record.ConfigDir != configDir || record.Outcome != "failed" || now.Sub(record.AttemptedAt) >= ProbeCooldown {
		return "", nil
	}
	return record.Failure, nil
}

func readProbeRecord(path string) (ProbeRecord, bool, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ProbeRecord{}, false, nil
	}
	if err != nil {
		return ProbeRecord{}, false, fmt.Errorf("read credential probe gate %s: %w", path, err)
	}
	var record ProbeRecord
	if err := json.Unmarshal(body, &record); err != nil {
		return ProbeRecord{}, false, fmt.Errorf("decode credential probe gate %s: %w", path, err)
	}
	return record, true, nil
}

func writeProbeRecord(path string, record ProbeRecord) error {
	body, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode credential probe gate %s: %w", path, err)
	}
	if err := atomicfile.Write(path, body, 0o600); err != nil {
		return fmt.Errorf("write credential probe gate %s: %w", path, err)
	}
	return nil
}
