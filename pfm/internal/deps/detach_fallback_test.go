package deps

import "testing"

// TestNohupAppliesToEveryPlatformThatLacksSetsid pins the detach fallback's
// reason for existing. pfmcall.detachThenWaiter resolves setsid first and falls
// back to nohup; setsid ships with util-linux and is absent on darwin, so
// darwin is the ONLY platform that ever reaches the fallback. Gating nohup to
// linux therefore made Resolve refuse a binary that /usr/bin/nohup provides on
// every macOS, and the fallback could never engage on the one platform written
// for it.
//
// nohup also carries no VersionArgs: BSD nohup rejects --version outright
// ("illegal option -- -"), which a version probe would report as StateBroken.
// It is a non-Required entry with no MinVersion, so presence is the only fact
// the resolver needs.
func TestNohupAppliesToEveryPlatformThatLacksSetsid(t *testing.T) {
	var nohup, setsid *Entry
	for index, entry := range Registry() {
		switch entry.Name {
		case "nohup":
			nohup = &Registry()[index]
		case "setsid":
			setsid = &Registry()[index]
		}
	}
	if nohup == nil || setsid == nil {
		t.Fatalf("registry lost a detach dependency: nohup=%v setsid=%v", nohup != nil, setsid != nil)
	}
	for _, platform := range []string{"linux", "darwin"} {
		if !nohup.AppliesTo(platform) {
			t.Errorf("nohup must apply to %s — it is the setsid fallback and %s ships it", platform, platform)
		}
	}
	if setsid.AppliesTo("darwin") {
		t.Error("setsid must not apply to darwin — macOS ships no setsid, which is why the nohup fallback exists")
	}
	if len(nohup.VersionArgs) != 0 {
		t.Errorf("nohup must carry no VersionArgs: BSD nohup rejects --version, got %v", nohup.VersionArgs)
	}
	if nohup.Required {
		t.Error("nohup must stay non-Required — it is a fallback, not a hard dependency")
	}
}
