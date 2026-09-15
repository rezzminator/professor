package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// Migration is the one-time move from the pre-split layout: config.json →
// pfm.config.json, mcp.servers.harvester → harvester.config.json, and the
// init-written loopback port 8377 → 18377. `pfm install` plans it in the
// preview and applies it before it wires clients, so registrations and the
// daemon agree on the port.
type Migration struct {
	LegacyPath string // non-empty: rename this file to Path
	Path       string
	// StrayLegacyPath is a pre-split config.json left beside an existing
	// pfm.config.json — an interrupted migration's leftover; it is parked.
	StrayLegacyPath string
	// HarvesterEnabled is the legacy mcp.servers.harvester.enabled to move.
	HarvesterEnabled *bool
	MovePort         bool
	// PortKept names why an init-written 8377 stays: its target collides.
	PortKept        string
	harvesterPath   string
	harvesterExists bool
}

// Empty reports whether the machine already has the current layout.
func (migration Migration) Empty() bool {
	return migration.LegacyPath == "" && migration.StrayLegacyPath == "" && migration.HarvesterEnabled == nil &&
		!migration.MovePort
}

func (migration Migration) rewrites() bool {
	return migration.LegacyPath != "" || migration.HarvesterEnabled != nil || migration.MovePort
}

// Steps renders each planned change as one human line, in apply order.
func (migration Migration) Steps() []string {
	var steps []string
	if migration.LegacyPath != "" {
		steps = append(
			steps,
			fmt.Sprintf(
				"rename %s → %s (pre-split copy kept as %s)",
				migration.LegacyPath,
				migration.Path,
				LegacyBackupName,
			),
		)
	}
	if migration.HarvesterEnabled != nil {
		steps = append(
			steps,
			fmt.Sprintf(
				"move mcp.servers.harvester.enabled=%t → %s",
				*migration.HarvesterEnabled,
				migration.harvesterPath,
			),
		)
	}
	if migration.MovePort {
		steps = append(
			steps,
			fmt.Sprintf(
				"move mcp.http.port %d → %d (client registrations re-wire in this same install)",
				legacyDefaultMCPPort,
				DefaultMCPPort,
			),
		)
	}
	if migration.PortKept != "" {
		steps = append(steps, migration.PortKept)
	}
	if migration.StrayLegacyPath != "" {
		steps = append(
			steps,
			fmt.Sprintf(
				"park leftover pre-split %s as %s (%s already holds the migrated config)",
				migration.StrayLegacyPath,
				LegacyBackupName,
				migration.Path,
			),
		)
	}
	return steps
}

// Preview returns config as the installer will wire it after ApplyMigration,
// so an install preview wires exactly the port the apply will. Path stays on
// the file that exists today: the preview's own reads (the retired authToken
// probe among them) must see real content, not a file the apply has yet to write.
func (migration Migration) Preview(config Config) Config {
	if migration.MovePort {
		config.MCP.HTTP.Port = DefaultMCPPort
	}
	return config
}

// PlanMigration inspects the machine's pfm config without modifying it.
func PlanMigration(config Config) (Migration, error) {
	migration := Migration{Path: config.Path}
	if !config.Exists {
		return migration, nil
	}
	if filepath.Base(config.Path) == LegacyFileName {
		migration.LegacyPath = config.Path
		migration.Path = filepath.Join(filepath.Dir(config.Path), FileName)
	} else {
		stray := filepath.Join(filepath.Dir(config.Path), LegacyFileName)
		exists, err := pathExists(stray)
		if err != nil {
			return Migration{}, err
		}
		if exists {
			migration.StrayLegacyPath = stray
		}
	}
	migration.harvesterPath = HarvesterPath(migration.Path)
	top, err := readTopLevel(config.Path)
	if err != nil {
		return Migration{}, err
	}
	mcpObject, err := decodeObject(top["mcp"], config.Path, "mcp")
	if err != nil {
		return Migration{}, err
	}
	servers, err := decodeObject(mcpObject["servers"], config.Path, "mcp.servers")
	if err != nil {
		return Migration{}, err
	}
	if content, found := servers[mcpServerHarvester]; found {
		var server rawMCPServer
		if err := decodeStrict(content, &server); err != nil {
			return Migration{}, fmt.Errorf("decode config %s mcp.servers.harvester: %w", config.Path, err)
		}
		enabled := false
		if server.Enabled != nil {
			enabled = *server.Enabled
		}
		migration.HarvesterEnabled = &enabled
	}
	if content, found := mcpObject["http"]; found {
		var httpValue rawMCPHTTP
		if err := json.Unmarshal(content, &httpValue); err != nil {
			return Migration{}, fmt.Errorf("decode config %s mcp.http: %w", config.Path, err)
		}
		migration.MovePort = httpValue.Port == legacyDefaultMCPPort
	}
	if external := config.Harvester.External; migration.MovePort && external.Enabled &&
		external.Port == DefaultMCPPort {
		// Moving the loopback port onto the external gateway's port would turn
		// a working machine into one whose config refuses to load.
		migration.MovePort = false
		migration.PortKept = fmt.Sprintf(
			"keep mcp.http.port %d: external.port in %s already uses %d",
			legacyDefaultMCPPort,
			migration.harvesterPath,
			DefaultMCPPort,
		)
	}
	if _, err := os.Stat(migration.harvesterPath); err == nil {
		migration.harvesterExists = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Migration{}, fmt.Errorf("inspect harvester config %s: %w", migration.harvesterPath, err)
	}
	return migration, nil
}

// ApplyMigration performs a planned migration. Every write is atomic; the
// pre-split file is parked only after its successor landed. An interruption
// between the two leaves both files, which the next PlanMigration reports as
// StrayLegacyPath and parks — never silently treated as done.
func ApplyMigration(migration Migration) error {
	if migration.Empty() {
		return nil
	}
	if migration.rewrites() {
		if err := rewriteMigratedConfig(migration); err != nil {
			return err
		}
	}
	park := migration.LegacyPath
	if park == "" {
		park = migration.StrayLegacyPath
	}
	if park == "" {
		return nil
	}
	backup := filepath.Join(filepath.Dir(park), LegacyBackupName)
	if exists, err := pathExists(backup); err != nil {
		return err
	} else if exists {
		identical, err := filesIdentical(park, backup)
		if err != nil {
			return err
		}
		if identical {
			if err := os.Remove(park); err != nil {
				return fmt.Errorf("remove pre-split config %s already parked as %s: %w", park, backup, err)
			}
			return nil
		}
		parkSize, backupSize, err := fileSizes(park, backup)
		if err != nil {
			return err
		}
		return fmt.Errorf(
			"park pre-split config %s: %s already exists with different content (%d vs %d bytes); compare the two, remove the one you no longer need, and rerun",
			park,
			backup,
			parkSize,
			backupSize,
		)
	}
	if err := os.Rename(park, backup); err != nil {
		return fmt.Errorf("park pre-split config %s as %s: %w", park, backup, err)
	}
	return nil
}

// filesIdentical compares two local files byte-for-byte; a SHA is not needed
// for two files already on disk.
func filesIdentical(a, b string) (bool, error) {
	aContent, err := os.ReadFile(a)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", a, err)
	}
	bContent, err := os.ReadFile(b)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", b, err)
	}
	return bytes.Equal(aContent, bContent), nil
}

// fileSizes reads both files' sizes for the differing-content refusal
// message; a read error is reported against the file it came from, never
// folded into "differ".
func fileSizes(a, b string) (int64, int64, error) {
	aInfo, err := os.Stat(a)
	if err != nil {
		return 0, 0, fmt.Errorf("inspect %s: %w", a, err)
	}
	bInfo, err := os.Stat(b)
	if err != nil {
		return 0, 0, fmt.Errorf("inspect %s: %w", b, err)
	}
	return aInfo.Size(), bInfo.Size(), nil
}

func rewriteMigratedConfig(migration Migration) error {
	source := migration.Path
	if migration.LegacyPath != "" {
		source = migration.LegacyPath
	}
	top, err := readTopLevel(source)
	if err != nil {
		return err
	}
	mcpObject, err := decodeObject(top["mcp"], source, "mcp")
	if err != nil {
		return err
	}
	if migration.HarvesterEnabled != nil {
		if err := moveHarvesterEnabled(migration); err != nil {
			return err
		}
		servers, err := decodeObject(mcpObject["servers"], source, "mcp.servers")
		if err != nil {
			return err
		}
		delete(servers, mcpServerHarvester)
		if len(servers) == 0 {
			delete(mcpObject, "servers")
		} else {
			mcpObject["servers"], _ = json.Marshal(servers)
		}
	}
	if migration.MovePort {
		mcpObject["http"], _ = json.Marshal(map[string]int{jsonKeyPort: DefaultMCPPort})
	}
	if len(mcpObject) == 0 {
		delete(top, "mcp")
	} else {
		top["mcp"], _ = json.Marshal(mcpObject)
	}
	content, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("encode migrated config %s: %w", migration.Path, err)
	}
	return writeAtomic(migration.Path, append(content, '\n'))
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("inspect %s: %w", path, err)
}

// moveHarvesterEnabled writes the legacy flag into harvester.config.json. An
// existing harvester file that already sets enabled wins; its other keys are
// preserved byte-for-byte in meaning.
func moveHarvesterEnabled(migration Migration) error {
	top := map[string]json.RawMessage{}
	if migration.harvesterExists {
		existing, err := readTopLevel(migration.harvesterPath)
		if err != nil {
			return err
		}
		top = existing
	}
	if _, set := top[jsonKeyEnabled]; !set {
		top[jsonKeyEnabled], _ = json.Marshal(*migration.HarvesterEnabled)
	}
	content, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("encode harvester config %s: %w", migration.harvesterPath, err)
	}
	return writeAtomic(migration.harvesterPath, append(content, '\n'))
}

func readTopLevel(path string) (map[string]json.RawMessage, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}
	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(content, &top); err != nil {
		return nil, configJSONError(path, err)
	}
	return top, nil
}

func decodeObject(content json.RawMessage, path, key string) (map[string]json.RawMessage, error) {
	object := map[string]json.RawMessage{}
	if len(content) == 0 {
		return object, nil
	}
	if err := json.Unmarshal(content, &object); err != nil {
		return nil, fmt.Errorf("decode config %s %s: %w", path, key, err)
	}
	return object, nil
}
