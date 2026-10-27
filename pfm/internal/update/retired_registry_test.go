package update

import "testing"

func TestRetiredRegistryNames(t *testing.T) {
	t.Parallel()
	names := RetiredRegistryNames()
	for kind, want := range map[string][]string{
		"agent": {
			"rndier", "tracer-pro", "ultimate", "terra", "imagine", "imagine-historian",
			"foreman-pro", "gpt-foreman-pro", "flights-mechanical-executor", "gpt-flights-mechanical-executor",
			"gpt-flights-speccer",
		},
		"command": {
			"wave", "jc", "qa/live", "pfm/refresh", "quality/forlint", "chat",
			"imagine/build", "imagine/build-fast", "imagine/build-spec", "imagine/callmeter", "imagine/evolve",
			"flights/spec", "flights/init", "flights/orchestrate-cross-harness",
		},
		"skill": {"wave-builder", "transcript", "close-out", "imagine-core"},
	} {
		set := map[string]map[string]bool{"agent": names.Agents, "command": names.Commands, "skill": names.Skills}[kind]
		for _, name := range want {
			if !set[name] {
				t.Errorf("%s %q is not a retired registry name: %v", kind, name, set)
			}
		}
	}
	if names.Agents["flights-speccer"] {
		t.Error("agent flights-speccer ships again (templates/global/agents) but is a retired registry name")
	}
	for _, phrase := range []string{`subagent_type="ultimate"`, "pfm chat inject", "transcript skill", "mcp__professor__", "JC-COMMIT", "/wave", "mono-architect/"} {
		for kind, set := range map[string]map[string]bool{"agent": names.Agents, "command": names.Commands, "skill": names.Skills} {
			if set[phrase] {
				t.Errorf("%s set holds %q, which is not one registry entry", kind, phrase)
			}
		}
	}
}
