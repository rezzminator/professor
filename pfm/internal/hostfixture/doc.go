// Package hostfixture holds the six host edge cases unit tests jail against
// instead of assuming a clean, writable, English, tmux-
// equipped, logged-in host. Each fixture builds on testjail (the jailed fleet
// root), paths.MapEnv (an injectable environment mirror), deps.FakeRunner
// (scripted external commands) and clock.Fake (a deterministic clock), and
// returns a Base — or a small struct embedding one — plus whatever extra
// state that case's own assertion helpers need. One line per case:
//
//   - NoHome — HOME and PFM_HOME both unset; paths.Home must refuse.
//   - SymlinkedConfigDir — ~/.claude is a symlink to a physical directory
//     elsewhere in the jail.
//   - NoTmux — the FakeRunner answers ENOENT for tmux.
//   - OddPaths — HOME sits under a directory whose name carries a space and
//     a non-ASCII character.
//   - NoCreds — no ~/.credentials.json exists, and the FakeRunner answers
//     the Keychain "security" probe with its not-found exit status.
//   - StaleArtifacts — a dead tmux socket file, an exited process's pid
//     file, a pfm.db-wal left by a crashed writer, and a leftover reload
//     lock.
package hostfixture
