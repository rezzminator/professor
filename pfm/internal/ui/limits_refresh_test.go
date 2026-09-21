package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/rezzminator/professor/pfm/internal/compose"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	pfmstats "github.com/rezzminator/professor/pfm/internal/stats"
)

// contextLimitsSampler is deliberately incompatible with the legacy limits
// path: a test that accidentally calls Sample instead of the optional live
// method fails loudly, while each live call records the context identity.
type contextLimitsSampler struct {
	contexts []context.Context
	calls    int
}

func (sampler *contextLimitsSampler) Sample([]compose.Row) (pfmstats.Snapshot, error) {
	return pfmstats.Snapshot{}, errors.New("legacy full sample was called")
}

func (sampler *contextLimitsSampler) SampleLiveLimits(ctx context.Context) pfmstats.Snapshot {
	sampler.contexts = append(sampler.contexts, ctx)
	sampler.calls++
	return pfmstats.Snapshot{
		Ready:      true,
		SampleTime: fixtureNowNS + int64(time.Minute),
		Limits: []pfmstats.AccountLimits{{
			Account: 1,
			Engine:  pfmengine.Claude,
			Windows: []pfmstats.Window{{Name: "5h", UsedPct: 10}},
		}},
	}
}

func enterLimitsWithContext(t *testing.T, snapshot Snapshot) (Model, tea.Cmd) {
	t.Helper()
	model := NewModel(snapshot)
	model, _ = applyKey(t, model, specialKey(tea.KeyTab))
	model, command := applyKey(t, model, specialKey(tea.KeyTab))
	if model.tab != TabLimits {
		t.Fatalf("tab = %d, want Limits", model.tab)
	}
	return model, command
}

func TestLimitsClockTickAdvancesLongIdleAgeAndResetCountdown(t *testing.T) {
	snapshot := fixtureSnapshot(120)
	snapshot.NoSky = true
	model := NewModel(snapshot)
	model.tab = TabLimits
	base := time.Unix(0, model.nowNS)
	model.stats = pfmstats.Snapshot{
		Ready: true,
		Limits: []pfmstats.AccountLimits{{
			Account:     1,
			Engine:      pfmengine.Claude,
			ConfirmedAt: base.Add(-59 * time.Minute),
			Windows: []pfmstats.Window{{
				Name:    "5h",
				UsedPct: 42,
				ResetAt: base.Add(2*time.Hour + time.Minute),
			}},
		}},
	}

	initial := ansi.Strip(model.renderLimitsPanel(120, 8))
	for _, want := range []string{"provider confirmed 59m ago", "↻ 2h 1m"} {
		if !strings.Contains(initial, want) {
			t.Fatalf("initial Limits panel missing %q:\n%s", want, initial)
		}
	}

	firstTick := model.nowNS + int64(time.Hour)
	updated, command := model.Update(clockTickMsg{nowNS: firstTick})
	model = updated.(Model)
	if command == nil || model.nowNS != firstTick || model.cosmosNowNS != firstTick {
		t.Fatalf(
			"first wall-clock tick: command=%v now=%d cosmosNow=%d want=%d",
			command,
			model.nowNS,
			model.cosmosNowNS,
			firstTick,
		)
	}
	middle := ansi.Strip(model.renderLimitsPanel(120, 8))
	for _, want := range []string{"provider confirmed 1h ago", "↻ 1h 1m"} {
		if !strings.Contains(middle, want) {
			t.Fatalf("one-hour Limits panel missing %q:\n%s", want, middle)
		}
	}

	finalTick := model.nowNS + int64(time.Hour+time.Minute)
	updated, command = model.Update(clockTickMsg{nowNS: finalTick})
	model = updated.(Model)
	if command == nil || model.nowNS != finalTick || model.cosmosNowNS != finalTick {
		t.Fatalf(
			"second wall-clock tick: command=%v now=%d cosmosNow=%d want=%d",
			command,
			model.nowNS,
			model.cosmosNowNS,
			finalTick,
		)
	}
	final := ansi.Strip(model.renderLimitsPanel(120, 8))
	for _, want := range []string{"provider confirmed 3h ago", "↻ refreshing…"} {
		if !strings.Contains(final, want) {
			t.Fatalf("long-idle Limits panel missing %q:\n%s", want, final)
		}
	}
}

func TestStatsSampleTimeAdvancesBothClocksAndNeverRewinds(t *testing.T) {
	model := NewModel(fixtureSnapshot(80))
	model.tab = TabLimits
	model.statsGeneration = 1

	clocked := fixtureNowNS + int64(time.Hour)
	updated, _ := model.Update(clockTickMsg{nowNS: clocked})
	model = updated.(Model)

	fresh := clocked + int64(time.Hour)
	updated, _ = model.Update(statsSampleMsg{
		generation: 1,
		snapshot:   pfmstats.Snapshot{SampleTime: fresh},
	})
	model = updated.(Model)
	if model.nowNS != fresh || model.cosmosNowNS != fresh {
		t.Fatalf(
			"fresh SampleTime did not advance both clocks: now=%d cosmosNow=%d want=%d",
			model.nowNS,
			model.cosmosNowNS,
			fresh,
		)
	}

	older := fresh - int64(30*time.Minute)
	updated, _ = model.Update(statsSampleMsg{
		generation: 1,
		snapshot:   pfmstats.Snapshot{SampleTime: older},
	})
	model = updated.(Model)
	if model.nowNS != fresh || model.cosmosNowNS != fresh {
		t.Fatalf(
			"older SampleTime rewound clocks: now=%d cosmosNow=%d older=%d fresh=%d",
			model.nowNS,
			model.cosmosNowNS,
			older,
			fresh,
		)
	}
}

func TestClockThenLateCosmosTickCannotRewindLedger(t *testing.T) {
	model := cosmosTestModel(80, false)
	model.tab = TabCosmos
	newNow := model.cosmosNowNS + int64(2*time.Hour)
	updated, command := model.Update(clockTickMsg{nowNS: newNow})
	model = updated.(Model)
	if command == nil || model.cosmosNowNS != newNow {
		t.Fatalf(
			"clock tick did not advance cosmos clock: command=%v now=%d want=%d",
			command,
			model.cosmosNowNS,
			newNow,
		)
	}

	late := newNow - int64(30*time.Minute)
	updated, _ = model.Update(cosmosTickMsg{
		generation: model.cosmosTickGeneration,
		nowNS:      late,
	})
	model = updated.(Model)
	if model.cosmosNowNS != newNow {
		t.Fatalf("late cosmos tick rewound ledger: now=%d late=%d fresh=%d", model.cosmosNowNS, late, newNow)
	}
}

func TestLimitsSamplerKeepsTabContextAcrossPollsAndCancelsOnExit(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	sampler := &contextLimitsSampler{}
	snapshot := fixtureSnapshot(80)
	snapshot.StatsSampler = sampler
	snapshot.SamplingContext = parent
	model, command := enterLimitsWithContext(t, snapshot)
	if command == nil {
		t.Fatal("entering Limits returned no sample command")
	}
	firstMessage := command()
	first, ok := firstMessage.(statsSampleMsg)
	if !ok {
		t.Fatalf("Limits command returned %T", firstMessage)
	}
	if sampler.calls != 1 || len(sampler.contexts) != 1 {
		t.Fatalf("first live sample calls=%d contexts=%d, want one", sampler.calls, len(sampler.contexts))
	}
	limitsContext := sampler.contexts[0]
	if limitsContext == parent || limitsContext.Err() != nil {
		t.Fatalf(
			"first Limits context = %p parent=%p err=%v, want live tab child",
			limitsContext,
			parent,
			limitsContext.Err(),
		)
	}

	updated, wait := model.Update(first)
	model = updated.(Model)
	if wait == nil {
		t.Fatal("first Limits sample returned no normal polling command")
	}
	updated, command = model.Update(statsTickMsg{generation: model.statsGeneration})
	model = updated.(Model)
	if command == nil {
		t.Fatal("normal Limits poll returned no sample command")
	}
	secondMessage := command()
	second, ok := secondMessage.(statsSampleMsg)
	if !ok {
		t.Fatalf("normal Limits poll returned %T", secondMessage)
	}
	if sampler.calls != 2 || len(sampler.contexts) != 2 || sampler.contexts[1] != limitsContext {
		t.Fatalf("poll contexts=%#v, want the same tab-lifetime context twice", sampler.contexts)
	}
	if limitsContext.Err() != nil {
		t.Fatalf("normal poll canceled Limits context: %v", limitsContext.Err())
	}

	updated, _ = model.Update(second)
	model = updated.(Model)
	model, _ = applyKey(t, model, specialKey(tea.KeyTab))
	if model.tab != TabCosmos {
		t.Fatalf("leaving Limits landed on tab %d, want Cosmos", model.tab)
	}
	if limitsContext.Err() != context.Canceled {
		t.Fatalf("leaving Limits context err=%v, want context.Canceled", limitsContext.Err())
	}
}

func TestLimitsSamplerContextFollowsParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	sampler := &contextLimitsSampler{}
	snapshot := fixtureSnapshot(80)
	snapshot.StatsSampler = sampler
	snapshot.SamplingContext = parent
	model, _ := enterLimitsWithContext(t, snapshot)
	limitsContext := model.limitsContext
	if limitsContext == nil {
		t.Fatal("entering Limits did not create a tab context")
	}
	cancelParent()
	if limitsContext.Err() != context.Canceled {
		t.Fatalf("parent cancellation left Limits context err=%v", limitsContext.Err())
	}
}
