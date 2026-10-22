package installer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	pfmconfig "github.com/rezzminator/professor/pfm/internal/config"
	"github.com/rezzminator/professor/pfm/internal/deps"
	pfmengine "github.com/rezzminator/professor/pfm/internal/engine"
	"github.com/rezzminator/professor/pfm/internal/obs"
	"github.com/rezzminator/professor/pfm/internal/paths"
)

const harvesterConfigFileName = "harvester.config.json"

func harvesterBinaryPath(env paths.Env, home, version string) (string, error) {
	root, err := harvesterHome(env, home)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "bin", version, mcpServerHarvester), nil
}

func harvesterHome(env paths.Env, home string) (string, error) {
	if root := env.Get("HARVESTER_HOME"); root != "" {
		if !filepath.IsAbs(root) {
			return "", fmt.Errorf("harvester home HARVESTER_HOME must be absolute, got %q", root)
		}
		return root, nil
	}
	return filepath.Join(home, ".local", "share", mcpServerHarvester), nil
}

func harvesterConfigPath(env paths.Env, home string) string {
	if path := env.Get("HARVESTER_CONFIG"); path != "" {
		return path
	}
	root := env.Get("XDG_CONFIG_HOME")
	if root == "" {
		root = filepath.Join(home, ".config")
	}
	return filepath.Join(root, mcpServerHarvester, harvesterConfigFileName)
}

// migrateHarvesterConfig preserves opaque plugin fields and moves only enablement into pfm.
func (installer *engine) migrateHarvesterConfig() error {
	if installer.options.MCPConfigPath == "" {
		return nil
	}
	legacy := filepath.Join(filepath.Dir(installer.options.MCPConfigPath), harvesterConfigFileName)
	content, present, err := readMCPFile(legacy)
	if err != nil {
		return fmt.Errorf("read legacy harvester config %s: %w", legacy, err)
	}
	if !present {
		return nil
	}
	destination := harvesterConfigPath(installer.env(), installer.options.Home)
	if filepath.Clean(destination) == filepath.Clean(legacy) {
		return fmt.Errorf("harvester config migration: HARVESTER_CONFIG still names legacy path %s", legacy)
	}
	if _, err := os.Lstat(destination); err == nil {
		installer.skip(
			"harvester config migration: " + destination + " already exists; legacy file retained at " + legacy,
		)
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect harvester config %s: %w", destination, err)
	}
	var document map[string]json.RawMessage
	if err := json.Unmarshal(content, &document); err != nil || document == nil {
		return fmt.Errorf("parse legacy harvester config %s: expected JSON object: %v", legacy, err)
	}
	config, err := pfmconfig.Load(installer.options.MCPConfigPath, installer.options.Home, nil)
	if err != nil {
		return fmt.Errorf("load pfm config for harvester migration: %w", err)
	}
	enabled := config.MCPServers[pfmconfig.MCPServerHarvester].Enabled
	if value, present := document["enabled"]; present {
		var decoded *bool
		if err := json.Unmarshal(value, &decoded); err != nil || decoded == nil {
			return fmt.Errorf("parse legacy harvester config %s enabled: expected boolean: %v", legacy, err)
		}
		enabled = *decoded
	}
	delete(document, "enabled")
	if err := migrateHarvesterPreferences(document, installer.options.MCPConfigPath); err != nil {
		return err
	}
	wanted, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return fmt.Errorf("encode harvester config migration %s: %w", destination, err)
	}
	if err := installer.change("migrate harvester config "+legacy+" -> "+destination, func() error {
		if _, err := pfmconfig.SetMCPServerExplicit(config, pfmconfig.MCPServerHarvester, enabled); err != nil {
			return fmt.Errorf("migrate harvester enablement: %w", err)
		}
		if err := installer.writeMCPFile(destination, nil, append(wanted, '\n'), false); err != nil {
			return fmt.Errorf("write harvester config %s: %w", destination, err)
		}
		latest, err := os.ReadFile(legacy)
		if err != nil {
			return fmt.Errorf("recheck migrated harvester config %s: %w", legacy, err)
		}
		if !bytes.Equal(latest, content) {
			return fmt.Errorf("legacy harvester config %s changed during migration; retained for recovery", legacy)
		}
		if err := os.Remove(legacy); err != nil {
			return fmt.Errorf("remove migrated harvester config %s: %w", legacy, err)
		}
		return nil
	}); err != nil {
		return err
	}
	if installer.options.MCPEnabled == nil {
		installer.options.MCPEnabled = map[string]bool{}
	}
	installer.options.MCPEnabled[pfmconfig.MCPServerHarvester] = enabled
	return nil
}

// migrateHarvesterPreferences carries explicit fleet engine preferences into the plugin config.
func migrateHarvesterPreferences(document map[string]json.RawMessage, configPath string) error {
	content, err := os.ReadFile(configPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read pfm ask preferences %s: %w", configPath, err)
	}
	var legacy map[string]json.RawMessage
	if err := json.Unmarshal(content, &legacy); err != nil {
		return fmt.Errorf("decode pfm ask preferences %s: %w", configPath, err)
	}
	var oldAsk map[string]json.RawMessage
	if raw, present := legacy["ask"]; present {
		if err := json.Unmarshal(raw, &oldAsk); err != nil {
			return fmt.Errorf("decode pfm ask preferences %s: %w", configPath, err)
		}
	}
	engines := []string{pfmengine.MustLookup(pfmengine.Claude).LongName, pfmengine.MustLookup(pfmengine.Codex).LongName}
	for _, name := range engines {
		var enginePrefs map[string]json.RawMessage
		if raw, present := legacy[name]; present {
			if err := json.Unmarshal(raw, &enginePrefs); err != nil {
				return fmt.Errorf("decode pfm %s preferences: %w", name, err)
			}
		}
		binary := enginePrefs["binary"]
		if len(binary) == 0 {
			continue
		}
		prefs := map[string]json.RawMessage{}
		if raw, present := document[name]; present {
			if err := json.Unmarshal(raw, &prefs); err != nil || prefs == nil {
				return fmt.Errorf("decode harvester %s preferences: expected JSON object: %v", name, err)
			}
		}
		if _, present := prefs["binary"]; !present {
			prefs["binary"] = binary
		}
		document[name], err = json.Marshal(prefs)
		if err != nil {
			return fmt.Errorf("encode harvester %s preferences: %w", name, err)
		}
	}
	ask := map[string]json.RawMessage{}
	if raw, present := document["ask"]; present {
		if err := json.Unmarshal(raw, &ask); err != nil || ask == nil {
			return fmt.Errorf("decode harvester ask preferences: expected JSON object: %v", err)
		}
	}
	for _, key := range append([]string{"engine"}, engines...) {
		value, present := oldAsk[key]
		if !present {
			continue
		}
		if existing, exists := ask[key]; !exists {
			ask[key] = value
		} else if key != "engine" {
			var oldPrefs, prefs map[string]json.RawMessage
			if err := json.Unmarshal(value, &oldPrefs); err != nil {
				return fmt.Errorf("decode pfm ask.%s: %w", key, err)
			}
			if err := json.Unmarshal(existing, &prefs); err != nil || prefs == nil {
				return fmt.Errorf("decode harvester ask.%s: expected JSON object: %v", key, err)
			}
			for _, field := range []string{"model", "effort"} {
				if _, exists := prefs[field]; !exists && len(oldPrefs[field]) != 0 {
					prefs[field] = oldPrefs[field]
				}
			}
			ask[key], err = json.Marshal(prefs)
			if err != nil {
				return fmt.Errorf("encode harvester ask.%s: %w", key, err)
			}
		}
	}
	if len(ask) != 0 {
		document["ask"], err = json.Marshal(ask)
		if err != nil {
			return fmt.Errorf("encode harvester ask preferences: %w", err)
		}
	}
	return nil
}

// installHarvester provisions the standalone binary before migrating and invoking setup.
func (installer *engine) installHarvester(ctx context.Context) error {
	legacy := ""
	if installer.options.MCPConfigPath != "" {
		path := filepath.Join(filepath.Dir(installer.options.MCPConfigPath), harvesterConfigFileName)
		if _, err := os.Lstat(path); err == nil {
			legacy = path
		} else if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("inspect legacy harvester config %s: %w", path, err)
		}
	}
	configured := legacy != "" || installer.options.MCPEnabled[pfmconfig.MCPServerHarvester] ||
		installer.options.ClaudePluginsDefault
	for _, plugin := range installer.options.ClaudePlugins {
		configured = configured || plugin.Name() == mcpServerHarvester
	}
	if !configured {
		return nil
	}
	root, err := harvesterHome(installer.env(), installer.options.Home)
	if err != nil {
		return err
	}
	stable := filepath.Join(root, "bin", mcpServerHarvester)
	var binary string
	if obs.AlphaBuild(installer.options.Version) {
		sourceRepo, sourceErr := installer.globalSourceRepoRoot()
		descriptor := goBinaryPlugins[mcpServerHarvester]
		target := localClaudePluginTarget(
			ClaudePluginTarget{Name: mcpServerHarvester, Source: descriptor.repository},
			ClaudePluginBuild{
				Version:       installer.options.Version,
				Home:          installer.options.Home,
				CheckoutRoot:  installer.options.ClaudePluginCheckoutRoot,
				SourceRepo:    sourceRepo,
				SourceRepoErr: sourceErr,
			},
		)
		if !target.Local {
			return fmt.Errorf("harvester source build unavailable: %s%s", target.Undecided, target.Fallback)
		}
		if err := installer.ensureGoPluginBinary(ctx, &target); err != nil {
			return err
		}
		version, err := callmeterCheckoutVersion(target.Checkout)
		if err != nil {
			return err
		}
		binary, err = descriptor.binaryPath(installer.env(), installer.options.Home, version)
		if err != nil {
			return err
		}
	} else {
		binary, err = installer.ensureHarvesterRelease(ctx, root)
		if err != nil {
			return err
		}
	}
	if installer.apply && !callmeterExecutable(binary) {
		return fmt.Errorf("harvester plugin binary %s missing or not executable", binary)
	}
	if _, err := installer.ensureLink(binary, stable); err != nil {
		return fmt.Errorf("link harvester stable binary: %w", err)
	}
	link := mcpFamilyBinary(installer.options.Home, pfmconfig.MCPServerHarvester)
	if installer.apply && !callmeterExecutable(stable) {
		return fmt.Errorf("harvester plugin binary %s missing or not executable", stable)
	}
	if _, err := installer.ensureLink(stable, link); err != nil {
		return fmt.Errorf("link harvester onto PATH: %w", err)
	}
	if err := installer.migrateHarvesterConfig(); err != nil {
		return err
	}
	if err := installer.convergeMCPDaemon(ctx); err != nil {
		return err
	}
	args := []string{"setup"}
	if installer.options.Offline {
		args = append(args, "--offline")
	}
	return installer.change("run harvester "+strings.Join(args, " "), func() error {
		return installer.runHarvester(ctx, link, args...)
	})
}

func (installer *engine) runHarvester(ctx context.Context, binary string, args ...string) error {
	result, err := installer.processRunner().Run(ctx, append([]string{binary}, args...), deps.RunOptions{})
	if len(result.Stdout) > 0 {
		installer.say("%s", strings.TrimSpace(string(result.Stdout)))
	}
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf(
			"run %s %s: exit %d: %v: %s",
			binary,
			strings.Join(args, " "),
			result.ExitCode,
			err,
			strings.TrimSpace(string(result.Stderr)),
		)
	}
	return nil
}
