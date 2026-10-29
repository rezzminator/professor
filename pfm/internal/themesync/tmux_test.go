package themesync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/gather"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestTmuxOptionsRenderStylesAndUserOptions(t *testing.T) {
	options := tmuxOptions(tokyoNightApplied(t), Dark, 3)
	byName := map[string]string{}
	for _, option := range options {
		byName[option.Name] = option.Value
	}
	if len(options) != len(byName) || len(options) != 30 {
		t.Fatalf("got %d options (%d unique), want 30", len(options), len(byName))
	}
	format := "#{?#{==:#{@pfm-state},blocked},#ffd23f,#{?#{==:#{@pfm-state},working},#2d9bff," +
		"#{?#{==:#{@pfm-state},done},#00ffa3,#9b8fd6}}}"
	want := map[string]string{
		"status-style":                "bg=#221634,fg=#ff9ed6",
		"window-status-style":         "fg=#7a6fb0",
		"window-status-current-style": "fg=" + format + ",bold",
		"pane-border-style":           "fg=#7a6fb0",
		"pane-active-border-style":    "fg=" + format,
		"message-style":               "bg=#4a3d6b,fg=#ff9ed6",
		"message-command-style":       "bg=#221634,fg=#c95cff",
		"mode-style":                  "bg=#3a2d6b,fg=#ff9ed6",
		"@host_accent":                "#c95cff",
		"@p_g0":                       "#221634",
		"@p_g3":                       "#0f0f1a",
		"@p_fg":                       "#ff9ed6",
		"@p_fg1":                      "#7a6fb0",
		"@p_fg2":                      "#7a6fb0",
		"@p_red":                      "#ff3d6e",
		"@p_amber":                    "#ffd23f",
		"@pfm-theme-chips":            "1",
		"@pfm-theme":                  "custom:tokyo-night",
		"@pfm-theme-generation":       "3",
		"@pfm-c-bg":                   "#0f0f1a",
		"@pfm-c-accent":               "#c95cff",
		"@pfm-state-blocked":          "#ffd23f",
		"@pfm-state-idle":             "#9b8fd6",
		"@pfm-state-colour":           format,
	}
	for name, value := range want {
		if byName[name] != value {
			t.Errorf("%s = %q, want %q", name, byName[name], value)
		}
	}
	for _, option := range options {
		if strings.ContainsAny(option.Value, "\n\x00") {
			t.Errorf("%s value holds a control character: %q", option.Name, option.Value)
		}
	}
}

func TestTmuxPriorOptionNameAndSentinel(t *testing.T) {
	if got := tmuxPriorName("status-style"); got != "@pfm-theme-prior-status-style" {
		t.Fatalf("tmuxPriorName = %q", got)
	}
	if got := tmuxPriorValue(""); got != "v:" {
		t.Fatalf("an empty prior must still be a non-empty sentinel, got %q", got)
	}
	if value, ok := tmuxPriorParse("v:bg=red"); !ok || value != "bg=red" {
		t.Fatalf("tmuxPriorParse = %q, %v", value, ok)
	}
	if _, ok := tmuxPriorParse(""); ok {
		t.Fatal("an empty read is not a captured prior")
	}
}

// The real gather probe executes vectors sequentially, not as an atomic batch.
// Its executable fixture persists each option before the next vector can fail.
type prefixFaultTmux struct {
	probe     gather.TmuxProbe
	dir       string
	remaining int
	commands  int
}

func (f *prefixFaultTmux) Sockets(context.Context) ([]string, error) { return []string{"fixture"}, nil }

func (f *prefixFaultTmux) Show(_ context.Context, _, name string) (string, error) {
	body, err := os.ReadFile(filepath.Join(f.dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrTmuxOptionUnset
	}
	return string(body), err
}

func (f *prefixFaultTmux) Apply(ctx context.Context, socket string, commands [][]string) error {
	f.commands += len(commands)
	if f.remaining >= 0 {
		if f.remaining < len(commands) {
			prefix := append([][]string{}, commands[:f.remaining]...)
			prefix = append(prefix, []string{"injected-prefix-fault"})
			f.remaining = -1
			return f.probe.ApplyGlobalOptions(ctx, socket, prefix)
		}
		f.remaining -= len(commands)
	}
	return f.probe.ApplyGlobalOptions(ctx, socket, commands)
}

func TestTmuxRestoreEveryPrefixIsRetrySafe(t *testing.T) {
	ctx := context.Background()
	// Includes data restoration, ownership-marker removal and prior cleanup.
	restoreVectors := 2*(len(tmuxStyleNames)+len(tmuxChipNames)) + 15
	for prefix := 0; prefix < restoreVectors; prefix++ {
		t.Run(fmt.Sprint(prefix), func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			binary := filepath.Join(dir, "tmux-fixture")
			script := "#!/bin/sh\nshift 2\n[ \"$1\" = set-option ] || exit 73\n" +
				"if [ \"$2\" = -g ]; then printf '%s' \"$4\" > \"" + dir + "/$3\"; else rm -f \"" + dir + "/$3\"; fi\n"
			if err := testjail.WriteExecutable(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			f := &prefixFaultTmux{probe: gather.TmuxProbe{Binary: binary, TmuxTmpDir: dir}, dir: dir, remaining: -1}
			want := map[string]string{}
			for _, name := range append(append([]string{}, tmuxStyleNames...), tmuxChipNames...) {
				// Include both a captured empty value and a genuinely unset chip.
				if name == "@p_red" {
					continue
				}
				want[name] = "prior-" + name
				if name == "@p_fg" {
					want[name] = ""
				}
				if err := os.WriteFile(filepath.Join(dir, name), []byte(want[name]), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			applied := tokyoNightApplied(t)
			s := &session{servers: f, state: themeState{Applied: &applied, Appearance: Dark, Generation: 1}}
			if err := s.tmuxApplySocket(ctx, "fixture"); err != nil {
				t.Fatal(err)
			}
			f.commands, f.remaining = 0, prefix
			if err := s.tmuxRestoreSocket(ctx, "fixture"); err == nil {
				t.Fatalf("prefix %d did not reach injected failure (%d vectors)", prefix, f.commands)
			}
			if err := s.tmuxRestoreSocket(ctx, "fixture"); err != nil {
				t.Fatal(err)
			}
			for name, expected := range want {
				got, err := f.Show(ctx, "fixture", name)
				if err != nil || got != expected {
					t.Errorf("retry destroyed original %s: %q, %v; want %q", name, got, err, expected)
				}
			}
			if _, err := f.Show(ctx, "fixture", "@p_red"); !errors.Is(err, ErrTmuxOptionUnset) {
				t.Errorf("unset chip not restored: %v", err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), "@pfm-") {
					t.Errorf("cleanup left %s", entry.Name())
				}
			}
		})
	}
}
