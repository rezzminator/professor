package chat

import (
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/rezzminator/professor/pfm/internal/atomicfile"
	"github.com/rezzminator/professor/pfm/internal/compose"
	"github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/paths"
	"github.com/rezzminator/professor/pfm/internal/testjail"
)

func TestRestoredRecipeCarriesTheRootSettingsEnvs(t *testing.T) {
	for _, kind := range []compose.Kind{compose.ResumeClaude, compose.ResumeCodex} {
		t.Run(kind.String(), func(t *testing.T) {
			fleet := testjail.Fleet(t)
			bin := t.TempDir()
			for _, name := range []string{"claude", "codex"} {
				script := []byte("#!/bin/sh\nexit 0\n")
				if err := testjail.WriteExecutable(filepath.Join(bin, name), script, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			credentials := []byte(`{"tokens":{"access_token":"jailed","account_id":"jailed-account"}}`)
			if err := atomicfile.Write(filepath.Join(fleet, "codex", "auth.json"), credentials, 0o600); err != nil {
				t.Fatal(err)
			}
			runtime, err := config.LoadRuntime("")
			if err != nil {
				t.Fatal(err)
			}
			runtime.Config.Claude.Binary = filepath.Join(bin, "claude")
			root := t.TempDir()
			for path, body := range map[string]string{
				filepath.Join(root, ".professor", "baseline.json"): "{}",
				paths.Settings(root): `{"envs":{"TEAM_MODE":"strict"}}`,
			} {
				if err := atomicfile.Write(path, []byte(body), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			row := compose.Row{Kind: kind, CWD: root, Name: "restored"}
			account := runtime.Config.PrimaryAccountFor(compose.EngineForKind(kind), 1)
			recipe, err := restoredRecipe(row, "c8b40132-3190-4b56-9368-d2f78930a340", account, runtime, io.Discard)
			if err != nil || recipe == nil || !slices.Contains(recipe.Env, "TEAM_MODE=strict") {
				t.Fatalf(
					"restoredRecipe(%s) = %+v, %v; want TEAM_MODE=strict from %s",
					kind,
					recipe,
					err,
					paths.Settings(root),
				)
			}
		})
	}
}
