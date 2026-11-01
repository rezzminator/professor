package paths

import "testing"

func TestOfflineSwitchesReadOnlyTheirOwnVariableSetToOne(t *testing.T) {
	switches := []struct {
		name string
		read func(Env) bool
	}{
		{EnvSkillSourcesOffline, SkillSourcesOfflineIn},
		{EnvThemesOffline, ThemesOfflineIn},
		{EnvPricesOffline, PricesOfflineIn},
		{EnvUpdateCheckOffline, UpdateCheckOfflineIn},
		{EnvUsageOffline, UsageOfflineIn},
	}
	for _, under := range switches {
		for _, row := range []struct {
			value string
			want  bool
		}{{"1", true}, {"", false}, {"0", false}, {"true", false}} {
			env := &MapEnv{Values: map[string]string{under.name: row.value}}
			for _, other := range switches {
				want := row.want && other.name == under.name
				if got := other.read(env); got != want {
					t.Errorf("%s=%q: %s reads %v, want %v", under.name, row.value, other.name, got, want)
				}
			}
		}
	}
	t.Setenv(EnvUpdateCheckOffline, "1")
	if !UpdateCheckOffline() {
		t.Errorf("UpdateCheckOffline() with %s=1 in the process environment = false, want true", EnvUpdateCheckOffline)
	}
}
