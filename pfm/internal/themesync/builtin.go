package themesync

import (
	"fmt"
	"strings"
)

// builtinThemes is Claude Code 2.1.301's six built-in themes, reduced to the
// tokens derive reads. The values are the producer's own spellings (rgb(), hex,
// ansi:name), parsed by parseColor at resolve time. An unknown theme name
// resolves to dark, Claude's own default branch (D10). Re-extract after a
// Claude release that changes built-in colours: the extraction command sits in
// the flight's research file claude-2.1.301-builtin-themes.json.
var builtinThemes = parseBuiltinThemes(builtinSource)

// builtinDefault is the theme an unknown or absent value resolves to.
const builtinDefault = "dark"

// parseBuiltinThemes reads the embedded `theme token colour` rows. The source
// is data compiled into the binary, so a malformed row is a build defect and
// panics at start-up, where builtin_test.go catches it.
func parseBuiltinThemes(source string) map[string]map[string]string {
	themes := map[string]map[string]string{}
	for _, row := range strings.Split(strings.TrimSpace(source), "\n") {
		fields := strings.Fields(row)
		if len(fields) != 3 {
			panic(fmt.Sprintf("themesync: built-in theme row %q is not `theme token colour`", row))
		}
		if themes[fields[0]] == nil {
			themes[fields[0]] = map[string]string{}
		}
		themes[fields[0]][fields[1]] = fields[2]
	}
	return themes
}

// builtinSource is one `theme token colour` row per token of each built-in.
const builtinSource = `
dark claude rgb(215,119,87)
dark inactive rgb(153,153,153)
dark inactiveShimmer rgb(193,193,193)
dark inverseText rgb(0,0,0)
dark permission rgb(177,185,249)
dark selectionBg rgb(38,79,120)
dark subtle rgb(80,80,80)
dark success rgb(78,186,101)
dark text rgb(255,255,255)
dark userMessageBackground rgb(55,55,55)
dark blue_FOR_SUBAGENTS_ONLY rgb(106,155,204)
dark cyan_FOR_SUBAGENTS_ONLY rgb(8,145,178)
dark green_FOR_SUBAGENTS_ONLY rgb(22,163,74)
dark orange_FOR_SUBAGENTS_ONLY rgb(217,119,87)
dark purple_FOR_SUBAGENTS_ONLY rgb(130,125,189)
dark red_FOR_SUBAGENTS_ONLY rgb(220,38,38)
dark yellow_FOR_SUBAGENTS_ONLY rgb(202,138,4)
light claude rgb(215,119,87)
light inactive rgb(102,102,102)
light inactiveShimmer rgb(142,142,142)
light inverseText rgb(255,255,255)
light permission rgb(87,105,247)
light selectionBg rgb(180,213,255)
light subtle rgb(175,175,175)
light success rgb(44,122,57)
light text rgb(0,0,0)
light userMessageBackground rgb(240,240,240)
light blue_FOR_SUBAGENTS_ONLY rgb(106,155,204)
light cyan_FOR_SUBAGENTS_ONLY rgb(8,145,178)
light green_FOR_SUBAGENTS_ONLY rgb(22,163,74)
light orange_FOR_SUBAGENTS_ONLY rgb(217,119,87)
light purple_FOR_SUBAGENTS_ONLY rgb(130,125,189)
light red_FOR_SUBAGENTS_ONLY rgb(220,38,38)
light yellow_FOR_SUBAGENTS_ONLY rgb(202,138,4)
dark-daltonized claude rgb(255,153,51)
dark-daltonized inactive rgb(153,153,153)
dark-daltonized inactiveShimmer rgb(193,193,193)
dark-daltonized inverseText rgb(0,0,0)
dark-daltonized permission rgb(153,204,255)
dark-daltonized selectionBg rgb(38,79,120)
dark-daltonized subtle rgb(80,80,80)
dark-daltonized success rgb(51,153,255)
dark-daltonized text rgb(255,255,255)
dark-daltonized userMessageBackground rgb(55,55,55)
dark-daltonized blue_FOR_SUBAGENTS_ONLY rgb(102,178,255)
dark-daltonized cyan_FOR_SUBAGENTS_ONLY rgb(102,204,204)
dark-daltonized green_FOR_SUBAGENTS_ONLY rgb(102,255,102)
dark-daltonized orange_FOR_SUBAGENTS_ONLY rgb(255,178,102)
dark-daltonized purple_FOR_SUBAGENTS_ONLY rgb(178,102,255)
dark-daltonized red_FOR_SUBAGENTS_ONLY rgb(255,102,102)
dark-daltonized yellow_FOR_SUBAGENTS_ONLY rgb(255,255,102)
light-daltonized claude rgb(255,153,51)
light-daltonized inactive rgb(102,102,102)
light-daltonized inactiveShimmer rgb(142,142,142)
light-daltonized inverseText rgb(255,255,255)
light-daltonized permission rgb(51,102,255)
light-daltonized selectionBg rgb(180,213,255)
light-daltonized subtle rgb(175,175,175)
light-daltonized success rgb(0,102,153)
light-daltonized text rgb(0,0,0)
light-daltonized userMessageBackground rgb(220,220,220)
light-daltonized blue_FOR_SUBAGENTS_ONLY rgb(0,102,204)
light-daltonized cyan_FOR_SUBAGENTS_ONLY rgb(0,178,178)
light-daltonized green_FOR_SUBAGENTS_ONLY rgb(0,204,0)
light-daltonized orange_FOR_SUBAGENTS_ONLY rgb(255,128,0)
light-daltonized purple_FOR_SUBAGENTS_ONLY rgb(128,0,128)
light-daltonized red_FOR_SUBAGENTS_ONLY rgb(204,0,0)
light-daltonized yellow_FOR_SUBAGENTS_ONLY rgb(255,204,0)
dark-ansi claude ansi:redBright
dark-ansi inactive ansi:white
dark-ansi inactiveShimmer ansi:whiteBright
dark-ansi inverseText ansi:black
dark-ansi permission ansi:blueBright
dark-ansi selectionBg ansi:blue
dark-ansi subtle ansi:white
dark-ansi success ansi:greenBright
dark-ansi text ansi:whiteBright
dark-ansi userMessageBackground ansi:blackBright
dark-ansi blue_FOR_SUBAGENTS_ONLY ansi:blueBright
dark-ansi cyan_FOR_SUBAGENTS_ONLY ansi:cyanBright
dark-ansi green_FOR_SUBAGENTS_ONLY ansi:greenBright
dark-ansi orange_FOR_SUBAGENTS_ONLY ansi:redBright
dark-ansi purple_FOR_SUBAGENTS_ONLY ansi:magentaBright
dark-ansi red_FOR_SUBAGENTS_ONLY ansi:redBright
dark-ansi yellow_FOR_SUBAGENTS_ONLY ansi:yellowBright
light-ansi claude ansi:redBright
light-ansi inactive ansi:blackBright
light-ansi inactiveShimmer ansi:white
light-ansi inverseText ansi:white
light-ansi permission ansi:blue
light-ansi selectionBg ansi:cyan
light-ansi subtle ansi:blackBright
light-ansi success ansi:green
light-ansi text ansi:black
light-ansi userMessageBackground ansi:white
light-ansi blue_FOR_SUBAGENTS_ONLY ansi:blue
light-ansi cyan_FOR_SUBAGENTS_ONLY ansi:cyan
light-ansi green_FOR_SUBAGENTS_ONLY ansi:green
light-ansi orange_FOR_SUBAGENTS_ONLY ansi:redBright
light-ansi purple_FOR_SUBAGENTS_ONLY ansi:magenta
light-ansi red_FOR_SUBAGENTS_ONLY ansi:red
light-ansi yellow_FOR_SUBAGENTS_ONLY ansi:yellow
`
