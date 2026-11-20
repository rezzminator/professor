package installer

import (
	"os"
	"strings"
	"testing"
)

// TestHerdrKeysBlockIsWrittenAdoptedOrLeftAlone drives install's keys step
// over every config shape it can meet: none, other tables only, pfm's own
// block, an identical hand-written table, a differing one, an old block.
func TestHerdrKeysBlockIsWrittenAdoptedOrLeftAlone(t *testing.T) {
	const theme = "[theme]\nname = \"catppuccin\"\n"
	identical := "[keys]\nnext_workspace = 'prefix+m'\nprevious_workspace = \"prefix+i\"\n\n" +
		"[[keys.command]]\ndescription = \"new pfm chat in this space\"\nkey = \"prefix+a\"\ntype = \"shell\"\n" +
		"command = '''\"${HERDR_BIN_PATH:-herdr}\" tab create --workspace \"${HERDR_ACTIVE_WORKSPACE_ID:-$HERDR_WORKSPACE_ID}\" --env PFM_AUTO_OPEN=pfm --focus'''\n"
	differing := "[keys]\nprevious_workspace = \"cmd+shift+up\"\nnext_workspace = [\"cmd+shift+down\", \"prefix+n\"]\n\n" +
		"[[keys.command]]\nkey = \"prefix+z\"\ntype = \"shell\"\ncommand = \"true\"\n"
	oldBlock := herdrKeysOpen + "[keys]\nprevious_workspace = \"prefix+i\"\n" + herdrKeysClose
	cases := []struct {
		name, original, want, line string
	}{
		{
			name: "empty config", original: "", want: herdrKeysBlock,
			line: "  change  wire herdr keys in {path}",
		},
		{
			name: "other tables only", original: theme, want: theme + "\n" + herdrKeysBlock,
			line: "  change  wire herdr keys in {path}",
		},
		{
			name: "owned block", original: theme + "\n" + herdrKeysBlock, want: theme + "\n" + herdrKeysBlock,
			line: "  ok      herdr keys: wired {path}",
		},
		{
			name: "identical hand-written table", original: theme + "\n" + identical + "\n[ui]\nsound = false\n",
			want: theme + "\n" + herdrKeysBlock + "\n[ui]\nsound = false\n",
			line: "  change  adopt hand-written [keys] into pfm's block in {path}",
		},
		{
			name: "differing hand-written table", original: differing + "\n" + theme, want: differing + "\n" + theme,
			line: "  skip    herdr keys: hand-written [keys] in {path} left alone; it lacks " +
				`previous_workspace = "prefix+i", next_workspace = "prefix+m", ` +
				`[[keys.command]] key = "prefix+a" (new pfm chat in this space)`,
		},
		{
			name: "old block", original: theme + "\n" + oldBlock, want: theme + "\n" + herdrKeysBlock,
			line: "  change  update herdr keys in {path}",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			installer, config, output := herdrShellInstaller(t, testCase.original, true)
			if err := installer.wireHerdrKeys(false); err != nil {
				t.Fatalf("wireHerdrKeys: %v\n%s", err, output)
			}
			got, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			wantLine := strings.ReplaceAll(testCase.line, "{path}", config) + "\n"
			if string(got) != testCase.want || output.String() != wantLine {
				t.Errorf("config:\n%s\nwant:\n%s\noutput %q, want %q", got, testCase.want, output.String(), wantLine)
			}
		})
	}
}

func TestHerdrKeysUninstallRemovesOnlyTheBlockAndAbsentHerdrSkips(t *testing.T) {
	const theme = "[theme]\nname = \"catppuccin\"\n"
	installer, config, output := herdrShellInstaller(t, theme+"\n"+herdrKeysBlock, true)
	if err := installer.wireHerdrKeys(true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(config); string(got) != theme+"\n" {
		t.Errorf("uninstall left %q, want %q", got, theme+"\n")
	}
	// A config TOML refuses (a hand-written [keys] beside the block) still
	// loses the block at uninstall, found by its markers.
	broken, brokenConfig, _ := herdrShellInstaller(t, "[keys]\nx = 1\n"+herdrKeysBlock, true)
	if err := broken.wireHerdrKeys(true); err != nil {
		t.Fatalf("uninstall over an unparsable config: %v", err)
	}
	if got, _ := os.ReadFile(brokenConfig); string(got) != "[keys]\nx = 1\n" {
		t.Errorf("uninstall over an unparsable config left %q, want %q", got, "[keys]\nx = 1\n")
	}
	absent, _, absentOutput := herdrShellInstaller(t, "", false)
	if err := absent.wireHerdrKeys(false); err != nil {
		t.Fatal(err)
	}
	if want := "  skip    herdr keys: herdr absent\n"; absentOutput.String() != want {
		t.Errorf("absent herdr said %q, want %q (uninstall said %q)", absentOutput.String(), want, output.String())
	}
}
