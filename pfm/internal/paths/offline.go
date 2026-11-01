package paths

// The offline switches: each set to 1 stops one network fetch, so a test run
// and the fence lanes reach no public server; testjail sets every one.
const (
	// EnvSkillSourcesOffline=1 stops pfm install from fetching the
	// source-fetched global skills (templates/global/skills/sources.json):
	// an existing store copy is still linked, and pfm doctor reports an
	// unfetched skill as OFFLINE rather than a warning. The e2e harness sets
	// it so no test reaches a public repository.
	EnvSkillSourcesOffline = "PFM_SKILL_SOURCES_OFFLINE"
	// EnvThemesOffline=1 stops pfm install from downloading the release
	// profiles; the profiles in the source clone still install.
	// The test harness sets it so no test fetches themes from a public repository.
	EnvThemesOffline = "PFM_THEMES_OFFLINE"
	// EnvPricesOffline=1 stops every price refresh — pfm model-cost, pfm
	// install, pfm doctor — from fetching the publishers' pricing pages; the
	// served table stays the clone file or the embedded copy. The test harness
	// and the fence lanes set it so no test fetches a pricing page.
	EnvPricesOffline = "PFM_PRICES_OFFLINE"
	// EnvUpdateCheckOffline=1 stops the picker's background update check from
	// asking GitHub for the latest Professor release; the cached notice and
	// failure marker stay as they are. The test harness sets it so no test
	// reaches GitHub.
	EnvUpdateCheckOffline = "PFM_UPDATE_CHECK_OFFLINE"
	// EnvUsageOffline=1 stops every usage fetch from calling the provider's
	// usage endpoint; a fetch reports ErrUsageOffline instead. The test
	// harness and the fence lanes set it so no test sends a credential to
	// the provider; the production builders read it into the fetch options.
	EnvUsageOffline = "PFM_USAGE_OFFLINE"
)

// SkillSourcesOffline reports EnvSkillSourcesOffline=1: pfm install fetches no
// source-fetched global skill and pfm doctor reports an unfetched one OFFLINE.
func SkillSourcesOffline() bool {
	return SkillSourcesOfflineIn(OSEnv{})
}

// SkillSourcesOfflineIn is SkillSourcesOffline read from env, the environment
// a caller was handed.
func SkillSourcesOfflineIn(env Env) bool {
	return env.Get(EnvSkillSourcesOffline) == "1"
}

// ThemesOffline reports EnvThemesOffline=1: pfm install reads the profiles
// in the source clone but downloads no release profile.
func ThemesOffline() bool {
	return ThemesOfflineIn(OSEnv{})
}

// ThemesOfflineIn is ThemesOffline read from env, the environment a caller was handed.
func ThemesOfflineIn(env Env) bool {
	return env.Get(EnvThemesOffline) == "1"
}

// PricesOffline reports EnvPricesOffline=1: no price refresh fetches the
// publishers' pricing pages.
func PricesOffline() bool {
	return PricesOfflineIn(OSEnv{})
}

// PricesOfflineIn is PricesOffline read from env, the environment a caller was handed.
func PricesOfflineIn(env Env) bool {
	return env.Get(EnvPricesOffline) == "1"
}

// UpdateCheckOffline reports EnvUpdateCheckOffline=1: the update check asks
// no release server.
func UpdateCheckOffline() bool {
	return UpdateCheckOfflineIn(OSEnv{})
}

// UpdateCheckOfflineIn is UpdateCheckOffline read from env, the environment a caller was handed.
func UpdateCheckOfflineIn(env Env) bool {
	return env.Get(EnvUpdateCheckOffline) == "1"
}

// UsageOffline reports EnvUsageOffline=1: no usage fetch calls the
// provider's usage endpoint.
func UsageOffline() bool {
	return UsageOfflineIn(OSEnv{})
}

// UsageOfflineIn is UsageOffline read from env, the environment a caller was handed.
func UsageOfflineIn(env Env) bool {
	return env.Get(EnvUsageOffline) == "1"
}
