package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"

	"github.com/rezzminator/professor/pfm/internal/clock"
)

// TabStatusOff disables tab decorations without changing sidebar indicators.
const TabStatusOff = "off"

const tabPaneListResult = "pane_list"

// TabDecoration binds an inspection to a pane incarnation and generation.
// Validate rechecks the producing session immediately before a rename.
type TabDecoration struct {
	Pane, Terminal, Tab, Identity, Name, State, Mode, AgentStatus string
	Revision                                                      uint64
	Session                                                       json.RawMessage
	Validate                                                      func(context.Context) (bool, error)
}

// TabProvider enumerates the chats displayed on one Herdr endpoint.
type TabProvider func(context.Context, string) ([]TabDecoration, error)

// DecoratedTabLabel reserves prefix runes before truncating the clean base.
// Every emoji occupies two columns; the off phase is a hollow square, not
// whitespace that TabRename's cleaning would strip. Text markers use three.
func DecoratedTabLabel(base, state, mode string, on bool) string {
	base = strings.Join(strings.Fields(TabLabel(base)), " ")
	if mode == TabStatusOff || state == "" {
		return TabLabel(base)
	}
	glyph, text := "❔", "[?]"
	switch state {
	case "working":
		glyph, text = "🟡", "[W]"
	case "idle":
		glyph, text = "🟢", "[I]"
	case string(StateBlocked):
		glyph, text = "🔴", "[B]"
	case "error":
		glyph, text = "🟥", "[E]"
	case "dead":
		glyph, text = "⬛", "[D]"
	}
	if !on && (state == string(StateBlocked) || state == "error") {
		glyph, text = "⬜", "[ ]"
	}
	if mode == "text" {
		glyph = text
	}
	prefix := glyph + " "
	runes := []rune(base)
	limit := MaxTitleRunes - len([]rune(prefix))
	if len(runes) > limit {
		base = string(runes[:limit])
	}
	return prefix + base
}

// TabPane is the producer's PaneInfo projection used for ownership checks.
// Focused is a pointer so a missing mandatory field is not a false observation.
type TabPane struct {
	Pane      string          `json:"pane_id"`
	Terminal  string          `json:"terminal_id"`
	Workspace string          `json:"workspace_id"`
	Tab       string          `json:"tab_id"`
	Focused   *bool           `json:"focused"`
	Revision  uint64          `json:"revision"`
	Session   json.RawMessage `json:"agent_session"`
	State     string          `json:"agent_status"`
}

func (pane TabPane) valid() bool {
	return pane.Pane != "" && pane.Terminal != "" && pane.Workspace != "" && pane.Tab != "" && pane.Focused != nil
}

// ReadTabPanes validates pane.list's identity fields at data entry.
func ReadTabPanes(ctx context.Context, socket string) ([]TabPane, error) {
	body, err := SeatRequest(ctx, socket, "pane.list", struct{}{})
	if err != nil {
		return nil, fmt.Errorf("list tab panes: %w", err)
	}
	var result struct {
		Type  string    `json:"type"`
		Panes []TabPane `json:"panes"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decode tab panes: %w", err)
	}
	if result.Type != tabPaneListResult {
		return nil, fmt.Errorf("tab panes: unexpected result %s", body)
	}
	for _, pane := range result.Panes {
		if !pane.valid() {
			return nil, fmt.Errorf("tab panes: incomplete PaneInfo for %q", pane.Pane)
		}
	}
	return result.Panes, nil
}

func readTabPane(
	ctx context.Context,
	socket, pane string,
	call func(context.Context, string, ...[]byte) ([]json.RawMessage, error),
) (TabPane, error) {
	line, err := json.Marshal(
		request{ID: "pfm-tab-pane", Method: "pane.get", Params: map[string]string{"pane_id": pane}},
	)
	if err != nil {
		return TabPane{}, fmt.Errorf("encode tab pane: %w", err)
	}
	bodies, err := call(ctx, socket, line)
	if err != nil {
		return TabPane{}, err
	}
	if len(bodies) != 1 {
		return TabPane{}, fmt.Errorf("pane.get returned %d results", len(bodies))
	}
	var result struct {
		Type string  `json:"type"`
		Pane TabPane `json:"pane"`
	}
	if err := json.Unmarshal(bodies[0], &result); err != nil {
		return TabPane{}, fmt.Errorf("decode tab pane: %w", err)
	}
	if result.Type != "pane_info" || !result.Pane.valid() {
		return TabPane{}, fmt.Errorf("incomplete pane.get: %s", bodies[0])
	}
	return result.Pane, nil
}

func sameTabObservation(pane TabPane, d TabDecoration) bool {
	var a, b any
	if len(pane.Session) > 0 {
		if err := json.Unmarshal(pane.Session, &a); err != nil {
			return false
		}
	}
	if len(d.Session) > 0 {
		if err := json.Unmarshal(d.Session, &b); err != nil {
			return false
		}
	}
	return pane.Pane == d.Pane && pane.Terminal == d.Terminal && pane.Tab == d.Tab && pane.Revision == d.Revision &&
		pane.State == d.AgentStatus &&
		reflect.DeepEqual(a, b)
}

func reconcileTab(
	ctx context.Context,
	socket, dir string,
	d TabDecoration,
	on bool,
	call func(context.Context, string, ...[]byte) ([]json.RawMessage, error),
) (wrote bool, returnErr error) {
	if d.Tab == "" || d.Terminal == "" || d.Identity == "" {
		return false, fmt.Errorf("tab observation has incomplete ownership: %+v", d)
	}
	valid, err := validateTabOwner(ctx, d)
	if err != nil || !valid {
		return false, err
	}
	err = withTabLabelLock(ctx, dir, socket, d.Tab, func(path string) error {
		pane, err := readTabPane(ctx, socket, d.Pane, call)
		if err != nil {
			return err
		}
		if !sameTabObservation(pane, d) {
			return nil
		}
		record, err := readTabLabel(path)
		if err != nil {
			return err
		}
		before := record
		valid, err := validateTabOwner(ctx, d)
		if err != nil || !valid {
			return err
		}
		// Repeat the identity read after potentially slow validation. Herdr has no
		// conditional rename: this minimizes, but cannot eliminate, external races.
		pane, err = readTabPane(ctx, socket, d.Pane, call)
		if err != nil {
			return err
		}
		if !sameTabObservation(pane, d) {
			return nil
		}
		get, err := (TabGet{Tab: d.Tab, Seq: 1}).Line()
		if err != nil {
			return err
		}
		results, err := call(ctx, socket, get)
		if err != nil {
			return err
		}
		if len(results) != 1 {
			return fmt.Errorf("tab.get returned %d results", len(results))
		}
		tab, err := ParseTab(results[0])
		if err != nil {
			return err
		}
		if tab.TabID != d.Tab || tab.PaneCount != 1 {
			return nil
		}
		if d.Mode == TabStatusOff {
			if record.Identity != d.Identity || record.Rendered != tab.Label {
				return nil
			}
		} else {
			switch {
			case record.Identity != "" && record.Identity != d.Identity:
				record = tabLabelRecord{Base: d.Name}
			case record.Base == "":
				record.Base = d.Name
			case record.Rendered != tab.Label && tab.Label != record.Base:
				// Treat an external label as literal text, never strip prefixes.
				record.Base = tab.Label
			}
		}
		record.Identity, record.State, record.Mode, record.On = d.Identity, d.State, d.Mode, on
		want := DecoratedTabLabel(record.Base, d.State, d.Mode, on)
		if tab.Label != want {
			rename, err := (TabRename{Tab: d.Tab, Label: want, Seq: 2}).Line()
			if err != nil {
				return err
			}
			if _, err := call(ctx, socket, rename); err != nil {
				return fmt.Errorf("rename tab %s: %w", d.Tab, err)
			}
			wrote = true
		}
		record.Rendered = want
		if record == before {
			return nil
		}
		return writeTabLabel(path, record)
	})
	if tabTargetGone(err) {
		return false, nil
	}
	if err != nil {
		return wrote, fmt.Errorf("tab status %s on %s: %w", d.Tab, socket, err)
	}
	return wrote, nil
}

func tabTargetGone(err error) bool {
	var refusal *Refusal
	return errors.As(err, &refusal) && (refusal.Code == "pane_not_found" || refusal.Code == "tab_not_found")
}

// PaintIndicatorsWithTabs reuses the sidebar clock, failure bound and singleton.
func PaintIndicatorsWithTabs(
	ctx context.Context,
	socket, lockDir string,
	stderr io.Writer,
	tabs TabProvider,
	proof PaneProof,
) error {
	if stderr == nil {
		stderr = io.Discard
	}
	painter := &indicatorPainter{
		socket:   socket,
		lockDir:  lockDir,
		stderr:   stderr,
		call:     Call,
		clock:    clock.Real,
		reported: map[string]bool{},
		tabs:     tabs, provePane: proof,
	}
	return painter.run(ctx)
}

func (painter *indicatorPainter) restoreTabs(ctx context.Context) error {
	for i := range painter.tabObservations {
		d := painter.tabObservations[i]
		valid, err := validateTabOwner(ctx, d)
		if err != nil {
			return err
		}
		if !valid {
			continue
		}
		pane, err := readTabPane(ctx, painter.socket, d.Pane, painter.call)
		if err != nil {
			if tabTargetGone(err) {
				continue
			}
			return fmt.Errorf("restore tab %s: %w", d.Tab, err)
		}
		d.Revision = pane.Revision
		d.AgentStatus = pane.State
		d.Mode = TabStatusOff
		if _, err := reconcileTab(ctx, painter.socket, painter.lockDir, d, true, painter.call); err != nil {
			return err
		}
	}
	return nil
}

func validateTabOwner(ctx context.Context, d TabDecoration) (bool, error) {
	if d.Validate == nil {
		return false, fmt.Errorf("tab %s ownership unproved: no validator", d.Tab)
	}
	valid, err := d.Validate(ctx)
	if err != nil {
		return false, fmt.Errorf("tab %s ownership unproved: %w", d.Tab, err)
	}
	return valid, nil
}
