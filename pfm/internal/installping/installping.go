// Package installping sends Professor's anonymous install ping: a random
// install id, pfm's version, OS and architecture, and the enabled plugins of
// Professor's own marketplace, POSTed to the install-ping Worker
// (infra/install-ping/) so real installs and daily-active installs can be
// counted. Nothing is sent when Reason names a cause: "installPing": false
// in pfm.config.json, an unreadable pfm.config.json, the opt-out variables,
// CI, the fence, or an unstamped build.
//
// Entry points:
//   - Reason: why no ping is sent under Options, "" when one is.
//   - Notice, NoticeSkipped: the one line pfm install prints about the ping.
//   - SendInstall: the event=install ping, sent once at the end of pfm install.
//   - SendDaily: the event=daily ping, at most once per UTC day.
//   - ForRuntime: the production Options of a runtime.
//
// Files:
//   - payload.go: Ping and BuildPing, the body the contract accepts.
//   - state.go: the install id and the day claims in pfm's state directory.
//   - send.go: the POST and its 5 s bound.
package installping

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/rezzminator/professor/pfm/internal/claudeplugin"
	"github.com/rezzminator/professor/pfm/internal/clock"
	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const (
	// Endpoint is the install-ping Worker's ping route.
	Endpoint = "https://install-ping.rezzminator.workers.dev/v1/ping"
	// Timeout bounds one ping, connection to response.
	Timeout = 5 * time.Second
	// EnvDoNotTrack is the cross-tool opt-out (https://consoledonottrack.com):
	// any value but empty, 0 or false sends nothing.
	EnvDoNotTrack = "DO_NOT_TRACK"
	// The CI markers: CI set to anything but empty, 0 or false, or
	// GITHUB_ACTIONS=true, sends nothing.
	envCI            = "CI"
	envGitHubActions = "GITHUB_ACTIONS"
	// reasonUnstamped is Reason for a binary whose version the counter refuses.
	reasonUnstamped = "unstamped development build"
	// reasonConfigOff and reasonConfigUnreadable are Reason for the config switch off and a config that did not load.
	reasonConfigOff        = `"installPing": false in pfm.config.json`
	reasonConfigUnreadable = "pfm.config.json unreadable"
	// optOut is the clause every notice ends on: the config key that turns both pings off.
	optOut = `set "installPing": false in pfm.config.json to turn it off`
)

// The two events the counter takes.
const (
	EventInstall = "install"
	EventDaily   = "daily"
)

// Options is one ping's inputs; ForRuntime builds the production ones, a test
// its own Endpoint, Client, Clock, Env, platform and directories.
type Options struct {
	Env paths.Env
	// ConfigOff is "installPing": false in pfm.config.json; ConfigError is the
	// error that left pfm.config.json unread. Either one sends nothing.
	ConfigOff   bool
	ConfigError error
	// StateDir holds the install id and the daily marker: pfm's state directory.
	StateDir string
	// ClaudeConfigDir is the Claude config dir whose enabled plugins are read.
	ClaudeConfigDir string
	Version         string
	// OS and Arch are the platform the ping reports.
	OS, Arch string
	Endpoint string
	Client   *http.Client
	Clock    clock.Clock
}

// ForRuntime is the production Options of runtime: the state directory beside
// its state database, env's Claude config dir, the binary's platform, the real endpoint, clock
// and a logged client bounded by Timeout.
func ForRuntime(runtime pfmconfig.Runtime, env paths.Env) Options {
	goos, goarch := paths.Platform()
	return Options{
		Env:             env,
		ConfigOff:       !runtime.Config.InstallPing,
		ConfigError:     runtime.ConfigError,
		StateDir:        filepath.Dir(runtime.Paths.StateDB),
		ClaudeConfigDir: claudeplugin.ConfigDirIn(env, runtime.Paths.Home),
		Version:         runtime.Version,
		OS:              goos,
		Arch:            goarch,
		Endpoint:        Endpoint,
		Client:          obs.WrapClient(&http.Client{Timeout: Timeout}),
		Clock:           clock.Real,
	}
}

// Reason is why no ping is sent under options, "" when one is, the one
// decision every caller shares: the config switch, then an unreadable config
// (a user who wrote false and broke the file is never pinged), then the
// variable that turned it off, the CI or fence marker, or an unstamped build
// the counter would refuse.
func Reason(options Options) string {
	env := options.Env
	switch {
	case options.ConfigOff:
		return reasonConfigOff
	case options.ConfigError != nil:
		return reasonConfigUnreadable
	case paths.InstallPingOfflineIn(env):
		return paths.EnvInstallPingOffline
	case set(env.Get(EnvDoNotTrack)):
		return EnvDoNotTrack
	case set(env.Get(envCI)):
		return envCI
	case env.Get(envGitHubActions) == "true":
		return envGitHubActions
	case env.Get(paths.EnvDevFence) == "1":
		return paths.EnvDevFence
	case !versionPattern.MatchString(options.Version):
		return reasonUnstamped
	}
	return ""
}

// set reports an opt-out or CI value that is on: non-empty, not 0, not false.
func set(value string) bool {
	return value != "" && value != "0" && !strings.EqualFold(value, "false")
}

// Notice is the one line pfm install prints: what the ping sends and how to
// turn it off when reason is "", else what turned it off.
func Notice(reason string) string {
	if reason == "" {
		return "pfm: install ping on — an anonymous count of Professor installs (random install id, pfm version, " +
			"OS, architecture, enabled Professor plugins; no IP, hostname, username or paths), sent at install " +
			"and once a day; " + optOut
	}
	return "pfm: install ping off (" + reason + ")"
}

// NoticeSkipped is the line for an install that skips its own ping for cause
// while the daily ping stays on: it names the config key that turns both off.
func NoticeSkipped(cause string) string {
	return "pfm: install ping not sent (" + cause + "); the daily ping stays on — " + optOut
}

// SendInstall sends the event=install ping and claims today, so the daily ping
// waits for tomorrow. Nothing is sent when Reason names a cause. A failure is
// logged WARN and returned; the caller's command never fails on it.
func SendInstall(ctx context.Context, options Options) error {
	if Reason(options) != "" {
		return nil
	}
	today := day(options.Clock)
	_, err := claimDay(options.StateDir, today)
	if err == nil {
		pruneDaysLogged(ctx, options, today)
		err = post(ctx, options, EventInstall)
	}
	return warnNotSent(ctx, options, EventInstall, err)
}

// SendDaily sends the event=daily ping unless this UTC day is already claimed,
// claiming it with an exclusive create before it sends, so concurrent carriers
// send it once; a failed send is not retried that day. Nothing is sent when
// Reason names a cause. A failure is logged WARN and returned.
func SendDaily(ctx context.Context, options Options) error {
	if Reason(options) != "" {
		return nil
	}
	today := day(options.Clock)
	claimed, err := claimDay(options.StateDir, today)
	if err == nil && !claimed {
		return nil
	}
	if err == nil {
		pruneDaysLogged(ctx, options, today)
		err = post(ctx, options, EventDaily)
	}
	return warnNotSent(ctx, options, EventDaily, err)
}

// pruneDaysLogged drops the earlier days' claims; a failure costs only a
// leftover file, so it is logged WARN and the ping still goes out.
func pruneDaysLogged(ctx context.Context, options Options, today string) {
	if err := pruneDays(options.StateDir, today); err != nil {
		obs.Logger(ctx).Warn("install ping: earlier day claims not removed",
			"path", options.StateDir, obs.FieldErr, err.Error())
	}
}

// day is the clock's UTC date, the counter's day key.
func day(clk clock.Clock) string {
	return clk.Now().UTC().Format(time.DateOnly)
}

func warnNotSent(ctx context.Context, options Options, event string, err error) error {
	if err != nil {
		obs.Logger(ctx).Warn("install ping: not sent",
			"kind", event, "target", options.Endpoint, "path", options.StateDir, obs.FieldErr, err.Error())
	}
	return err
}
