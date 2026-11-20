package installer

import (
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
)

// herdrKeysDefaultBlock is the block pfm's keymap renders with herdr.keys unset.
const herdrKeysDefaultBlock = "# >>> pfm herdr keys >>>\n[keys]\nnext_workspace = \"prefix+m\"\nprevious_workspace = \"prefix+i\"\n\n" +
	"[[keys.command]]\nkey = \"prefix+a\"\ntype = \"shell\"\ndescription = \"new pfm chat in this space\"\n" +
	"command = \"\\\"${HERDR_BIN_PATH:-herdr}\\\" tab create --workspace \\\"${HERDR_ACTIVE_WORKSPACE_ID:-$HERDR_WORKSPACE_ID}\\\" --env PFM_AUTO_OPEN=pfm --focus\"\n" +
	"# <<< pfm herdr keys <<<\n"

const herdrKeysNewChat = `"${HERDR_BIN_PATH:-herdr}" tab create --workspace "${HERDR_ACTIVE_WORKSPACE_ID:-$HERDR_WORKSPACE_ID}" --env PFM_AUTO_OPEN=pfm --focus`

func herdrBinding(keys ...string) pfmconfig.HerdrKeyBinding {
	return pfmconfig.HerdrKeyBinding{Keys: keys}
}

func TestHerdrKeymapMergedOverPfmKeymap(t *testing.T) {
	newChat := pfmconfig.HerdrKeyCommand{
		Key: "prefix+a", Type: "shell", Description: "new pfm chat in this space", Command: herdrKeysNewChat,
	}
	previous := herdrKeyAction{name: "previous_workspace", binding: herdrBinding("prefix+i")}
	next := herdrKeyAction{name: "next_workspace", binding: herdrBinding("prefix+m")}
	zoom := pfmconfig.HerdrKeyCommand{Key: "prefix+z", Type: "shell", Command: "zoom"}
	cases := []struct {
		name string
		user *pfmconfig.HerdrKeys
		want herdrKeymap
	}{
		{
			name: "unset is pfm's keymap",
			want: herdrKeymap{[]herdrKeyAction{previous, next}, []pfmconfig.HerdrKeyCommand{newChat}},
		},
		{
			name: "empty object is pfm's keymap", user: &pfmconfig.HerdrKeys{},
			want: herdrKeymap{[]herdrKeyAction{previous, next}, []pfmconfig.HerdrKeyCommand{newChat}},
		},
		{
			name: "an action overrides, null removes, a new one is added",
			user: &pfmconfig.HerdrKeys{Actions: map[string]*pfmconfig.HerdrKeyBinding{
				"previous_workspace": {Keys: []string{"cmd+shift+up", "prefix+i"}, List: true},
				"next_workspace":     nil,
				"split_vertical":     {Keys: []string{"cmd+\\"}},
				"close_pane":         {Keys: []string{"ctrl+shift+`"}},
				"never_bound":        nil,
			}},
			want: herdrKeymap{
				[]herdrKeyAction{
					{
						name:    "previous_workspace",
						binding: pfmconfig.HerdrKeyBinding{Keys: []string{"cmd+shift+up", "prefix+i"}, List: true},
					},
					{name: "close_pane", binding: herdrBinding("ctrl+shift+`")},
					{name: "split_vertical", binding: herdrBinding("cmd+\\")},
				},
				[]pfmconfig.HerdrKeyCommand{newChat},
			},
		},
		{
			name: "a command replaces pfm's of its key and new keys follow in order",
			user: &pfmconfig.HerdrKeys{Commands: []pfmconfig.HerdrKeyCommand{
				zoom,
				{Key: "prefix+a", Type: "plugin", Command: "chat"},
				{Key: "prefix+b", Type: "shell", Command: "b"},
			}},
			want: herdrKeymap{
				[]herdrKeyAction{previous, next},
				[]pfmconfig.HerdrKeyCommand{
					{
						Key:     "prefix+a",
						Type:    "plugin",
						Command: "chat",
					},
					zoom,
					{Key: "prefix+b", Type: "shell", Command: "b"},
				},
			},
		},
		{
			name: "an entry holding only its key removes pfm's command",
			user: &pfmconfig.HerdrKeys{Commands: []pfmconfig.HerdrKeyCommand{
				{Key: "prefix+a", Remove: true}, {Key: "prefix+q", Remove: true},
			}},
			want: herdrKeymap{actions: []herdrKeyAction{previous, next}},
		},
	}
	for _, testCase := range cases {
		if got := herdrKeymapMerged(testCase.user); !reflect.DeepEqual(got, testCase.want) {
			t.Errorf("%s: herdrKeymapMerged = %+v, want %+v", testCase.name, got, testCase.want)
		}
	}
}

// TestHerdrKeysBlockForRendersAndRoundTrips holds the rendered block to its
// exact text and its decode to the keymap, keys with a backslash or a
// backtick included.
func TestHerdrKeysBlockForRendersAndRoundTrips(t *testing.T) {
	cases := []struct {
		name   string
		keymap herdrKeymap
		want   string
		decode map[string]any
	}{
		{
			name: "pfm's keymap", keymap: herdrKeymapDefault(), want: herdrKeysDefaultBlock,
			decode: map[string]any{
				"next_workspace": "prefix+m", "previous_workspace": "prefix+i",
				"command": []map[string]any{
					{
						"key":         "prefix+a",
						"type":        "shell",
						"description": "new pfm chat in this space",
						"command":     herdrKeysNewChat,
					},
				},
			},
		},
		{
			name: "backslash, backtick and a list",
			keymap: herdrKeymap{
				actions: []herdrKeyAction{
					{name: "split_vertical", binding: herdrBinding("cmd+\\")},
					{
						name:    "close_pane",
						binding: pfmconfig.HerdrKeyBinding{Keys: []string{"ctrl+shift+`", "prefix+x"}, List: true},
					},
				},
				commands: []pfmconfig.HerdrKeyCommand{{Key: "cmd+\\", Type: "shell", Command: `echo "a\b"`}},
			},
			want: "# >>> pfm herdr keys >>>\n[keys]\nclose_pane = [\"ctrl+shift+`\", \"prefix+x\"]\nsplit_vertical = \"cmd+\\\\\"\n\n" +
				"[[keys.command]]\nkey = \"cmd+\\\\\"\ntype = \"shell\"\ncommand = \"echo \\\"a\\\\b\\\"\"\n# <<< pfm herdr keys <<<\n",
			decode: map[string]any{
				"split_vertical": "cmd+\\", "close_pane": []any{"ctrl+shift+`", "prefix+x"},
				"command": []map[string]any{{"key": "cmd+\\", "type": "shell", "command": `echo "a\b"`}},
			},
		},
		{
			name: "everything removed", keymap: herdrKeymap{},
			want: "# >>> pfm herdr keys >>>\n[keys]\n# <<< pfm herdr keys <<<\n", decode: map[string]any{},
		},
	}
	for _, testCase := range cases {
		got, err := herdrKeysBlockFor(testCase.keymap)
		if err != nil || got != testCase.want {
			t.Errorf("%s: herdrKeysBlockFor = %q, %v\nwant %q", testCase.name, got, err, testCase.want)
			continue
		}
		var decoded map[string]any
		if _, err := toml.Decode(got, &decoded); err != nil || !reflect.DeepEqual(decoded["keys"], testCase.decode) {
			t.Errorf("%s: decoded %#v, %v\nwant %#v", testCase.name, decoded["keys"], err, testCase.decode)
		}
	}
}
