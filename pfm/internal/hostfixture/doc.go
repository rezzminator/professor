// Package hostfixture holds the ten host edge cases every unit test needs
// to jail against instead of assuming a clean, writable, English, tmux-
// equipped, logged-in host
// (docs/dev/trains/testing-foundation/waves/3-unit-law/spec.md § The ten
// host edge cases). Each fixture builds on testjail (the jailed fleet
// root), paths.MapEnv (an injectable environment mirror), deps.FakeRunner
// (scripted external commands) and clock.Fake (a deterministic clock), and
// returns a Base — or a small struct embedding one — plus whatever extra
// state that case's own assertion helpers need. One line per case:
//
//   - NoHome — HOME and PFM_HOME both unset; paths.Home must refuse.
//   - ReadOnlyHome — the jailed home exists but is 0o555; every write into it
//     must fail (skipped by name when the fence itself runs as root).
//   - SymlinkedConfigDir — ~/.claude is a symlink to a physical directory
//     elsewhere in the jail.
//   - CaseFoldProbe — creates "a" and "A" and reports whether this
//     filesystem folded them to one entry.
//   - NoTmux — the FakeRunner answers ENOENT for tmux.
//   - OldTmux — the FakeRunner resolves tmux to a build below deps'
//     registered minimum version.
//   - NoServiceManager — the FakeRunner answers ENOENT for both systemctl
//     and launchctl.
//   - BareTerm — TERM is unset and LANG/LC_ALL are forced to the POSIX "C"
//     locale.
//   - OddPaths — HOME sits under a directory whose name carries a space and
//     a non-ASCII character.
//   - ExpiredCreds — ~/.credentials.json exists with expiresAt in the past.
//   - NoCreds — no ~/.credentials.json exists, and the FakeRunner answers
//     the Keychain "security" probe with its not-found exit status.
//   - StaleArtifacts — a dead tmux socket file, an exited process's pid
//     file, a fleet.db-wal left by a crashed writer, and a leftover reload
//     lock.
//   - TwoWriters — runs a caller-supplied function twice concurrently
//     against the same jailed fleet.
package hostfixture
