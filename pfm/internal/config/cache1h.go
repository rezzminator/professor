package config

import "hostops/pfm/internal/paths"

// InitialCache1H resolves the prompt-cache TTL for a newly launched chat.
func (config Config) InitialCache1H(account int) bool {
	return config.InitialCache1HFrom(paths.OSEnv{}, account)
}

// InitialCache1HFrom applies the cache policy over an injected environment.
func (config Config) InitialCache1HFrom(env paths.Env, account int) bool {
	if value, ok := env.Lookup("CC_ARM_1H"); ok {
		return value == "1"
	}
	if value, ok := env.Lookup("ENABLE_PROMPT_CACHING_1H"); ok {
		return value == "1" && env.Get("CLAUDECODE") == ""
	}
	return config.EffectiveClaude(account).Cache1H
}
