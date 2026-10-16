package themesync

import (
	"strings"
	"testing"
)

func TestFollowerPlistHoldsTheContractedKeys(t *testing.T) {
	plist := followerPlist("/opt/pfm", "/h", "/h/.local/state/pfm/theme")
	for _, want := range []string{
		"<key>Label</key>\n\t<string>com.professor.pfm.theme</string>",
		"<string>/opt/pfm</string>\n\t\t<string>theme</string>\n\t\t<string>sync</string>",
		"<key>WatchPaths</key>\n\t<array>\n\t\t<string>/h/Library/Preferences/.GlobalPreferences.plist</string>",
		"<key>StartInterval</key>\n\t<integer>60</integer>",
		"<key>RunAtLoad</key>\n\t<true/>",
		"<key>StandardOutPath</key>\n\t<string>/h/.local/state/pfm/theme/follow.log</string>",
		"<key>StandardErrorPath</key>\n\t<string>/h/.local/state/pfm/theme/follow.log</string>",
	} {
		if !strings.Contains(plist, want) {
			t.Errorf("plist lacks %q:\n%s", want, plist)
		}
	}
	if !strings.HasPrefix(plist, "<?xml") || !strings.HasSuffix(plist, "</plist>\n") {
		t.Errorf("plist is not a whole document:\n%s", plist)
	}
}

func TestFollowerPlistEscapesXML(t *testing.T) {
	plist := followerPlist("/opt/p&fm<x>", "/h", "/s")
	if !strings.Contains(plist, "/opt/p&amp;fm&lt;x&gt;") {
		t.Fatalf("binary path not escaped:\n%s", plist)
	}
}
