package config

import "os"

// InitialCache1H resolves the prompt-cache TTL for a newly launched chat.
func (config Config) InitialCache1H(account int) bool {
	if value, ok := os.LookupEnv("CC_ARM_1H"); ok {
		return value == "1"
	}
	if value, ok := os.LookupEnv("ENABLE_PROMPT_CACHING_1H"); ok {
		return value == "1" && os.Getenv("CLAUDECODE") == ""
	}
	return config.EffectiveClaude(account).Cache1H
}
