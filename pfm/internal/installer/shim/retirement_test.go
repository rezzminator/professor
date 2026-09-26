package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestShimSourceAndResourceRetireCCSurfaceWithoutShadowingCompiler(t *testing.T) {
	zsh, err := exec.LookPath("zsh")
	if err != nil {
		t.Skip("zsh is not installed")
	}
	home := t.TempDir()
	binDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeShimFile(t, filepath.Join(binDir, "pfm"), "#!/bin/sh\nexit 0\n")
	fakeBin := filepath.Join(home, "external-bin")
	if err := os.MkdirAll(fakeBin, 0o700); err != nil {
		t.Fatal(err)
	}
	writeShimFile(t, filepath.Join(fakeBin, "cc"), "#!/bin/sh\nexit 0\n")
	shim := quoteZsh(embeddedShimPath(t))
	script := `
cc() { :; }
cc1() { :; }
cc-ls() { :; }
_cc_run() { :; }
_cc_primary() { :; }
_cc_auto_open() { :; }
_pfm_eval() { :; }
ccache() { :; }
alias cc2=true
alias cc-swap=true
alias ccustom=true
precmd_functions=(_cc_auto_open operator_precmd)
source ` + shim + `
cc-open() { :; }
_cc_selfswitch() { :; }
alias cc-revive=true
source ` + shim + `
for retired in cc cc1 cc2 cc-ls cc-open cc-swap cc-revive _cc_run _cc_primary _cc_auto_open _cc_selfswitch _pfm_eval; do
  if (( ${+functions[$retired]} || ${+aliases[$retired]} )); then print -r -- "retained=$retired"; fi
done
print -r -- "compiler=$(whence -p cc)"
print -r -- "cx=$(typeset -f cx >/dev/null 2>&1 && print yes || print no)"
print -r -- "foreign-function=$(typeset -f ccache >/dev/null 2>&1 && print yes || print no)"
print -r -- "foreign-alias=$([[ ${aliases[ccustom]-} == true ]] && print yes || print no)"
print -r -- "precmd=${(j:,:)precmd_functions}"
`
	command := jailedZshCommand(zsh, script, home, "PATH="+fakeBin+":/usr/bin:/bin")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("source shim twice: %v: %s", err, output)
	}
	got := string(output)
	if strings.Contains(got, "retained=") {
		t.Fatalf("re-sourced shim retained a legacy public function, alias, or helper: %q", got)
	}
	for _, want := range []string{
		"compiler=" + fakeBin + "/cc\n",
		"cx=yes\n",
		"foreign-function=yes\n",
		"foreign-alias=yes\n",
		"precmd=operator_precmd\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("re-sourced shim output %q lacks %q", got, want)
		}
	}
}
