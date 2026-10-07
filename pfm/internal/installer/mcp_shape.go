package installer

// withoutEmptyEnv is the one normalization every exact-shape comparison of a
// Claude registration applies: Claude Code adds `"env": {}` whenever it
// rewrites its config, so an env that is present but empty ({} or null) is
// shape-neutral and is dropped from the copy returned. A non-empty env, or any
// other value under the key, is kept, so the entry stays someone else's.
func withoutEmptyEnv(registration map[string]any) map[string]any {
	env, present := registration[configEnvKey]
	if !present {
		return registration
	}
	if env != nil {
		if values, ok := env.(map[string]any); !ok || len(values) != 0 {
			return registration
		}
	}
	neutral := make(map[string]any, len(registration)-1)
	for key, value := range registration {
		if key != configEnvKey {
			neutral[key] = value
		}
	}
	return neutral
}
