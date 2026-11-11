//go:build darwin

package themesync

// hostPlatform is the profile a run serves when Env.Platform is nil.
var hostPlatform = darwinPlatform
